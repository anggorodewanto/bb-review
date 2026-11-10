package server

import (
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/anggorodewanto/bb-review/internal/bitbucket"
)

func TestStatsEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/repositories/ws", func(w http.ResponseWriter, r *http.Request) {
		upstreamJSON(t, w, 200, map[string]any{"values": []any{}})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		upstreamJSON(t, w, 200, map[string]any{"values": []any{prMap(1, "x")}})
	})
	env := newTestEnv(t, mux)
	env.scan(t)

	rec := env.do(t, http.MethodGet, "/api/stats", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	body := decodeBody[statsResponse](t, rec)
	if body.UpstreamLastHour <= 0 {
		t.Fatalf("upstream_last_hour = %d, want the requests of the scan", body.UpstreamLastHour)
	}
	if body.Scanner.ReposTracked != 1 {
		t.Fatalf("repos_tracked = %d, want 1", body.Scanner.ReposTracked)
	}
	if body.Scanner.HotRepos != 1 {
		t.Fatalf("hot_repos = %d, want 1", body.Scanner.HotRepos)
	}
	if body.Scanner.LastTickAt == "" || body.Scanner.LastFullSweepAt == "" {
		t.Fatalf("scanner timestamps = %+v", body.Scanner)
	}
	if body.Scanner.RateLimited {
		t.Fatal("rate_limited = true after a clean scan")
	}
}

func TestPRListFillsBuildStatesFromCache(t *testing.T) {
	var statusCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/repositories/ws", func(w http.ResponseWriter, r *http.Request) {
		upstreamJSON(t, w, 200, map[string]any{"values": []any{}})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		pr := prMap(1, "with builds")
		pr["source"] = map[string]any{
			"branch": map[string]any{"name": "feat"},
			"commit": map[string]any{"hash": "sha1"},
		}
		upstreamJSON(t, w, 200, map[string]any{"values": []any{pr}})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/commit/sha1/statuses", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&statusCalls, 1)
		upstreamJSON(t, w, 200, map[string]any{
			"values": []any{map[string]any{"key": "ci", "name": "CI", "state": "SUCCESSFUL"}},
		})
	})
	env := newTestEnv(t, mux)
	env.scan(t)

	rec := env.do(t, http.MethodGet, "/api/prs?role=reviewer", nil)
	prs := decodeBody[[]bitbucket.PRSummary](t, rec)
	if len(prs) != 1 || prs[0].SourceCommit != "sha1" {
		t.Fatalf("prs = %+v, want one row with source_commit sha1", prs)
	}
	if prs[0].BuildState != bitbucket.BuildSuccessful {
		t.Fatalf("build_state = %q, want SUCCESSFUL", prs[0].BuildState)
	}
	if got := atomic.LoadInt32(&statusCalls); got != 1 {
		t.Fatalf("status calls = %d, want 1", got)
	}

	// A terminal state is cached until evicted: the next poll costs nothing.
	rec = env.do(t, http.MethodGet, "/api/prs?role=reviewer", nil)
	prs = decodeBody[[]bitbucket.PRSummary](t, rec)
	if len(prs) != 1 || prs[0].BuildState != bitbucket.BuildSuccessful {
		t.Fatalf("second poll prs = %+v", prs)
	}
	if got := atomic.LoadInt32(&statusCalls); got != 1 {
		t.Fatalf("status calls = %d after a cached poll, want 1", got)
	}
}
