package bitbucket

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeWorkspace is a small in-memory Bitbucket workspace fake: a paged repo
// listing plus a pull request list per repo.
type fakeWorkspace struct {
	t        *testing.T
	server   *httptest.Server
	repos    []fakeRepo // served in order across pages of reposPageLen
	pageSize int

	mu             sync.Mutex
	discoveryCalls int
	discoveryPage  int
	listCalls      map[string]int
	prTitle        string
	blockList      chan struct{} // when non-nil, pull request lists wait for it
	listStarted    chan struct{}
}

type fakeRepo struct {
	slug      string
	updatedOn time.Time
}

func newFakeWorkspace(t *testing.T, repos []fakeRepo) *fakeWorkspace {
	t.Helper()
	f := &fakeWorkspace{
		t:         t,
		repos:     repos,
		pageSize:  reposPageLen,
		listCalls: make(map[string]int),
		prTitle:   "pr",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(meJSON))
	})
	mux.HandleFunc("GET /2.0/repositories/ws", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "" {
			if got := r.URL.Query().Get("sort"); got != "-updated_on" {
				t.Errorf("discovery sort = %q, want -updated_on", got)
			}
			if got := r.URL.Query().Get("pagelen"); got != "100" {
				t.Errorf("discovery pagelen = %q, want 100", got)
			}
			if got := r.URL.Query().Get("fields"); got != "next,values.slug,values.updated_on" {
				t.Errorf("discovery fields = %q", got)
			}
		}

		f.mu.Lock()
		f.discoveryCalls++
		f.discoveryPage++
		page := f.discoveryPage
		start := (page - 1) * f.pageSize
		f.mu.Unlock()

		values := make([]any, 0, f.pageSize)
		for i := start; i < len(f.repos) && i < start+f.pageSize; i++ {
			values = append(values, map[string]any{
				"slug":       f.repos[i].slug,
				"updated_on": f.repos[i].updatedOn.UTC().Format(time.RFC3339),
			})
		}
		next := ""
		if start+f.pageSize < len(f.repos) {
			next = fmt.Sprintf("%s/2.0/repositories/ws?page=%d", f.server.URL, page+1)
		}
		writeJSON(t, w, 200, map[string]any{"values": values, "next": next})
	})
	mux.HandleFunc("GET /2.0/repositories/{ws}/{repo}/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		repo := r.PathValue("repo")
		f.mu.Lock()
		f.listCalls[repo]++
		title := f.prTitle
		block := f.blockList
		started := f.listStarted
		f.mu.Unlock()

		if block != nil {
			if started != nil {
				select {
				case started <- struct{}{}:
				default:
				}
			}
			<-block
		}

		writeJSON(t, w, 200, map[string]any{
			"values": []any{map[string]any{
				"id": 1, "title": title, "state": "OPEN",
				"updated_on": "2026-01-02T09:00:00+00:00",
				"author":     map[string]any{"uuid": "uuid-author"},
				"source":     map[string]any{"commit": map[string]any{"hash": ""}},
			}},
			"next": "",
		})
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeWorkspace) client(opts Options) *Client {
	opts.BaseURL = f.server.URL
	opts.Email = "me@example.com"
	opts.Token = "tok"
	if opts.Logger == nil {
		opts.Logger = discardLogger()
	}
	return New(opts)
}

func (f *fakeWorkspace) listCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, n := range f.listCalls {
		total += n
	}
	return total
}

func (f *fakeWorkspace) discoveryCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.discoveryCalls
}

func (f *fakeWorkspace) setTitle(title string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prTitle = title
}

func (f *fakeWorkspace) setBlock(ch chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blockList = ch
}

