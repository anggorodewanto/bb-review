// Package server implements the HTTP API described in docs/API.md, the security
// middleware and the embedded SPA handler.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/anggorodewanto/bb-review/internal/bitbucket"
	"github.com/anggorodewanto/bb-review/internal/config"
	"github.com/anggorodewanto/bb-review/internal/scanner"
	"github.com/anggorodewanto/bb-review/internal/store"
)

// Options wire a Server together. Config, Client and Store are required.
type Options struct {
	Config *config.Config
	Client *bitbucket.Client
	Store  *store.Store
	Logger *slog.Logger
	// Assets is the embedded web/dist file system. May be nil or empty, in which
	// case the SPA handler serves a "frontend not built" page.
	Assets fs.FS
	// Scanner serves the reviewer PR list from incremental scan state.
	Scanner *scanner.Scanner
}

// Server serves the bb-review API and SPA.
type Server struct {
	cfg      *config.Config
	bb       *bitbucket.Client
	store    *store.Store
	log      *slog.Logger
	assets   fs.FS
	hasIndex bool
	scanner  *scanner.Scanner
}

// New returns a Server.
func New(opts Options) *Server {
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	s := &Server{
		cfg:     opts.Config,
		bb:      opts.Client,
		store:   opts.Store,
		log:     logger,
		assets:  opts.Assets,
		scanner: opts.Scanner,
	}
	if s.assets != nil {
		if _, err := fs.Stat(s.assets, "index.html"); err == nil {
			s.hasIndex = true
		}
	}
	return s
}

// Handler returns the fully wired HTTP handler: logging, security, API, SPA.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/me", s.handleMe)
	mux.HandleFunc("GET /api/resolve", s.handleResolve)
	mux.HandleFunc("GET /api/prs", s.handlePRList)
	mux.HandleFunc("POST /api/scan", s.handleScan)
	mux.HandleFunc("GET /api/prs/{ws}/{repo}/{id}", s.handlePRDetail)
	mux.HandleFunc("GET /api/prs/{ws}/{repo}/{id}/diff", s.handleDiff)
	mux.HandleFunc("GET /api/prs/{ws}/{repo}/{id}/comments", s.handleComments)
	mux.HandleFunc("POST /api/prs/{ws}/{repo}/{id}/comments", s.handleCreateComment)
	mux.HandleFunc("GET /api/prs/{ws}/{repo}/{id}/drafts", s.handleDrafts)
	mux.HandleFunc("POST /api/prs/{ws}/{repo}/{id}/drafts", s.handleCreateDraft)
	mux.HandleFunc("POST /api/prs/{ws}/{repo}/{id}/drafts/submit", s.handleSubmitDrafts)
	mux.HandleFunc("PUT /api/prs/{ws}/{repo}/{id}/drafts/{draftID}", s.handleUpdateDraft)
	mux.HandleFunc("DELETE /api/prs/{ws}/{repo}/{id}/drafts/{draftID}", s.handleDeleteDraft)
	mux.HandleFunc("POST /api/prs/{ws}/{repo}/{id}/approve", s.handleReviewAction(reviewApprove, true))
	mux.HandleFunc("DELETE /api/prs/{ws}/{repo}/{id}/approve", s.handleReviewAction(reviewApprove, false))
	mux.HandleFunc("POST /api/prs/{ws}/{repo}/{id}/request-changes", s.handleReviewAction(reviewRequestChanges, true))
	mux.HandleFunc("DELETE /api/prs/{ws}/{repo}/{id}/request-changes", s.handleReviewAction(reviewRequestChanges, false))
	mux.HandleFunc("GET /api/prs/{ws}/{repo}/{id}/viewed", s.handleViewed)
	mux.HandleFunc("PUT /api/prs/{ws}/{repo}/{id}/viewed", s.handleSetViewed)
	mux.HandleFunc("GET /api/file/{ws}/{repo}/{commit}/{path...}", s.handleFile)
	mux.HandleFunc("/api/", s.handleAPINotFound)
	mux.HandleFunc("/", s.handleSPA)

	return s.withLogging(s.withSecurity(mux))
}

// --- middleware -------------------------------------------------------------

