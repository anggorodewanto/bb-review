package server

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/anggorodewanto/bb-review/internal/bitbucket"
)

// resolveBody is the GET /api/resolve response.
type resolveBody struct {
	Workspace string `json:"workspace"`
	Repo      string `json:"repo"`
	ID        int    `json:"id"`
}

// errNotAPRURL is returned for anything that is not a Bitbucket pull request URL.
var errNotAPRURL = errors.New(
	"url must look like https://bitbucket.org/{workspace}/{repo}/pull-requests/{id}",
)

// handleResolve turns a pasted Bitbucket pull request URL into workspace/repo/id.
// It is pure parsing: no upstream call is made.
func (s *Server) handleResolve(w http.ResponseWriter, r *http.Request) {
	repo, id, err := parsePRURL(r.URL.Query().Get("url"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resolveBody{Workspace: repo.Workspace, Repo: repo.Repo, ID: id})
}

// parsePRURL accepts
// https://bitbucket.org/{ws}/{repo}/pull-requests/{id}[/anything][?query][#frag].
func parsePRURL(raw string) (bitbucket.Repo, int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return bitbucket.Repo{}, 0, errors.New("url is required")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return bitbucket.Repo{}, 0, errNotAPRURL
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return bitbucket.Repo{}, 0, errNotAPRURL
	}
	if host := strings.ToLower(parsed.Hostname()); host != "bitbucket.org" && host != "www.bitbucket.org" {
		return bitbucket.Repo{}, 0, errNotAPRURL
	}

	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) < 4 {
		return bitbucket.Repo{}, 0, errNotAPRURL
	}
	if segments[0] == "" || segments[1] == "" || segments[2] != "pull-requests" {
		return bitbucket.Repo{}, 0, errNotAPRURL
	}
	id, err := strconv.Atoi(segments[3])
	if err != nil || id <= 0 {
		return bitbucket.Repo{}, 0, errNotAPRURL
	}
	return bitbucket.Repo{Workspace: segments[0], Repo: segments[1]}, id, nil
}