func (f *fakeWorkspace) setStarted(ch chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listStarted = ch
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestDiscoveryStopsAtActiveDaysCutoff(t *testing.T) {
	now := time.Now()
	f := newFakeWorkspace(t, []fakeRepo{
		{slug: "repo-a", updatedOn: now.Add(-24 * time.Hour)},
		{slug: "repo-b", updatedOn: now.Add(-3 * 24 * time.Hour)},
		{slug: "repo-old", updatedOn: now.Add(-30 * 24 * time.Hour)},
		{slug: "repo-ancient", updatedOn: now.Add(-90 * 24 * time.Hour)},
	})
	f.pageSize = 2 // force pagination

	client := f.client(Options{})
	result, err := client.ListPRs(context.Background(), ListOptions{
		Role: "reviewer", State: "OPEN", Workspace: "ws", ActiveDays: 14,
	})
	if err != nil {
		t.Fatalf("ListPRs: %v", err)
	}
	if len(result.Failed) != 0 {
		t.Fatalf("failed = %v", result.Failed)
	}
	if len(result.PRs) != 2 {
		t.Fatalf("prs = %d, want 2 (one per active repo)", len(result.PRs))
	}

	f.mu.Lock()
	calls := make(map[string]int, len(f.listCalls))
	for repo, n := range f.listCalls {
		calls[repo] = n
	}
	pages := f.discoveryPage
	f.mu.Unlock()

	if calls["repo-a"] != 1 || calls["repo-b"] != 1 {
		t.Fatalf("active repos queried = %v", calls)
	}
	if _, ok := calls["repo-old"]; ok {
		t.Fatalf("paging should stop at the cutoff, but repo-old was queried: %v", calls)
	}
	if pages != 2 {
		t.Fatalf("discovery pages fetched = %d, want 2", pages)
	}
}

func TestDiscoveryStopsBeforeWalkingTheWorkspace(t *testing.T) {
	now := time.Now()
	// 500 repos would be 5 pages; the cutoff must stop after the first page.
	repos := make([]fakeRepo, 0, 500)
	for i := 0; i < 500; i++ {
		updated := now.Add(-time.Duration(i) * 24 * time.Hour)
		repos = append(repos, fakeRepo{slug: fmt.Sprintf("repo-%03d", i), updatedOn: updated})
	}
	f := newFakeWorkspace(t, repos)
	client := f.client(Options{})

	result, err := client.ListPRs(context.Background(), ListOptions{
		Role: "reviewer", State: "OPEN", Workspace: "ws", ActiveDays: 14,
	})
	if err != nil {
		t.Fatalf("ListPRs: %v", err)
	}
	if got := f.discoveryCount(); got != 1 {
		t.Fatalf("discovery pages = %d, want 1 (early stop)", got)
	}
	// repos are now-0d .. now-499d; the now-14d one is already past the cutoff.
	if len(result.PRs) != 14 {
		t.Fatalf("prs = %d, want 14 active repos", len(result.PRs))
	}
}

func TestDiscoveryUnionsConfiguredRepos(t *testing.T) {
	now := time.Now()
	f := newFakeWorkspace(t, []fakeRepo{
		{slug: "repo-a", updatedOn: now.Add(-time.Hour)},
		{slug: "repo-b", updatedOn: now.Add(-2 * time.Hour)},
	})

	client := f.client(Options{})
	configured := []Repo{
		{Workspace: "ws", Repo: "repo-b"}, // duplicate of discovery
		{Workspace: "ws", Repo: "extra"},  // config only
		{Workspace: "other", Repo: "far"}, // other workspace, config only
	}
	if _, err := client.ListPRs(context.Background(), ListOptions{
		Role: "reviewer", State: "OPEN", Workspace: "ws", Repos: configured, ActiveDays: 14,
	}); err != nil {
		t.Fatalf("ListPRs: %v", err)
	}

	f.mu.Lock()
	calls := make(map[string]int, len(f.listCalls))
	for repo, n := range f.listCalls {
		calls[repo] = n
	}
	f.mu.Unlock()

	want := map[string]int{"repo-a": 1, "repo-b": 1, "extra": 1, "far": 1}
	for repo, n := range want {
		if calls[repo] != n {
			t.Fatalf("repo %q queried %d times, want %d (calls: %v)", repo, calls[repo], n, calls)
		}
	}
	if len(calls) != len(want) {
		t.Fatalf("queried repos = %v, want %v", calls, want)
	}
}

func TestDiscoveryCachedForTTL(t *testing.T) {
	now := time.Now()
	f := newFakeWorkspace(t, []fakeRepo{{slug: "repo-a", updatedOn: now.Add(-time.Hour)}})
	client := f.client(Options{DiscoveryTTL: time.Minute})

	// Different state → different list cache key → both hit the repo listing.
	for _, state := range []string{"OPEN", "MERGED"} {
		if _, err := client.ListPRs(context.Background(), ListOptions{
			Role: "reviewer", State: state, Workspace: "ws", ActiveDays: 14,
		}); err != nil {
			t.Fatalf("ListPRs(%s): %v", state, err)
		}
	}
	if got := f.discoveryCount(); got != 1 {
		t.Fatalf("discovery calls = %d, want 1 (cached)", got)
	}
}

func TestDiscoveryFailureFallsBackToConfiguredRepos(t *testing.T) {
	var listCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/repositories/ws", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 500, map[string]any{"error": map[string]any{"message": "discovery down"}})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		listCalls++
		writeJSON(t, w, 200, map[string]any{"values": []any{map[string]any{
			"id": 1, "title": "x", "state": "OPEN", "updated_on": "2026-01-02T09:00:00+00:00",
			"source": map[string]any{"commit": map[string]any{"hash": ""}},
		}}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Options{BaseURL: srv.URL, Email: "me@example.com", Token: "tok", Logger: discardLogger()})
	result, err := client.ListPRs(context.Background(), ListOptions{
		Role: "reviewer", State: "OPEN", Workspace: "ws",
		Repos:      []Repo{{Workspace: "ws", Repo: "repo-a"}},
		ActiveDays: 14,
	})
	if err != nil {
		t.Fatalf("ListPRs: %v", err)
	}
	if listCalls != 1 {
		t.Fatalf("configured repo list calls = %d, want 1", listCalls)
	}
	if len(result.PRs) != 1 {
		t.Fatalf("prs = %d, want 1", len(result.PRs))
	}
}

func TestStaleWhileRevalidate(t *testing.T) {
	now := time.Now()
	f := newFakeWorkspace(t, []fakeRepo{{slug: "repo-a", updatedOn: now.Add(-time.Hour)}})
	client := f.client(Options{StaleAfter: 30 * time.Millisecond})

	// First request blocks and fills the cache.
	first, err := client.ListPRs(context.Background(), ListOptions{
		Role: "reviewer", State: "OPEN", Workspace: "ws", ActiveDays: 14,
	})
	if err != nil {
		t.Fatalf("first ListPRs: %v", err)
	}
	if got := f.listCallCount(); got != 1 {
		t.Fatalf("upstream list calls = %d, want 1", got)
	}
	if first.FetchedAt.IsZero() {
		t.Fatal("FetchedAt is zero")
	}
	if first.PRs[0].Title != "pr" {
		t.Fatalf("title = %q", first.PRs[0].Title)
	}

	// Within the freshness window: served from cache, no upstream call.
	if _, err := client.ListPRs(context.Background(), ListOptions{
		Role: "reviewer", State: "OPEN", Workspace: "ws", ActiveDays: 14,
	}); err != nil {
		t.Fatalf("fresh ListPRs: %v", err)
	}
	if got := f.listCallCount(); got != 1 {
		t.Fatalf("fresh cache hit made %d upstream calls, want 1 total", got)
	}

	// Make the refresh block so we can observe single-flight behaviour.
	block := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(block) }) }
	defer release()

	started := make(chan struct{}, 8)
	f.setStarted(started)
	f.setBlock(block)
	f.setTitle("pr-v2")

	time.Sleep(60 * time.Millisecond)

	// Stale request: returns the old copy immediately and starts one refresh.
	stale, err := client.ListPRs(context.Background(), ListOptions{
		Role: "reviewer", State: "OPEN", Workspace: "ws", ActiveDays: 14,
	})
	if err != nil {
		t.Fatalf("stale ListPRs: %v", err)
	}
	if stale.PRs[0].Title != "pr" {
		t.Fatalf("stale title = %q, want the old copy", stale.PRs[0].Title)
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("background refresh did not start")
	}

	// Further stale requests must not start a second refresh.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := client.ListPRs(context.Background(), ListOptions{
				Role: "reviewer", State: "OPEN", Workspace: "ws", ActiveDays: 14,
			}); err != nil {
				t.Errorf("concurrent stale ListPRs: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := f.listCallCount(); got != 2 {
		t.Fatalf("upstream list calls = %d, want 2 (single-flight)", got)
	}

	release()
	f.setBlock(nil)

	// The refresh lands: the next request serves the new data.
	waitFor(t, 2*time.Second, "refreshed data", func() bool {
		result, err := client.ListPRs(context.Background(), ListOptions{
			Role: "reviewer", State: "OPEN", Workspace: "ws", ActiveDays: 14,
		})
		return err == nil && len(result.PRs) == 1 && result.PRs[0].Title == "pr-v2"
	})
}

