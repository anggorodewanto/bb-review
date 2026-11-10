package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/anggorodewanto/bb-review/internal/bitbucket"
)

// handleMe returns the authenticated user.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	me, err := s.bb.Me(r.Context())
	if err != nil {
		s.writeUpstream(w, "me", err)
		return
	}
	writeJSON(w, http.StatusOK, me)
}

// handlePRList returns open pull requests for the reviewer or author role.
//
// reviewer: served straight from scanner state (never blocks on Bitbucket).
// author: fetched live from the workspace endpoint.
func (s *Server) handlePRList(w http.ResponseWriter, r *http.Request) {
	role := strings.TrimSpace(r.URL.Query().Get("role"))
	if role == "" {
		role = "reviewer"
	}
	if role != "reviewer" && role != "author" {
		writeError(w, http.StatusBadRequest, `role must be "reviewer" or "author"`)
		return
	}

	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if state == "" {
		state = "OPEN"
	}

	if role == "author" {
		s.writeAuthorPRList(w, r, state)
		return
	}
	s.writeReviewerPRList(w, r)
}

// writeAuthorPRList fetches the author list live.
func (s *Server) writeAuthorPRList(w http.ResponseWriter, r *http.Request, state string) {
	prs, err := s.bb.AuthorPRs(r.Context(), s.cfg.Workspace, state)
	if err != nil {
		if bitbucket.IsRateLimited(err) {
			w.Header().Set("X-BBR-Rate-Limited", "1")
		}
		s.writeUpstream(w, "pr list", err)
		return
	}
	w.Header().Set("X-BBR-Fetched-At", time.Now().UTC().Format(time.RFC3339))
	if prs == nil {
		prs = []bitbucket.PRSummary{}
	}
	writeJSON(w, http.StatusOK, prs)
}

// writeReviewerPRList answers from scanner state with freshness/progress headers.
func (s *Server) writeReviewerPRList(w http.ResponseWriter, r *http.Request) {
	if s.scanner == nil {
		writeError(w, http.StatusInternalServerError, "scanner is not configured")
		return
	}
	snapshot := s.scanner.Snapshot()

	header := w.Header()
	if !snapshot.FetchedAt.IsZero() {
		header.Set("X-BBR-Fetched-At", snapshot.FetchedAt.UTC().Format(time.RFC3339))
	}
	if snapshot.Scanning {
		header.Set("X-BBR-Scanning", fmt.Sprintf("%d/%d", snapshot.Done, snapshot.Total))
	}
	if len(snapshot.Failed) > 0 {
		header.Set("X-BBR-Partial", strings.Join(snapshot.Failed, ","))
	}
	if snapshot.RateLimited {
		header.Set("X-BBR-Rate-Limited", "1")
	}
	if snapshot.PRs == nil {
		snapshot.PRs = []bitbucket.PRSummary{}
	}
	writeJSON(w, http.StatusOK, snapshot.PRs)
}

// handleScan triggers an immediate scan tick. Debounced by the scanner itself.
func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	if s.scanner == nil {
		writeError(w, http.StatusInternalServerError, "scanner is not configured")
		return
	}
	writeJSON(w, http.StatusOK, scanResponse{Started: s.scanner.Trigger(r.Context())})
}

type scanResponse struct {
	Started bool `json:"started"`
}

// refreshRepoAfterWrite re-scans one repo so the reviewer list reflects a comment,
// approval or change request.
func (s *Server) refreshRepoAfterWrite(ctx context.Context, repo bitbucket.Repo) {
	if s.scanner == nil {
		return
	}
	if err := s.scanner.RefreshRepo(ctx, repo); err != nil {
		s.log.Warn("refresh repo after write failed", "repo", repo.String(), "err", err.Error())
	}
}

// handlePRDetail returns the full pull request view.
func (s *Server) handlePRDetail(w http.ResponseWriter, r *http.Request) {
	repo, id, ok := s.mustPR(w, r)
	if !ok {
		return
	}
	detail, err := s.bb.PRDetail(r.Context(), repo, id)
	if err != nil {
		s.writeUpstream(w, "pr detail", err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleDiff streams the unified diff as text/plain.
func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request) {
	repo, id, ok := s.mustPR(w, r)
	if !ok {
		return
	}
	body, err := s.bb.Diff(r.Context(), repo, id)
	if err != nil {
		s.writeUpstream(w, "pr diff", err)
		return
	}
	defer body.Close()

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, body); err != nil {
		s.log.Debug("diff stream ended early", "repo", repo.String(), "id", id, "err", err.Error())
	}
}

// mustPR resolves the path parameters or writes a 400.
func (s *Server) mustPR(w http.ResponseWriter, r *http.Request) (bitbucket.Repo, int, bool) {
	repo, id, err := s.prFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return bitbucket.Repo{}, 0, false
	}
	return repo, id, true
}
