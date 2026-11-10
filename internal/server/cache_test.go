package server

import (
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/anggorodewanto/bb-review/internal/bitbucket"
)

func TestPRDetailIsCachedAndBustedByWrite(t *testing.T) {
	var detailCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests/7", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&detailCalls, 1)
		upstreamJSON(t, w, 200, map[string]any{
			"id": 7, "title": "cached", "state": "OPEN",
			"participants": []any{map[string]any{"user": map[string]any{"uuid": "uuid-me"}, "approved": true}},
			"source":       map[string]any{"commit": map[string]any{"hash": "src111"}},
			"destination":  map[string]any{"commit": map[string]any{"hash": "dst222"}},
		})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests/7/merge-base", func(w http.ResponseWriter, r *http.Request) {
		upstreamJSON(t, w, 200, map[string]any{"hash": "base000"})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/commit/src111/statuses", func(w http.ResponseWriter, r *http.Request) {
		upstreamJSON(t, w, 200, map[string]any{
			"values": []any{map[string]any{"key": "ci", "name": "CI", "state": "SUCCESSFUL"}},
		})
	})
	mux.HandleFunc("POST /2.0/repositories/ws/repo-a/pullrequests/7/approve", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		upstreamJSON(t, w, 200, map[string]any{"values": []any{}})
	})
	env := newTestEnv(t, mux)

	for i := 0; i < 2; i++ {
		rec := env.do(t, http.MethodGet, "/api/prs/ws/repo-a/7", nil)
		if rec.Code != 200 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
		}
		if detail := decodeBody[bitbucket.PRDetail](t, rec); detail.Title != "cached" {
			t.Fatalf("detail = %+v", detail)
		}
	}
	if got := atomic.LoadInt32(&detailCalls); got != 1 {
		t.Fatalf("detail fetched %d times for two reads, want 1 (cached)", got)
	}

	// A write busts the cache; the write path refetches the detail once and the
	// next GET is served from the fresh cache entry.
	rec := env.do(t, http.MethodPost, "/api/prs/ws/repo-a/7/approve", nil)
	if rec.Code != 200 {
		t.Fatalf("approve status = %d, body %s", rec.Code, rec.Body.String())
	}
	if got := atomic.LoadInt32(&detailCalls); got != 2 {
		t.Fatalf("detail fetched %d times after the write, want 2 (busted)", got)
	}
	rec = env.do(t, http.MethodGet, "/api/prs/ws/repo-a/7", nil)
	if rec.Code != 200 {
		t.Fatalf("status after write = %d", rec.Code)
	}
	if got := atomic.LoadInt32(&detailCalls); got != 2 {
		t.Fatalf("detail fetched %d times after the write, want 2 (fresh cache entry)", got)
	}
}
