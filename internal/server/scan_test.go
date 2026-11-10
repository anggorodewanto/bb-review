package server

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anggorodewanto/bb-review/internal/bitbucket"
	"github.com/anggorodewanto/bb-review/internal/config"
)

// prMap is the minimal pull request payload the fakes need.
func prMap(id int, title string) map[string]any {
	return map[string]any{
		"id": id, "title": title, "state": "OPEN",
		"updated_on": "2026-01-02T09:00:00+00:00",
		"author":     map[string]any{"uuid": "uuid-author", "display_name": "Ann"},
		"source":     map[string]any{"commit": map[string]any{"hash": ""}},
	}
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestPRListServedFromScannerState(t *testing.T) {
	var listCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/repositories/ws", func(w http.ResponseWriter, r *http.Request) {
		upstreamJSON(t, w, 200, map[string]any{"values": []any{}})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&listCalls, 1)
		upstreamJSON(t, w, 200, map[string]any{"values": []any{prMap(1, "cached")}})
	})
	env := newTestEnv(t, mux)
	env.scan(t)

	before := atomic.LoadInt32(&listCalls)
	for i := 0; i < 3; i++ {
		rec := env.do(t, http.MethodGet, "/api/prs?role=reviewer", nil)
		if rec.Code != 200 {
			t.Fatalf("status = %d", rec.Code)
		}
		prs := decodeBody[[]bitbucket.PRSummary](t, rec)
		if len(prs) != 1 || prs[0].Title != "cached" {
			t.Fatalf("prs = %+v", prs)
		}
	}
	if after := atomic.LoadInt32(&listCalls); after != before {
		t.Fatalf("serving the list made %d upstream calls, want 0", after-before)
	}
}

func TestPRListScanningProgressHeader(t *testing.T) {
	block := make(chan struct{})
	var once atomic.Bool

	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	recent := func(hoursAgo int) string {
		return time.Now().Add(-time.Duration(hoursAgo) * time.Hour).UTC().Format(time.RFC3339)
	}
	mux.HandleFunc("GET /2.0/repositories/ws", func(w http.ResponseWriter, r *http.Request) {
		upstreamJSON(t, w, 200, map[string]any{"values": []any{
			map[string]any{"slug": "repo-a", "updated_on": recent(1)},
			map[string]any{"slug": "repo-b", "updated_on": recent(2)},
			map[string]any{"slug": "repo-c", "updated_on": recent(3)},
		}})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		upstreamJSON(t, w, 200, map[string]any{"values": []any{prMap(1, "a")}})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-b/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		upstreamJSON(t, w, 200, map[string]any{"values": []any{prMap(2, "b")}})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-c/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		<-block
		upstreamJSON(t, w, 200, map[string]any{"values": []any{prMap(3, "c")}})
	})

	cfg := &config.Config{
		Email: "me@example.com", Token: "tok", Workspace: "ws",
		Repos: []string{"repo-a"}, Listen: testHost,
	}
	env := newTestEnvWithConfig(t, mux, cfg)

	tickDone := make(chan error, 1)
	go func() { tickDone <- env.scanner.Tick(context.Background()) }()

	waitFor(t, 3*time.Second, "two repos to finish", func() bool {
		snapshot := env.scanner.Snapshot()
		return snapshot.Scanning && snapshot.Done == 2
	})

	rec := env.do(t, http.MethodGet, "/api/prs?role=reviewer", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-BBR-Scanning"); got != "2/3" {
		t.Fatalf("X-BBR-Scanning = %q, want 2/3", got)
	}
	prs := decodeBody[[]bitbucket.PRSummary](t, rec)
	if len(prs) != 2 {
		t.Fatalf("partial results = %d, want 2", len(prs))
	}

	if !once.Swap(true) {
		close(block)
	}
	if err := <-tickDone; err != nil {
		t.Fatalf("Tick: %v", err)
	}

	rec = env.do(t, http.MethodGet, "/api/prs?role=reviewer", nil)
	if got := rec.Header().Get("X-BBR-Scanning"); got != "" {
		t.Fatalf("X-BBR-Scanning = %q after the sweep, want empty", got)
	}
	if prs := decodeBody[[]bitbucket.PRSummary](t, rec); len(prs) != 3 {
		t.Fatalf("prs = %d, want 3", len(prs))
	}
}