func TestRateLimitKeepsPartialResultsAndBacksOff(t *testing.T) {
	now := time.Now()
	_ = now

	var mu sync.Mutex
	calls := map[string]int{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/repositories/ws", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 200, map[string]any{"values": []any{}})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-ok/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls["repo-ok"]++
		mu.Unlock()
		writeJSON(t, w, 200, map[string]any{"values": []any{map[string]any{
			"id": 1, "title": "kept", "state": "OPEN", "updated_on": "2026-01-02T09:00:00+00:00",
			"source": map[string]any{"commit": map[string]any{"hash": ""}},
		}}})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-limited/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls["repo-limited"]++
		mu.Unlock()
		time.Sleep(30 * time.Millisecond) // let the healthy repo finish first
		w.Header().Set("Retry-After", "5")
		writeJSON(t, w, 429, map[string]any{"error": map[string]any{"message": "rate limited"}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Options{BaseURL: srv.URL, Email: "me@example.com", Token: "tok", Logger: discardLogger()})
	opts := ListOptions{
		Role: "reviewer", State: "OPEN", Workspace: "ws",
		Repos:      []Repo{{Workspace: "ws", Repo: "repo-ok"}, {Workspace: "ws", Repo: "repo-limited"}},
		ActiveDays: 14,
	}
	result, err := client.ListPRs(context.Background(), opts)
	if err != nil {
		t.Fatalf("ListPRs must tolerate a 429: %v", err)
	}
	if !result.RateLimited {
		t.Fatal("RateLimited = false, want true")
	}
	if len(result.PRs) != 1 || result.PRs[0].Title != "kept" {
		t.Fatalf("partial results = %+v, want the healthy repo's PR", result.PRs)
	}
	if len(result.Failed) != 0 {
		t.Fatalf("failed = %v, want none (429 is reported separately)", result.Failed)
	}

	mu.Lock()
	before := calls["repo-ok"] + calls["repo-limited"]
	mu.Unlock()

	// During the backoff window the cached copy is served without new calls.
	second, err := client.ListPRs(context.Background(), opts)
	if err != nil {
		t.Fatalf("second ListPRs: %v", err)
	}
	if !second.RateLimited {
		t.Fatal("second call should still report the rate limit")
	}
	mu.Lock()
	after := calls["repo-ok"] + calls["repo-limited"]
	mu.Unlock()
	if after != before {
		t.Fatalf("upstream calls during backoff = %d, want %d", after, before)
	}
}

func TestParseRetryAfter(t *testing.T) {
	future := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	past := time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat)

	tests := []struct {
		name  string
		value string
		min   time.Duration
		max   time.Duration
	}{
		{name: "seconds", value: "12", min: 12 * time.Second, max: 12 * time.Second},
		{name: "zero", value: "0"},
		{name: "negative", value: "-4"},
		{name: "garbage", value: "soon"},
		{name: "empty", value: ""},
		{name: "http date", value: future, min: 25 * time.Second, max: 31 * time.Second},
		{name: "http date in the past", value: past},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseRetryAfter(tc.value)
			if tc.min == 0 && tc.max == 0 {
				if got != 0 {
					t.Fatalf("parseRetryAfter(%q) = %v, want 0", tc.value, got)
				}
				return
			}
			if got < tc.min || got > tc.max {
				t.Fatalf("parseRetryAfter(%q) = %v, want between %v and %v", tc.value, got, tc.min, tc.max)
			}
		})
	}
}

