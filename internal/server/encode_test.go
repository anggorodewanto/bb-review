package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"
)

// gunzip decodes a gzip response body for assertions.
func gunzip(t *testing.T, body []byte) string {
	t.Helper()
	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer reader.Close()
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	return string(decoded)
}

func diffUpstream(t *testing.T, body string) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests/7/diff", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(body))
	})
	return mux
}

func TestDiffIsGzippedWhenAccepted(t *testing.T) {
	body := strings.Repeat("diff --git a/x b/x\n+added line\n", 100)
	env := newTestEnv(t, diffUpstream(t, body))

	rec := env.do(t, http.MethodGet, "/api/prs/ws/repo-a/7/diff", nil, func(r *http.Request) {
		r.Header.Set("Accept-Encoding", "gzip, deflate")
	})
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := rec.Header().Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Fatalf("Vary = %q, want Accept-Encoding", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := gunzip(t, rec.Body.Bytes()); got != body {
		t.Fatalf("decoded body = %q, want the diff", got)
	}
}

func TestDiffIsPlainWithoutAcceptEncoding(t *testing.T) {
	body := strings.Repeat("diff --git a/x b/x\n+added line\n", 100)
	env := newTestEnv(t, diffUpstream(t, body))

	rec := env.do(t, http.MethodGet, "/api/prs/ws/repo-a/7/diff", nil)
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want none", got)
	}
	if got := rec.Body.String(); got != body {
		t.Fatalf("body = %q, want the diff", got)
	}
}

func TestSmallBodiesAreNotGzipped(t *testing.T) {
	const small = "diff --git a/x b/x\n"
	env := newTestEnv(t, diffUpstream(t, small))

	rec := env.do(t, http.MethodGet, "/api/prs/ws/repo-a/7/diff", nil, func(r *http.Request) {
		r.Header.Set("Accept-Encoding", "gzip")
	})
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("small body was compressed (Content-Encoding = %q)", got)
	}
	if got := rec.Body.String(); got != small {
		t.Fatalf("body = %q, want %q", got, small)
	}
}

func TestGzipQZeroIsRespected(t *testing.T) {
	body := strings.Repeat("diff --git a/x b/x\n+added line\n", 100)
	env := newTestEnv(t, diffUpstream(t, body))

	rec := env.do(t, http.MethodGet, "/api/prs/ws/repo-a/7/diff", nil, func(r *http.Request) {
		r.Header.Set("Accept-Encoding", "gzip;q=0")
	})
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q for gzip;q=0", got)
	}
	if got := rec.Body.String(); got != body {
		t.Fatalf("body was not the plain diff")
	}
}

func TestFileIsGzippedWhenAccepted(t *testing.T) {
	body := strings.Repeat("package main\n\nfunc main() {}\n", 100)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/src/{commit}/{path...}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(body))
	})
	env := newTestEnv(t, mux)

	rec := env.do(t, http.MethodGet, "/api/file/ws/repo-a/abc123/main.go", nil, func(r *http.Request) {
		r.Header.Set("Accept-Encoding", "gzip")
	})
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := gunzip(t, rec.Body.Bytes()); got != body {
		t.Fatalf("decoded file body = %q", got)
	}
}
