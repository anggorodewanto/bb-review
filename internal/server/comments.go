package server

import (
	"net/http"
	"strings"

	"github.com/anggorodewanto/bb-review/internal/bitbucket"
)

// handleComments returns every comment on a pull request, oldest first.
func (s *Server) handleComments(w http.ResponseWriter, r *http.Request) {
	repo, id, ok := s.mustPR(w, r)
	if !ok {
		return
	}
	comments, err := s.bb.ListComments(r.Context(), repo, id)
	if err != nil {
		s.writeUpstream(w, "comments", err)
		return
	}
	if comments == nil {
		comments = []bitbucket.Comment{}
	}
	writeJSON(w, http.StatusOK, comments)
}

// handleCreateComment posts a comment immediately.
func (s *Server) handleCreateComment(w http.ResponseWriter, r *http.Request) {
	repo, id, ok := s.mustPR(w, r)
	if !ok {
		return
	}
	var req commentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !validateComment(w, req) {
		return
	}
	comment, err := s.bb.PostComment(r.Context(), repo, id, req.Raw, req.Inline, req.ParentID)
	if err != nil {
		s.writeUpstream(w, "post comment", err)
		return
	}
	s.refreshRepoAfterWrite(r.Context(), repo)
	writeJSON(w, http.StatusOK, comment)
}

// commentRequest is both the POST comment and the POST draft body.
type commentRequest struct {
	Raw      string                  `json:"raw"`
	Inline   *bitbucket.InlineAnchor `json:"inline"`
	ParentID *int                    `json:"parent_id"`
}

// validateComment checks the fields shared by comments and drafts.
func validateComment(w http.ResponseWriter, req commentRequest) bool {
	if strings.TrimSpace(req.Raw) == "" {
		writeError(w, http.StatusBadRequest, "raw must not be empty")
		return false
	}
	if req.Inline == nil {
		return true
	}
	if strings.TrimSpace(req.Inline.Path) == "" {
		writeError(w, http.StatusBadRequest, "inline.path must not be empty")
		return false
	}
	if req.Inline.From == nil && req.Inline.To == nil {
		writeError(w, http.StatusBadRequest, "inline needs one of from or to")
		return false
	}
	if req.Inline.From != nil && req.Inline.To != nil {
		writeError(w, http.StatusBadRequest, "inline must not set both from and to")
		return false
	}
	return true
}
