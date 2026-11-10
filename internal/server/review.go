package server

import (
	"net/http"

	"github.com/anggorodewanto/bb-review/internal/bitbucket"
)

// reviewActionKind is the write performed by handleReviewAction.
type reviewActionKind int

const (
	reviewApprove reviewActionKind = iota
	reviewRequestChanges
)

// handleReviewAction approves / unapproves / requests changes / withdraws them.
func (s *Server) handleReviewAction(kind reviewActionKind, add bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, id, ok := s.mustPR(w, r)
		if !ok {
			return
		}
		ctx := r.Context()

		var err error
		switch kind {
		case reviewApprove:
			err = s.bb.Approve(ctx, repo, id, add)
		case reviewRequestChanges:
			err = s.bb.RequestChanges(ctx, repo, id, add)
		}
		if err != nil {
			s.writeUpstream(w, "review action", err)
			return
		}

		detail, err := s.bb.PRDetail(ctx, repo, id)
		if err != nil {
			s.writeUpstream(w, "review state", err)
			return
		}
		writeJSON(w, http.StatusOK, reviewStateBody{MyState: detail.MyState})
	}
}

type reviewStateBody struct {
	MyState bitbucket.ReviewState `json:"my_state"`
}

// handleViewed returns the viewed-file state for a pull request.
func (s *Server) handleViewed(w http.ResponseWriter, r *http.Request) {
	repo, id, ok := s.mustPR(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.store.Viewed(repo.PRKey(id)))
}

// handleSetViewed replaces the viewed-file state for a pull request.
func (s *Server) handleSetViewed(w http.ResponseWriter, r *http.Request) {
	repo, id, ok := s.mustPR(w, r)
	if !ok {
		return
	}
	var state bitbucket.ViewedState
	if !decodeJSON(w, r, &state) {
		return
	}
	if state.Files == nil {
		state.Files = map[string]string{}
	}
	stored, err := s.store.SetViewed(repo.PRKey(id), state)
	if err != nil {
		s.writeStoreError(w, "set viewed", err)
		return
	}
	writeJSON(w, http.StatusOK, stored)
}
