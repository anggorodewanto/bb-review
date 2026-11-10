package server

import (
	"io"
	"net/http"

	"github.com/anggorodewanto/bb-review/internal/bitbucket"
)

// maxFileSize is the largest file the /api/file endpoint returns (2 MB).
const maxFileSize = 2 << 20

// handleFile streams raw file content at a commit, for expanding diff context.
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	repo := bitbucket.Repo{
		Workspace: r.PathValue("ws"),
		Repo:      r.PathValue("repo"),
	}
	commit := r.PathValue("commit")
	filePath := r.PathValue("path")
	if repo.Workspace == "" || repo.Repo == "" || commit == "" || filePath == "" {
		writeError(w, http.StatusBadRequest, "workspace, repository, commit and path are required")
		return
	}

	body, err := s.bb.File(r.Context(), repo, commit, filePath)
	if err != nil {
		s.writeUpstream(w, "file", err)
		return
	}
	defer body.Close()

	data, err := io.ReadAll(io.LimitReader(body, maxFileSize+1))
	if err != nil {
		s.writeUpstream(w, "file read", err)
		return
	}
	if int64(len(data)) > maxFileSize {
		writeError(w, http.StatusRequestEntityTooLarge, "file is larger than 2 MB")
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
