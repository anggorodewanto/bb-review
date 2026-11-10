package server

import (
	"bytes"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

// handleSPA serves the embedded SPA: real files as-is, everything else index.html.
func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if !isSafeMethod(r.Method) {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.hasIndex {
		s.serveNotBuilt(w)
		return
	}

	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name != "" && name != "index.html" && fs.ValidPath(name) {
		if _, err := fs.Stat(s.assets, name); err == nil {
			cache := "no-cache"
			if strings.HasPrefix(name, "assets/") {
				cache = "public, max-age=31536000, immutable"
			}
			s.serveAsset(w, r, name, cache)
			return
		}
	}
	s.serveAsset(w, r, "index.html", "no-cache")
}

// serveAsset writes one embedded file with an explicit cache policy.
func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request, name, cacheControl string) {
	data, err := fs.ReadFile(s.assets, name)
	if err != nil {
		s.log.Error("read embedded asset", "name", name, "err", err.Error())
		writeError(w, http.StatusInternalServerError, "frontend asset unavailable")
		return
	}
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("Content-Type", contentTypeFor(name))
	http.ServeContent(w, r, path.Base(name), time.Time{}, bytes.NewReader(data))
}

// serveNotBuilt answers every SPA route when web/dist has no index.html yet.
func (s *Server) serveNotBuilt(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "bb-review: frontend not built.\n\n"+
		"Run `make build` (or `npm --prefix web install && npm --prefix web run build`) and restart the server.\n")
}

// contentTypeFor picks a content type for an embedded asset.
func contentTypeFor(name string) string {
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		return ct
	}
	switch path.Ext(name) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".woff2":
		return "font/woff2"
	case ".json", ".map":
		return "application/json; charset=utf-8"
	}
	return "application/octet-stream"
}