func TestScanEndpointDebounces(t *testing.T) {
	var listCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/repositories/ws", func(w http.ResponseWriter, r *http.Request) {
		upstreamJSON(t, w, 200, map[string]any{"values": []any{}})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&listCalls, 1)
		upstreamJSON(t, w, 200, map[string]any{"values": []any{prMap(1, "x")}})
	})
	env := newTestEnv(t, mux)

	first := env.do(t, http.MethodPost, "/api/scan", nil)
	if first.Code != 200 {
		t.Fatalf("status = %d, body %s", first.Code, first.Body.String())
	}
	if body := decodeBody[scanResponse](t, first); !body.Started {
		t.Fatal("first scan was not started")
	}

	second := env.do(t, http.MethodPost, "/api/scan", nil)
	if body := decodeBody[scanResponse](t, second); body.Started {
		t.Fatal("second scan should be debounced")
	}

	waitFor(t, 3*time.Second, "the scan to finish", func() bool {
		return len(env.scanner.Snapshot().PRs) == 1 && !env.scanner.Busy()
	})
	if got := atomic.LoadInt32(&listCalls); got != 1 {
		t.Fatalf("repo queried %d times, want 1", got)
	}
}

func TestScanEndpointRequiresXBBR(t *testing.T) {
	env := newTestEnv(t, nil)
	rec := env.do(t, http.MethodPost, "/api/scan", nil, func(r *http.Request) { r.Header.Del("X-BBR") })
	if rec.Code != 403 {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestWriteActionRefreshesScannerEntry(t *testing.T) {
	var (
		mu    sync.Mutex
		title = "before"
		pulls int32
	)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/repositories/ws", func(w http.ResponseWriter, r *http.Request) {
		upstreamJSON(t, w, 200, map[string]any{"values": []any{}})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pulls, 1)
		mu.Lock()
		current := title
		mu.Unlock()
		upstreamJSON(t, w, 200, map[string]any{"values": []any{prMap(1, current)}})
	})
	mux.HandleFunc("POST /2.0/repositories/ws/repo-a/pullrequests/1/comments", func(w http.ResponseWriter, r *http.Request) {
		upstreamJSON(t, w, 201, map[string]any{"id": 5, "content": map[string]any{"raw": "hi", "html": "<p>hi</p>"}})
	})
	env := newTestEnv(t, mux)
	env.scan(t)

	rec := env.do(t, http.MethodGet, "/api/prs?role=reviewer", nil)
	if prs := decodeBody[[]bitbucket.PRSummary](t, rec); len(prs) != 1 || prs[0].Title != "before" {
		t.Fatalf("prs before = %+v", prs)
	}

	mu.Lock()
	title = "after"
	mu.Unlock()
	before := atomic.LoadInt32(&pulls)

	post := env.do(t, http.MethodPost, "/api/prs/ws/repo-a/1/comments", map[string]any{"raw": "hi"})
	if post.Code != 200 {
		t.Fatalf("comment status = %d, body %s", post.Code, post.Body.String())
	}

	if got := atomic.LoadInt32(&pulls) - before; got != 1 {
		t.Fatalf("write refresh made %d repo queries, want 1", got)
	}
	rec = env.do(t, http.MethodGet, "/api/prs?role=reviewer", nil)
	if prs := decodeBody[[]bitbucket.PRSummary](t, rec); len(prs) != 1 || prs[0].Title != "after" {
		t.Fatalf("prs after = %+v", prs)
	}
}
