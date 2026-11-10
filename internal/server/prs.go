package server

import (
	"io"
	"net/http"
	"strings"

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

// handlePRList returns open pull requests across the configured repos.
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

	repos := s.cfg.RepoRefs()
	result, err := s.bb.ListPRs(r.Context(), repos, role, state)
	if err != nil {
		s.writeUpstream(w, "pr list", err)
		return
	}
	if len(result.Failed) > 0 {
		w.Header().Set("X-BBR-Partial", strings.Join(result.Failed, ","))
	}
	if result.PRs == nil {
		result.PRs = []bitbucket.PRSummary{}
	}
	writeJSON(w, http.StatusOK, result.PRs)
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