func TestMergeReposDeduplicates(t *testing.T) {
	configured := []Repo{
		{Workspace: "ws", Repo: "repo-a"},
		{Workspace: "other", Repo: "repo-b"},
	}
	discovered := []Repo{
		{Workspace: "ws", Repo: "repo-a"}, // duplicate
		{Workspace: "WS", Repo: "Repo-A"}, // duplicate, different case
		{Workspace: "ws", Repo: "repo-c"}, // new
		{Workspace: "", Repo: "broken"},   // dropped
	}
	got := mergeRepos(configured, discovered)
	want := []Repo{
		{Workspace: "ws", Repo: "repo-a"},
		{Workspace: "other", Repo: "repo-b"},
		{Workspace: "ws", Repo: "repo-c"},
	}
	if len(got) != len(want) {
		t.Fatalf("merged = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("merged[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestAuthorRateLimitIsReported(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/workspaces/ws/pullrequests/{user}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		writeJSON(t, w, 429, map[string]any{"error": map[string]any{"message": "slow down"}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Options{BaseURL: srv.URL, Email: "me@example.com", Token: "tok", Logger: discardLogger()})
	result, err := client.ListPRs(context.Background(), ListOptions{
		Role: "author", State: "OPEN", Workspace: "ws", ActiveDays: 14,
	})
	if err != nil {
		t.Fatalf("author 429 should not fail the request: %v", err)
	}
	if !result.RateLimited {
		t.Fatal("RateLimited = false, want true")
	}
	if len(result.PRs) != 0 {
		t.Fatalf("prs = %+v, want none", result.PRs)
	}
}

func TestDiscoveryRejectsEmptyWorkspace(t *testing.T) {
	client := New(Options{BaseURL: "http://127.0.0.1:1", Email: "me@example.com", Token: "tok", Logger: discardLogger()})
	if _, err := client.discoverRepos(context.Background(), "", 14); err == nil {
		t.Fatal("expected an error for an empty workspace")
	}
}

// guard against accidental JSON drift in the fake
var _ = json.Marshal
