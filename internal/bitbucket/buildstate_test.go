package bitbucket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// clockFunc is a controllable clock for cache and counter tests.
type clockFunc struct {
	at time.Time
}

func (c *clockFunc) Now() time.Time { return c.at }

func newClock() *clockFunc {
	return &clockFunc{at: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
}

func TestRequestCounterRollingWindow(t *testing.T) {
	clock := newClock()
	counter := newRequestCounter(clock.Now)

	for i := 0; i < 5; i++ {
		counter.record()
	}
	if got := counter.lastHour(); got != 5 {
		t.Fatalf("lastHour = %d, want 5", got)
	}

	// A request 59 minutes later still shares the window.
	clock.at = clock.at.Add(59 * time.Minute)
	counter.record()
	if got := counter.lastHour(); got != 6 {
		t.Fatalf("lastHour = %d, want 6", got)
	}

	// One minute later the first bucket falls out of the window.
	clock.at = clock.at.Add(time.Minute)
	if got := counter.lastHour(); got != 1 {
		t.Fatalf("lastHour = %d, want 1 after the window moved", got)
	}

	// Everything ages out after an hour of silence.
	clock.at = clock.at.Add(time.Hour)
	if got := counter.lastHour(); got != 0 {
		t.Fatalf("lastHour = %d, want 0", got)
	}
}

func TestClientCountsEveryUpstreamRequest(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/repositories/ws", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 200, map[string]any{"values": []any{}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Options{BaseURL: srv.URL, Email: "me@example.com", Token: "tok", Logger: discardLogger()})
	// Me is cached, so the extra calls must not add to the counter.
	for i := 0; i < 3; i++ {
		if _, err := client.Me(context.Background()); err != nil {
			t.Fatalf("Me: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := client.DiscoverActiveRepos(context.Background(), "ws", 14); err != nil {
			t.Fatalf("DiscoverActiveRepos: %v", err)
		}
	}
	if got := client.RequestsLastHour(); got != 3 {
		t.Fatalf("RequestsLastHour = %d, want 3", got)
	}
}

func TestBuildStateCacheTerminalAndTTL(t *testing.T) {
	clock := newClock()
	cache := newBuildStateCache(clock.Now)

	// Terminal states never expire.
	cache.put("ws/repo@aaa", BuildSuccessful)
	clock.at = clock.at.Add(10 * time.Hour)
	state, fresh, found := cache.get("ws/repo@aaa")
	if !found || !fresh || state != BuildSuccessful {
		t.Fatalf("terminal entry = %q fresh=%v found=%v", state, fresh, found)
	}

	// INPROGRESS is fresh for 2 minutes, then re-fetched but still returned as
	// the last known value.
	cache.put("ws/repo@bbb", BuildInProgress)
	clock.at = clock.at.Add(90 * time.Second)
	if _, fresh, found := cache.get("ws/repo@bbb"); !found || !fresh {
		t.Fatalf("INPROGRESS at 90s: fresh=%v found=%v", fresh, found)
	}
	clock.at = clock.at.Add(31 * time.Second)
	state, fresh, found = cache.get("ws/repo@bbb")
	if !found || fresh || state != BuildInProgress {
		t.Fatalf("INPROGRESS at 121s = %q fresh=%v found=%v", state, fresh, found)
	}

	// NONE expires the same way.
	cache.put("ws/repo@ccc", BuildNone)
	clock.at = clock.at.Add(2 * time.Minute)
	if _, fresh, found := cache.get("ws/repo@ccc"); !found || fresh {
		t.Fatalf("NONE at 2m: fresh=%v found=%v", fresh, found)
	}

	// Unknown keys miss.
	if _, fresh, found := cache.get("ws/repo@zzz"); found || fresh {
		t.Fatalf("unknown key: fresh=%v found=%v", fresh, found)
	}
}

func TestBuildStateCacheEvictsAtLimit(t *testing.T) {
	cache := newBuildStateCache(nil)
	for i := 0; i < buildStateCacheLimit; i++ {
		key := buildStateKey(Repo{Workspace: "ws", Repo: "repo"}, strconv.Itoa(i))
		cache.put(key, BuildSuccessful)
	}
	if got := len(cache.entries); got != buildStateCacheLimit {
		t.Fatalf("entries = %d, want %d", got, buildStateCacheLimit)
	}
	cache.put("ws/repo@new", BuildSuccessful)
	if got := len(cache.entries); got != buildStateCacheLimit {
		t.Fatalf("entries after eviction = %d, want %d", got, buildStateCacheLimit)
	}
	if _, _, found := cache.get("ws/repo@new"); !found {
		t.Fatal("the newly added entry was evicted")
	}
}

// slowStatuses serves commit statuses after a delay, counting the calls.
func slowStatuses(t *testing.T, delay time.Duration, calls *int32) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/commit/{hash}/statuses", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		time.Sleep(delay)
		writeJSON(t, w, 200, map[string]any{
			"values": []any{map[string]any{"key": "ci", "name": "CI", "state": "SUCCESSFUL"}},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFillBuildStatesHonoursBudgetAndWarmsCache(t *testing.T) {
	var statusCalls int32
	srv := slowStatuses(t, 250*time.Millisecond, &statusCalls)
	client := New(Options{BaseURL: srv.URL, Email: "me@example.com", Token: "tok", Logger: discardLogger()})

	buildPRs := func() []PRSummary {
		return []PRSummary{
			{Workspace: "ws", Repo: "repo-a", ID: 1, SourceCommit: "sha1", BuildState: BuildNone},
			{Workspace: "ws", Repo: "repo-a", ID: 2, SourceCommit: "sha2", BuildState: BuildNone},
			{Workspace: "ws", Repo: "repo-a", ID: 3, SourceCommit: "sha3", BuildState: BuildNone},
		}
	}

	prs := buildPRs()
	start := time.Now()
	client.FillBuildStates(context.Background(), prs, 50*time.Millisecond)
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("FillBuildStates blocked for %v, want ~50ms", elapsed)
	}
	for _, pr := range prs {
		if pr.BuildState != BuildNone {
			t.Fatalf("pr %d build_state = %q, want NONE before the background fetch lands", pr.ID, pr.BuildState)
		}
	}

	// The background fetches finish and warm the cache, so a later poll is free.
	repo := Repo{Workspace: "ws", Repo: "repo-a"}
	commits := []string{"sha1", "sha2", "sha3"}
	deadline := time.Now().Add(3 * time.Second)
	for {
		ready := 0
		for _, commit := range commits {
			if state, fresh, found := client.buildCache.get(buildStateKey(repo, commit)); found && fresh && state == BuildSuccessful {
				ready++
			}
		}
		if ready == len(commits) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cache warmed %d/%d entries", ready, len(commits))
		}
		time.Sleep(10 * time.Millisecond)
	}

	before := atomic.LoadInt32(&statusCalls)
	refill := buildPRs()
	client.FillBuildStates(context.Background(), refill, time.Second)
	for _, pr := range refill {
		if pr.BuildState != BuildSuccessful {
			t.Fatalf("pr %d build_state = %q, want SUCCESSFUL from the cache", pr.ID, pr.BuildState)
		}
	}
	if after := atomic.LoadInt32(&statusCalls); after != before {
		t.Fatalf("statuses fetched %d extra times for terminal cache hits", after-before)
	}
}

func TestFillBuildStatesKeepsLastKnownValue(t *testing.T) {
	var statusCalls int32
	srv := slowStatuses(t, 250*time.Millisecond, &statusCalls)
	clock := newClock()
	client := New(Options{BaseURL: srv.URL, Email: "me@example.com", Token: "tok", Logger: discardLogger(), Now: clock.Now})

	// An expired INPROGRESS entry: the row must keep it while the refresh is slow.
	client.buildCache.put(buildStateKey(Repo{Workspace: "ws", Repo: "repo-a"}, "sha1"), BuildInProgress)
	clock.at = clock.at.Add(3 * time.Minute)

	prs := []PRSummary{{Workspace: "ws", Repo: "repo-a", ID: 1, SourceCommit: "sha1", BuildState: BuildNone}}
	client.FillBuildStates(context.Background(), prs, 20*time.Millisecond)
	if prs[0].BuildState != BuildInProgress {
		t.Fatalf("build_state = %q, want the last known INPROGRESS", prs[0].BuildState)
	}
}

func TestPRDetailPopulatesBuildStateCache(t *testing.T) {
	var statusCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests/1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 200, prJSON(1, "first", "2026-01-03T09:00:00+00:00"))
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests/1/merge-base", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 200, map[string]any{"hash": "base999"})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/commit/{hash}/statuses", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&statusCalls, 1)
		writeJSON(t, w, 200, map[string]any{
			"values": []any{map[string]any{"key": "ci", "name": "CI", "state": "SUCCESSFUL"}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Options{BaseURL: srv.URL, Email: "me@example.com", Token: "tok", Logger: discardLogger()})
	if _, err := client.PRDetail(context.Background(), Repo{Workspace: "ws", Repo: "repo-a"}, 1); err != nil {
		t.Fatalf("PRDetail: %v", err)
	}
	before := atomic.LoadInt32(&statusCalls)

	prs := []PRSummary{{Workspace: "ws", Repo: "repo-a", ID: 1, SourceCommit: "abc123", BuildState: BuildNone}}
	client.FillBuildStates(context.Background(), prs, DefaultBuildBudget)
	if prs[0].BuildState != BuildSuccessful {
		t.Fatalf("build_state = %q, want SUCCESSFUL from the detail fetch", prs[0].BuildState)
	}
	if after := atomic.LoadInt32(&statusCalls); after != before {
		t.Fatalf("detail cache not reused (%d extra status calls)", after-before)
	}
}
