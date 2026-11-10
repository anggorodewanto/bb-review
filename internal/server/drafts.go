package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/anggorodewanto/bb-review/internal/bitbucket"
	"github.com/anggorodewanto/bb-review/internal/store"
)

// failedDraft reports one draft that could not be posted during submit.
type failedDraft struct {
	Draft bitbucket.Draft `json:"draft"`
	Error string          `json:"error"`
}

// submitResponse is the POST {pr}/drafts/submit response.
type submitResponse struct {
	Posted []bitbucket.Comment `json:"posted"`
	Failed []failedDraft       `json:"failed"`
}

// submitRequest is the POST {pr}/drafts/submit body.
type submitRequest struct {
	Action *string `json:"action"`
}

// handleDrafts lists the local drafts for a pull request.
func (s *Server) handleDrafts(w http.ResponseWriter, r *http.Request) {
	repo, id, ok := s.mustPR(w, r)
	if !ok {
		return
	}
	drafts := s.store.Drafts(repo.PRKey(id))
	if drafts == nil {
		drafts = []bitbucket.Draft{}
	}
	writeJSON(w, http.StatusOK, drafts)
}

// handleCreateDraft stores a new local draft.
func (s *Server) handleCreateDraft(w http.ResponseWriter, r *http.Request) {
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
	draft, err := s.store.AddDraft(repo.PRKey(id), req.Raw, req.Inline, req.ParentID)
	if err != nil {
		s.writeStoreError(w, "create draft", err)
		return
	}
	writeJSON(w, http.StatusOK, draft)
}

// handleUpdateDraft rewrites a draft's text.
func (s *Server) handleUpdateDraft(w http.ResponseWriter, r *http.Request) {
	repo, id, ok := s.mustPR(w, r)
	if !ok {
		return
	}
	var req struct {
		Raw string `json:"raw"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Raw) == "" {
		writeError(w, http.StatusBadRequest, "raw must not be empty")
		return
	}
	draft, err := s.store.UpdateDraft(repo.PRKey(id), r.PathValue("draftID"), req.Raw)
	if err != nil {
		s.writeStoreError(w, "update draft", err)
		return
	}
	writeJSON(w, http.StatusOK, draft)
}

// handleDeleteDraft removes a draft.
func (s *Server) handleDeleteDraft(w http.ResponseWriter, r *http.Request) {
	repo, id, ok := s.mustPR(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteDraft(repo.PRKey(id), r.PathValue("draftID")); err != nil {
		s.writeStoreError(w, "delete draft", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSubmitDrafts posts every draft in creation order, deletes the ones that
// were posted, then applies the review action.
func (s *Server) handleSubmitDrafts(w http.ResponseWriter, r *http.Request) {
	repo, id, ok := s.mustPR(w, r)
	if !ok {
		return
	}
	var req submitRequest
	if err := decodeOptionalJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	action, ok := validSubmitAction(w, req.Action)
	if !ok {
		return
	}

	ctx := r.Context()
	prKey := repo.PRKey(id)
	drafts := s.store.Drafts(prKey)
	response := submitResponse{Posted: []bitbucket.Comment{}, Failed: []failedDraft{}}

	for _, draft := range drafts {
		comment, err := s.bb.PostComment(ctx, repo, id, draft.Raw, draft.Inline, draft.ParentID)
		if err != nil {
			response.Failed = append(response.Failed, failedDraft{Draft: draft, Error: upstreamMessage(err)})
			s.log.Warn("submit draft failed", "repo", repo.String(), "id", id, "draft", draft.ID, "err", err.Error())
			continue
		}
		response.Posted = append(response.Posted, comment)
		if err := s.store.DeleteDraft(prKey, draft.ID); err != nil {
			s.log.Warn("delete posted draft failed", "repo", repo.String(), "id", id, "draft", draft.ID, "err", err.Error())
		}
	}

	if action != "" && !s.applySubmitAction(w, r, repo, id, action) {
		return
	}
	if len(response.Posted) > 0 || action != "" {
		s.refreshRepoAfterWrite(ctx, repo)
	}
	writeJSON(w, http.StatusOK, response)
}

// applySubmitAction performs the optional approve / request changes step.
func (s *Server) applySubmitAction(w http.ResponseWriter, r *http.Request, repo bitbucket.Repo, id int, action string) bool {
	switch action {
	case "approve":
		if err := s.bb.Approve(r.Context(), repo, id, true); err != nil {
			s.writeUpstream(w, "submit approve", err)
			return false
		}
	case "request_changes":
		if err := s.bb.RequestChanges(r.Context(), repo, id, true); err != nil {
			s.writeUpstream(w, "submit request changes", err)
			return false
		}
	}
	return true
}

// validSubmitAction normalises and validates the optional action field.
func validSubmitAction(w http.ResponseWriter, action *string) (string, bool) {
	if action == nil {
		return "", true
	}
	value := strings.TrimSpace(*action)
	switch value {
	case "":
		return "", true
	case "approve", "request_changes":
		return value, true
	default:
		writeError(w, http.StatusBadRequest, `action must be "approve", "request_changes" or null`)
		return "", false
	}
}

// writeStoreError maps local state errors onto HTTP responses.
func (s *Server) writeStoreError(w http.ResponseWriter, context string, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeNotFound(w, "draft not found")
		return
	}
	s.log.Error("state error", "context", context, "err", err.Error())
	writeError(w, http.StatusInternalServerError, "local state error")
}

// upstreamMessage returns the human readable part of an upstream error.
func upstreamMessage(err error) string {
	var apiErr *bitbucket.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Message
	}
	return err.Error()
}

// decodeOptionalJSON decodes a body that may be absent entirely.
func decodeOptionalJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	err := decoder.Decode(v)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
