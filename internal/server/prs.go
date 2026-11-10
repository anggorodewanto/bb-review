package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/anggorodewanto/bb-review/internal/bitbucket"
	"github.com/anggorodewanto/bb-review/internal/scanner"
)

// handleMe returns the authenticated user.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	me, err := s.bb.Me(r.Context())
	s.debugUpstream("me", start)
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
	start := time.Now()
	prs, err := s.bb.AuthorPRs(r.Context(), s.cfg.Workspace, state)
	s.debugUpstream("pr list author", start)
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
	// Build states come from the client cache; only misses cost a request, and
	// none wait longer than the list budget.
	start := time.Now()
	s.bb.FillBuildStates(r.Context(), snapshot.PRs, bitbucket.DefaultBuildBudget)
	s.debugUpstream("pr list reviewer build states", start)
	writeJSON(w, http.StatusOK, snapshot.PRs)
}

// handleStats reports upstream request volume and scanner health.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	resp := statsResponse{}
	if s.bb != nil {
		resp.UpstreamLastHour = s.bb.RequestsLastHour()
	}
	if s.scanner != nil {
		resp.Scanner = s.scanner.Stats()
	}
	writeJSON(w, http.StatusOK, resp)
}

// statsResponse is GET /api/stats.
type statsResponse struct {
	UpstreamLastHour int           `json:"upstream_last_hour"`
	Scanner          scanner.Stats `json:"scanner"`
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
	start := time.Now()
	detail, err := s.bb.PRDetail(r.Context(), repo, id)
	s.debugUpstream("pr detail", start, "repo", repo.String(), "id", id)
	if err != nil {
		s.writeUpstream(w, "pr detail", err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleDiff returns the unified diff as text/plain, gzipped when asked for.
func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request) {
	repo, id, ok := s.mustPR(w, r)
	if !ok {
		return
	}
	start := time.Now()
	body, err := s.bb.Diff(r.Context(), repo, id)
	s.debugUpstream("pr diff", start, "repo", repo.String(), "id", id)
	if err != nil {
		s.writeUpstream(w, "pr diff", err)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Cache-Control", "no-store")
	writeMaybeGzipped(w, r, body)
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
