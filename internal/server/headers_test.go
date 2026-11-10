package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anggorodewanto/bb-review/internal/bitbucket"
	"github.com/anggorodewanto/bb-review/internal/config"
)

const wantCSP = "default-src 'self'; script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; img-src 'self' https: data:; " +
	"connect-src 'self'; object-src 'none'; base-uri 'none'; " +
	"frame-ancestors 'none'; form-action 'none'"

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	tests := []struct {
		name   string
		method string
		target string
		mutate func(*http.Request)
		want   int
	}{
		{name: "api success", method: http.MethodGet, target: "/api/me", want: 200},
		{name: "spa", method: http.MethodGet, target: "/pull-requests/12", want: 200},
		{
			name: "csrf rejection", method: http.MethodPost, target: "/api/prs/ws/repo-a/7/approve",
			mutate: func(r *http.Request) { r.Header.Del("X-BBR") }, want: 403,
		},
		{
			name: "host rejection", method: http.MethodGet, target: "/api/me",
			mutate: func(r *http.Request) { r.Host = "evil.example.com" }, want: 421,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, prDetailUpstream(t))
			var mutators []func(*http.Request)
			if tc.mutate != nil {
				mutators = append(mutators, tc.mutate)
			}
			rec := env.do(t, tc.method, tc.target, nil, mutators...)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if got := rec.Header().Get("Content-Security-Policy"); got != wantCSP {
				t.Fatalf("Content-Security-Policy = %q, want %q", got, wantCSP)
			}
			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("X-Content-Type-Options = %q", got)
			}
			if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
				t.Fatalf("Referrer-Policy = %q", got)
			}
		})
	}
}

func TestFileAndDiffContentHeaders(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/src/{commit}/{path...}", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("package main\n"))
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests/7/diff", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("diff --git a/x b/x\n"))
	})
	env := newTestEnv(t, mux)

	for _, target := range []string{"/api/file/ws/repo-a/abc/main.go", "/api/prs/ws/repo-a/7/diff"} {
		rec := env.do(t, http.MethodGet, target, nil)
		if rec.Code != 200 {
			t.Fatalf("%s status = %d, body %s", target, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
			t.Fatalf("%s Content-Type = %q", target, got)
		}
		if got := rec.Header().Get("Content-Disposition"); got != "inline" {
			t.Fatalf("%s Content-Disposition = %q", target, got)
		}
		if got := rec.Header().Get("Content-Security-Policy"); got != wantCSP {
			t.Fatalf("%s CSP = %q", target, got)
		}
	}
}

func listUpstream(t *testing.T, limited bool) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/repositories/ws", func(w http.ResponseWriter, r *http.Request) {
		upstreamJSON(t, w, 200, map[string]any{"values": []any{}})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		upstreamJSON(t, w, 200, map[string]any{"values": []any{map[string]any{
			"id": 1, "title": "kept", "state": "OPEN", "updated_on": "2026-01-02T09:00:00+00:00",
			"source": map[string]any{"commit": map[string]any{"hash": ""}},
		}}})
	})
	if limited {
		mux.HandleFunc("GET /2.0/repositories/ws/repo-b/pullrequests", func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(30 * time.Millisecond)
			w.Header().Set("Retry-After", "5")
			upstreamJSON(t, w, 429, map[string]any{"error": map[string]any{"message": "rate limited"}})
		})
		return mux
	}
	mux.HandleFunc("GET /2.0/repositories/ws/repo-b/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		upstreamJSON(t, w, 500, map[string]any{"error": map[string]any{"message": "repo b exploded"}})
	})
	return mux
}

func twoReposConfig() *config.Config {
	return &config.Config{
		Email: "me@example.com", Token: "tok", Workspace: "ws",
		Repos: []string{"repo-a", "repo-b"}, Listen: testHost,
	}
}

func TestPRListHeaders(t *testing.T) {
	env := newTestEnvWithConfig(t, listUpstream(t, false), twoReposConfig())
	rec := env.do(t, http.MethodGet, "/api/prs?role=reviewer", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-BBR-Partial"); got != "repo-b" {
		t.Fatalf("X-BBR-Partial = %q, want repo-b", got)
	}
	if got := rec.Header().Get("X-BBR-Rate-Limited"); got != "" {
		t.Fatalf("X-BBR-Rate-Limited = %q, want empty", got)
	}

	fetchedAt := rec.Header().Get("X-BBR-Fetched-At")
	if fetchedAt == "" {
		t.Fatal("X-BBR-Fetched-At is missing")
	}
	parsed, err := time.Parse(time.RFC3339, fetchedAt)
	if err != nil {
		t.Fatalf("X-BBR-Fetched-At %q is not RFC3339: %v", fetchedAt, err)
	}
	if age := time.Since(parsed); age < 0 || age > time.Minute {
		t.Fatalf("X-BBR-Fetched-At is %v old", age)
	}
}

func TestPRListRateLimitedHeader(t *testing.T) {
	env := newTestEnvWithConfig(t, listUpstream(t, true), twoReposConfig())
	rec := env.do(t, http.MethodGet, "/api/prs?role=reviewer", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-BBR-Rate-Limited"); got != "1" {
		t.Fatalf("X-BBR-Rate-Limited = %q, want 1", got)
	}
	prs := decodeBody[[]bitbucket.PRSummary](t, rec)
	if len(prs) != 1 {
		t.Fatalf("partial prs = %d, want 1", len(prs))
	}
	if got := rec.Header().Get("X-BBR-Partial"); got != "" {
		t.Fatalf("X-BBR-Partial = %q, want empty for a rate-limited repo", got)
	}
}

func TestPRListAuthorRoleUsesWorkspaceEndpoint(t *testing.T) {
	var workspaceCalls, repoCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/workspaces/ws/pullrequests/{user}", func(w http.ResponseWriter, r *http.Request) {
		workspaceCalls++
		upstreamJSON(t, w, 200, map[string]any{"values": []any{map[string]any{
			"id": 3, "title": "authored", "state": "OPEN", "updated_on": "2026-01-02T09:00:00+00:00",
			"destination": map[string]any{"repository": map[string]any{"full_name": "ws/authored-repo"}},
			"source":      map[string]any{"commit": map[string]any{"hash": ""}},
		}}})
	})
	mux.HandleFunc("GET /2.0/repositories/", func(w http.ResponseWriter, r *http.Request) {
		repoCalls++
		upstreamJSON(t, w, 500, map[string]any{"error": map[string]any{"message": "no fan-out expected"}})
	})
	env := newTestEnv(t, mux)

	rec := env.do(t, http.MethodGet, "/api/prs?role=author", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if workspaceCalls != 1 {
		t.Fatalf("workspace endpoint calls = %d, want 1", workspaceCalls)
	}
	if repoCalls != 0 {
		t.Fatalf("per-repo calls = %d, want 0 for the author role", repoCalls)
	}
	prs := decodeBody[[]bitbucket.PRSummary](t, rec)
	if len(prs) != 1 || prs[0].Repo != "authored-repo" || prs[0].Workspace != "ws" {
		t.Fatalf("prs = %+v", prs)
	}
	if got := rec.Header().Get("X-BBR-Partial"); got != "" {
		t.Fatalf("X-BBR-Partial = %q", got)
	}
	if strings.TrimSpace(rec.Header().Get("X-BBR-Fetched-At")) == "" {
		t.Fatal("X-BBR-Fetched-At is missing")
	}
}