// statusRecorder captures the status code for the access log.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wrote {
		r.status = code
		r.wrote = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.status = http.StatusOK
		r.wrote = true
	}
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// withLogging logs one JSON line per request.
func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.log.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
		)
	})
}

// contentSecurityPolicy locks the SPA down to its own origin.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; img-src 'self' https: data:; " +
	"connect-src 'self'; object-src 'none'; base-uri 'none'; " +
	"frame-ancestors 'none'; form-action 'none'"

// withSecurity sets the hardening headers, then enforces the CSRF (X-BBR) and
// DNS-rebinding (Host) guards. CORS preflights are never answered: OPTIONS is not
// a safe method and browsers cannot add X-BBR to a preflight, so it falls
// through to 403.
func (s *Server) withSecurity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("Content-Security-Policy", contentSecurityPolicy)
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Referrer-Policy", "no-referrer")

		if !s.hostAllowed(r.Host) {
			writeError(w, http.StatusMisdirectedRequest, "host not allowed")
			return
		}
		if !isSafeMethod(r.Method) && r.Header.Get("X-BBR") != "1" {
			writeError(w, http.StatusForbidden, "missing X-BBR header")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isSafeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

// hostAllowed checks the Host header against loopback names plus config.allowed_hosts.
func (s *Server) hostAllowed(host string) bool {
	name := hostName(host)
	if name == "" {
		return false
	}
	switch name {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	for _, allowed := range s.cfg.AllowedHosts {
		if strings.EqualFold(hostName(allowed), name) {
			return true
		}
	}
	return false
}

// hostName strips the port from a Host header value and lowercases it.
func hostName(host string) string {
	h := strings.TrimSpace(host)
	if strings.HasPrefix(h, "[") {
		if end := strings.Index(h, "]"); end >= 0 {
			h = h[1:end]
		}
	} else if idx := strings.LastIndex(h, ":"); idx >= 0 {
		h = h[:idx]
	}
	return strings.TrimSuffix(strings.ToLower(h), ".")
}

// --- helpers ----------------------------------------------------------------

// writeJSON writes v as JSON. API responses are never cached by the browser.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		return
	}
}

// writeError writes a local (non-upstream) error. Per docs/API.md the status
// field carries the upstream status, or 0 when the failure is ours.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorBody{Error: message, Status: 0})
}

type errorBody struct {
	Error  string `json:"error"`
	Status int    `json:"status"`
}

// writeUpstream maps a bitbucket error onto the documented error shape.
func (s *Server) writeUpstream(w http.ResponseWriter, context string, err error) {
	httpStatus := http.StatusBadGateway
	upstreamStatus := 0
	message := err.Error()

	var apiErr *bitbucket.APIError
	if errors.As(err, &apiErr) {
		message = apiErr.Message
		upstreamStatus = apiErr.Status
		if apiErr.Status >= 400 && apiErr.Status <= 599 {
			httpStatus = apiErr.Status
		}
	}
	s.log.Warn("upstream error", "context", context, "status", httpStatus, "err", message)
	writeJSON(w, httpStatus, errorBody{Error: message, Status: upstreamStatus})
}

// writeNotFound is the standard 404.
func writeNotFound(w http.ResponseWriter, message string) {
	writeError(w, http.StatusNotFound, message)
}

// decodeJSON decodes a JSON request body, rejecting oversized input.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// prFromPath resolves {ws}, {repo} and {id} into a repo and pull request id.
func (s *Server) prFromPath(r *http.Request) (bitbucket.Repo, int, error) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		return bitbucket.Repo{}, 0, fmt.Errorf("invalid pull request id %q", r.PathValue("id"))
	}
	repo := bitbucket.Repo{
		Workspace: r.PathValue("ws"),
		Repo:      r.PathValue("repo"),
	}
	if repo.Workspace == "" || repo.Repo == "" {
		return bitbucket.Repo{}, 0, errors.New("missing workspace or repository")
	}
	return repo, id, nil
}

// handleAPINotFound answers unknown /api paths.
func (s *Server) handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	writeNotFound(w, "no such endpoint: "+r.Method+" "+r.URL.Path)
}
