package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/anggorodewanto/bb-review/internal/bitbucket"
	"github.com/anggorodewanto/bb-review/internal/store"
)

const meJSON = `{"uuid":"uuid-me","account_id":"acc-me","display_name":"Me Myself"}`

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// fakeClock is a controllable clock.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeBB is a small Bitbucket fake: a workspace repo listing plus one PR list per repo.
type fakeBB struct {
	t        *testing.T
	srv      *httptest.Server
	pageSize int

	mu         sync.Mutex
	repos      []fakeRepo
	prs        map[string]int
	titles     map[string]string
	failStatus map[string]int
	retryAfter map[string]string
	blocks     map[string]chan struct{}
	delays     map[string]time.Duration

	discoveryCalls int
	listCalls      map[string]int
}

type fakeRepo struct {
	slug      string
	updatedOn time.Time
}

func newFakeBB(t *testing.T, repos ...fakeRepo) *fakeBB {
	t.Helper()
	f := &fakeBB{
		t:          t,
		pageSize:   100,
		prs:        map[string]int{},
		titles:     map[string]string{},
		failStatus: map[string]int{},
		retryAfter: map[string]string{},
		blocks:     map[string]chan struct{}{},
		delays:     map[string]time.Duration{},
		listCalls:  map[string]int{},
	}
	for _, repo := range repos {
		f.repos = append(f.repos, repo)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(meJSON))
	})
	mux.HandleFunc("GET /2.0/repositories/ws", func(w http.ResponseWriter, r *http.Request) {
		page := 1
		fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
		if page < 1 {
			page = 1
		}

		f.mu.Lock()
		f.discoveryCalls++
		size := f.pageSize
		repos := append([]fakeRepo(nil), f.repos...)
		f.mu.Unlock()

		sort.SliceStable(repos, func(i, j int) bool { return repos[i].updatedOn.After(repos[j].updatedOn) })

		start := (page - 1) * size
		values := make([]any, 0, size)
		for i := start; i < len(repos) && i < start+size; i++ {
			values = append(values, map[string]any{
				"slug":       repos[i].slug,
				"updated_on": repos[i].updatedOn.UTC().Format(time.RFC3339),
			})
		}
		next := ""
		if start+size < len(repos) {
			next = fmt.Sprintf("%s/2.0/repositories/ws?page=%d", f.srv.URL, page+1)
		}
		writeJSON(t, w, 200, map[string]any{"values": values, "next": next})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/{repo}/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		repo := r.PathValue("repo")

		f.mu.Lock()
		f.listCalls[repo]++
		status := f.failStatus[repo]
		retryAfter := f.retryAfter[repo]
		count := f.prs[repo]
		title := f.titles[repo]
		block := f.blocks[repo]
		delay := f.delays[repo]
		f.mu.Unlock()

		if block != nil {
			<-block
		}
		if delay > 0 {
			time.Sleep(delay)
		}
		if status == 429 {
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			writeJSON(t, w, 429, map[string]any{"error": map[string]any{"message": "rate limited"}})
			return
		}
		if status >= 400 {
			writeJSON(t, w, status, map[string]any{"error": map[string]any{"message": "upstream failure"}})
			return
		}
		writeJSON(t, w, 200, map[string]any{"values": prValues(repo, count, title)})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func prValues(repo string, count int, title string) []any {
	if title == "" {
		title = "pr"
	}
	values := make([]any, 0, count)
	for i := 0; i < count; i++ {
		values = append(values, map[string]any{
			"id":         i + 1,
			"title":      fmt.Sprintf("%s-%s-%d", title, repo, i+1),
			"state":      "OPEN",
			"updated_on": fmt.Sprintf("2026-01-%02dT09:00:00+00:00", 10+i),
			"author":     map[string]any{"uuid": "uuid-author", "display_name": "Ann"},
			"participants": []any{
				map[string]any{"user": map[string]any{"uuid": "uuid-me"}, "role": "REVIEWER", "approved": true},
			},
			"source":      map[string]any{"commit": map[string]any{"hash": ""}},
			"destination": map[string]any{"repository": map[string]any{"full_name": "ws/" + repo}},
		})
	}
	return values
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

func (f *fakeBB) client() *bitbucket.Client {
	return bitbucket.New(bitbucket.Options{
		BaseURL: f.srv.URL,
		Email:   "me@example.com",
		Token:   "tok",
		Logger:  discardLogger(),
	})
}

func (f *fakeBB) setPRs(repo string, count int, title string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prs[repo] = count
	if title != "" {
		f.titles[repo] = title
	}
}

func (f *fakeBB) setUpdatedOn(repo string, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.repos {
		if f.repos[i].slug == repo {
			f.repos[i].updatedOn = at
		}
	}
}

func (f *fakeBB) removeRepo(repo string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	kept := f.repos[:0]
	for _, item := range f.repos {
		if item.slug != repo {
			kept = append(kept, item)
		}
	}
	f.repos = kept
}

func (f *fakeBB) failRepo(repo string, status int, retryAfter string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failStatus[repo] = status
	f.retryAfter[repo] = retryAfter
}

func (f *fakeBB) delayRepo(repo string, d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delays[repo] = d
}

func (f *fakeBB) blockRepo(repo string) chan struct{} {
	ch := make(chan struct{})
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blocks[repo] = ch
	return ch
}

func (f *fakeBB) listCallCount(repo string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listCalls[repo]
}

func (f *fakeBB) totalListCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, n := range f.listCalls {
		total += n
	}
	return total
}

func (f *fakeBB) discoveryCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.discoveryCalls
}

func newTestStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return st, path
}

func newTestScanner(t *testing.T, f *fakeBB, st *store.Store, clock *fakeClock) *Scanner {
	t.Helper()
	return New(Options{
		Client:            f.client(),
		Store:             st,
		Logger:            discardLogger(),
		Workspace:         "ws",
		ActiveDays:        14,
		ScanInterval:      time.Minute,
		FullSweepInterval: 6 * time.Hour,
		Now:               clock.Now,
	})
}

func reps(slugs ...string) []fakeRepo {
	now := time.Now()
	out := make([]fakeRepo, 0, len(slugs))
	for i, slug := range slugs {
		out = append(out, fakeRepo{slug: slug, updatedOn: now.Add(-time.Duration(i+1) * time.Hour)})
	}
	return out
}

func TestFirstTickIsFullSweep(t *testing.T) {
	f := newFakeBB(t, reps("repo-a", "repo-b", "repo-c")...)
	f.setPRs("repo-a", 1, "a")
	f.setPRs("repo-b", 1, "b")
	f.setPRs("repo-c", 0, "")
	st, _ := newTestStore(t)
	clock := newFakeClock()
	sc := newTestScanner(t, f, st, clock)

	if err := sc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	for _, repo := range []string{"repo-a", "repo-b", "repo-c"} {
		if got := f.listCallCount(repo); got != 1 {
			t.Fatalf("%s queried %d times, want 1", repo, got)
		}
	}

	snapshot := sc.Snapshot()
	if len(snapshot.PRs) != 2 {
		t.Fatalf("prs = %d, want 2", len(snapshot.PRs))
	}
	if snapshot.FetchedAt.IsZero() {
		t.Fatal("FetchedAt is zero")
	}
	if snapshot.Scanning {
		t.Fatal("Scanning should be false after the tick")
	}
	if !snapshot.FetchedAt.Equal(clock.Now()) {
		t.Fatalf("FetchedAt = %v, want the tick time %v", snapshot.FetchedAt, clock.Now())
	}

	// Both the full sweep and the tick time are persisted.
	raw := st.ScannerState()
	if raw == nil {
		t.Fatal("scanner state was not persisted")
	}
	var state State
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if state.LastFullSweepAt == "" || state.LastTickAt == "" {
		t.Fatalf("state timestamps = %+v", state)
	}
	if len(state.Repos) != 3 {
		t.Fatalf("persisted repos = %d, want 3", len(state.Repos))
	}
	if got := state.Repos["ws/repo-a"].LastScannedUpdatedOn; got == "" {
		t.Fatalf("repo-a updated_on was not recorded: %+v", state.Repos["ws/repo-a"])
	}
}

func TestSecondTickQueriesOnlyChangedAndHotRepos(t *testing.T) {
	f := newFakeBB(t, reps("repo-a", "repo-b", "repo-c")...)
	f.setPRs("repo-a", 0, "")  // unchanged, not hot
	f.setPRs("repo-b", 2, "b") // unchanged but hot
	f.setPRs("repo-c", 1, "c") // will change
	st, _ := newTestStore(t)
	clock := newFakeClock()
	sc := newTestScanner(t, f, st, clock)

	if err := sc.Tick(context.Background()); err != nil {
		t.Fatalf("first Tick: %v", err)
	}

	// repo-c gets a new updated_on; time moves past the scan interval.
	f.setUpdatedOn("repo-c", clock.Now().Add(-time.Minute))
	clock.Advance(61 * time.Second)

	if err := sc.Tick(context.Background()); err != nil {
		t.Fatalf("second Tick: %v", err)
	}

	if got := f.listCallCount("repo-a"); got != 1 {
		t.Fatalf("repo-a queried %d times, want 1 (unchanged, not hot)", got)
	}
	if got := f.listCallCount("repo-b"); got != 2 {
		t.Fatalf("repo-b queried %d times, want 2 (hot)", got)
	}
	if got := f.listCallCount("repo-c"); got != 2 {
		t.Fatalf("repo-c queried %d times, want 2 (changed)", got)
	}

	// A third tick with nothing changed queries only the hot repos (b and c).
	if err := sc.Tick(context.Background()); err != nil {
		t.Fatalf("third Tick: %v", err)
	}
	if got := f.listCallCount("repo-c"); got != 3 {
		t.Fatalf("repo-c queried %d times, want 3 (hot)", got)
	}
	if got := f.listCallCount("repo-b"); got != 3 {
		t.Fatalf("repo-b queried %d times, want 3 (still hot)", got)
	}
	if got := f.listCallCount("repo-a"); got != 1 {
		t.Fatalf("repo-a queried %d times, want 1 (unchanged, not hot)", got)
	}
}

func TestFullSweepAfterInterval(t *testing.T) {
	f := newFakeBB(t, reps("repo-a", "repo-b")...)
	f.setPRs("repo-a", 0, "")
	f.setPRs("repo-b", 0, "")
	st, _ := newTestStore(t)
	clock := newFakeClock()
	sc := newTestScanner(t, f, st, clock)

	if err := sc.Tick(context.Background()); err != nil {
		t.Fatalf("first Tick: %v", err)
	}

	// Incremental tick: nothing changed, nothing hot.
	clock.Advance(time.Minute)
	if err := sc.Tick(context.Background()); err != nil {
		t.Fatalf("second Tick: %v", err)
	}
	if got := f.totalListCalls(); got != 2 {
		t.Fatalf("incremental tick queried %d repos, want 0 extra (total 2)", got-2)
	}

	clock.Advance(6*time.Hour + time.Second)
	if err := sc.Tick(context.Background()); err != nil {
		t.Fatalf("third Tick: %v", err)
	}
	if got := f.listCallCount("repo-a"); got != 2 {
		t.Fatalf("repo-a queried %d times, want 2 after the full sweep", got)
	}
	if got := f.listCallCount("repo-b"); got != 2 {
		t.Fatalf("repo-b queried %d times, want 2 after the full sweep", got)
	}
}

func TestRateLimitStopsTheTickAndBacksOff(t *testing.T) {
	f := newFakeBB(t, reps("repo-a", "repo-b", "repo-c")...)
	f.setPRs("repo-a", 1, "a")
	f.setPRs("repo-c", 1, "c")
	f.failRepo("repo-b", 429, "2")
	f.delayRepo("repo-b", 30*time.Millisecond)
	st, _ := newTestStore(t)
	clock := newFakeClock()
	sc := newTestScanner(t, f, st, clock)

	if err := sc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	snapshot := sc.Snapshot()
	if !snapshot.RateLimited {
		t.Fatal("RateLimited = false, want true")
	}
	if len(snapshot.PRs) != 2 {
		t.Fatalf("partial results = %d, want the two healthy repos", len(snapshot.PRs))
	}
	if len(snapshot.Failed) != 0 {
		t.Fatalf("failed = %v, want none for a 429", snapshot.Failed)
	}

	before := f.totalListCalls()

	// Inside the backoff window the tick is a no-op.
	clock.Advance(time.Second)
	if err := sc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick during backoff: %v", err)
	}
	if after := f.totalListCalls(); after != before {
		t.Fatalf("upstream calls during backoff = %d, want %d", after, before)
	}
	if !sc.Snapshot().RateLimited {
		t.Fatal("RateLimited = false during backoff")
	}

	// After the backoff the tick runs again.
	clock.Advance(2 * time.Second)
	if err := sc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick after backoff: %v", err)
	}
	if after := f.totalListCalls(); after <= before {
		t.Fatalf("tick after backoff made no calls (%d)", after)
	}
}

func TestRateLimitBackoffDoubles(t *testing.T) {
	f := newFakeBB(t, reps("repo-a")...)
	f.failRepo("repo-a", 429, "")
	st, _ := newTestStore(t)
	clock := newFakeClock()
	sc := newTestScanner(t, f, st, clock)

	if err := sc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	sc.mu.Lock()
	first := sc.backoff
	sc.mu.Unlock()
	if first != baseBackoff {
		t.Fatalf("first backoff = %v, want %v", first, baseBackoff)
	}

	// Second 429 (no Retry-After) doubles to 2 minutes.
	clock.Advance(baseBackoff + time.Second)
	if err := sc.Tick(context.Background()); err != nil {
		t.Fatalf("second Tick: %v", err)
	}
	sc.mu.Lock()
	second := sc.backoff
	sc.mu.Unlock()
	if second != 2*baseBackoff {
		t.Fatalf("second backoff = %v, want %v", second, 2*baseBackoff)
	}

	// It is capped at 10 minutes.
	for i := 0; i < 10; i++ {
		clock.Advance(second + time.Second)
		if err := sc.Tick(context.Background()); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		sc.mu.Lock()
		second = sc.backoff
		sc.mu.Unlock()
	}
	if second != maxBackoff {
		t.Fatalf("backoff = %v, want the %v cap", second, maxBackoff)
	}
}

func TestRateLimitHonoursRetryAfter(t *testing.T) {
	f := newFakeBB(t, reps("repo-a")...)
	f.failRepo("repo-a", 429, "120")
	st, _ := newTestStore(t)
	clock := newFakeClock()
	sc := newTestScanner(t, f, st, clock)

	if err := sc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	sc.mu.Lock()
	until := sc.rateLimitedUntil
	sc.mu.Unlock()
	if got := until.Sub(clock.Now()); got != 120*time.Second {
		t.Fatalf("backoff window = %v, want 120s", got)
	}
}

func TestStateIsPersistedAndReloaded(t *testing.T) {
	f := newFakeBB(t, reps("repo-a", "repo-b")...)
	f.setPRs("repo-a", 1, "a")
	f.setPRs("repo-b", 0, "")
	st, _ := newTestStore(t)
	clock := newFakeClock()
	sc := newTestScanner(t, f, st, clock)

	if err := sc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	wantFetched := sc.Snapshot().FetchedAt

	reloaded := newTestScanner(t, f, st, clock)
	snapshot := reloaded.Snapshot()
	if len(snapshot.PRs) != 1 {
		t.Fatalf("reloaded prs = %d, want 1", len(snapshot.PRs))
	}
	if snapshot.FetchedAt.IsZero() || !snapshot.FetchedAt.Equal(wantFetched) {
		t.Fatalf("reloaded FetchedAt = %v, want %v", snapshot.FetchedAt, wantFetched)
	}

	// No full sweep on the first tick after a restart: unchanged repos are skipped.
	clock.Advance(time.Minute)
	if err := reloaded.Tick(context.Background()); err != nil {
		t.Fatalf("reloaded Tick: %v", err)
	}
	if got := f.listCallCount("repo-b"); got != 1 {
		t.Fatalf("repo-b queried %d times, want 1 (unchanged after restart)", got)
	}
	if got := f.listCallCount("repo-a"); got != 2 {
		t.Fatalf("repo-a queried %d times, want 2 (hot)", got)
	}
}

func TestOutOfWindowReposKeepPRsUntilEmpty(t *testing.T) {
	f := newFakeBB(t, reps("repo-a", "repo-b")...)
	f.setPRs("repo-a", 0, "")
	f.setPRs("repo-b", 1, "b")
	st, _ := newTestStore(t)
	clock := newFakeClock()
	sc := newTestScanner(t, f, st, clock)

	if err := sc.Tick(context.Background()); err != nil {
		t.Fatalf("first Tick: %v", err)
	}

	// repo-b falls out of the window but still has an open PR: keep and re-check it.
	f.removeRepo("repo-b")
	clock.Advance(61 * time.Second)
	if err := sc.Tick(context.Background()); err != nil {
		t.Fatalf("second Tick: %v", err)
	}
	if got := f.listCallCount("repo-b"); got != 2 {
		t.Fatalf("repo-b queried %d times, want 2 (hot, out of window)", got)
	}
	if len(sc.Snapshot().PRs) != 1 {
		t.Fatalf("prs = %d, want the out-of-window PR kept", len(sc.Snapshot().PRs))
	}

	// Once its PRs are gone the entry is dropped.
	f.setPRs("repo-b", 0, "")
	clock.Advance(61 * time.Second)
	if err := sc.Tick(context.Background()); err != nil {
		t.Fatalf("third Tick: %v", err)
	}
	if got := len(sc.Snapshot().PRs); got != 0 {
		t.Fatalf("prs = %d, want 0 after the repo drained", got)
	}
	raw := st.ScannerState()
	var state State
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if _, ok := state.Repos["ws/repo-b"]; ok {
		t.Fatalf("repo-b should be gone from state: %+v", state.Repos)
	}
}

func TestConfigReposAreAlwaysQueried(t *testing.T) {
	f := newFakeBB(t, reps("repo-a")...)
	st, _ := newTestStore(t)
	clock := newFakeClock()
	sc := New(Options{
		Client:            f.client(),
		Store:             st,
		Logger:            discardLogger(),
		Workspace:         "ws",
		ConfigRepos:       []bitbucket.Repo{{Workspace: "ws", Repo: "pinned"}},
		ActiveDays:        14,
		ScanInterval:      time.Minute,
		FullSweepInterval: 6 * time.Hour,
		Now:               clock.Now,
	})

	if err := sc.Tick(context.Background()); err != nil {
		t.Fatalf("first Tick: %v", err)
	}
	clock.Advance(time.Minute)
	if err := sc.Tick(context.Background()); err != nil {
		t.Fatalf("second Tick: %v", err)
	}
	if got := f.listCallCount("pinned"); got != 2 {
		t.Fatalf("configured repo queried %d times, want 2 (always)", got)
	}
	if got := f.listCallCount("repo-a"); got != 1 {
		t.Fatalf("repo-a queried %d times, want 1 (unchanged)", got)
	}
}

func TestSingleFlight(t *testing.T) {
	f := newFakeBB(t, reps("repo-a")...)
	release := f.blockRepo("repo-a")
	var releaseOnce sync.Once
	doRelease := func() { releaseOnce.Do(func() { close(release) }) }
	defer doRelease()

	st, _ := newTestStore(t)
	clock := newFakeClock()
	sc := newTestScanner(t, f, st, clock)

	done := make(chan error, 1)
	go func() { done <- sc.Tick(context.Background()) }()

	// Wait until the blocked query is in flight.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if f.listCallCount("repo-a") == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the first tick did not start")
		}
		time.Sleep(2 * time.Millisecond)
	}

	if err := sc.Tick(context.Background()); err != ErrBusy {
		t.Fatalf("second Tick = %v, want ErrBusy", err)
	}
	if sc.Trigger(context.Background()) {
		t.Fatal("Trigger should be refused while a tick is running")
	}

	doRelease()
	if err := <-done; err != nil {
		t.Fatalf("first Tick: %v", err)
	}
	if sc.Snapshot().FetchedAt.IsZero() {
		t.Fatal("tick did not complete")
	}
}

func TestTriggerDebounces(t *testing.T) {
	f := newFakeBB(t, reps("repo-a")...)
	st, _ := newTestStore(t)
	clock := newFakeClock()
	sc := newTestScanner(t, f, st, clock)

	if !sc.Trigger(context.Background()) {
		t.Fatal("first Trigger was refused")
	}
	if sc.Trigger(context.Background()) {
		t.Fatal("second Trigger should be debounced")
	}

	// Wait for the first tick to finish (including the ticking flag), then reset
	// the debounce window.
	waitFor(t, 2*time.Second, "the triggered tick", func() bool {
		sc.mu.Lock()
		defer sc.mu.Unlock()
		return !sc.ticking && !sc.lastTickAt.IsZero()
	})
	clock.Advance(triggerDebounce + time.Second)
	if !sc.Trigger(context.Background()) {
		t.Fatal("Trigger after the debounce window was refused")
	}
	waitFor(t, 2*time.Second, "the second triggered tick", func() bool {
		return sc.Snapshot().FetchedAt.Equal(clock.Now())
	})
}

func TestRefreshRepoUpdatesOneEntry(t *testing.T) {
	f := newFakeBB(t, reps("repo-a", "repo-b")...)
	f.setPRs("repo-a", 1, "before")
	f.setPRs("repo-b", 1, "b")
	st, _ := newTestStore(t)
	clock := newFakeClock()
	sc := newTestScanner(t, f, st, clock)

	if err := sc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	before := f.totalListCalls()

	f.setPRs("repo-a", 1, "after")
	if err := sc.RefreshRepo(context.Background(), bitbucket.Repo{Workspace: "ws", Repo: "repo-a"}); err != nil {
		t.Fatalf("RefreshRepo: %v", err)
	}
	if got := f.totalListCalls() - before; got != 1 {
		t.Fatalf("refresh made %d upstream calls, want 1", got)
	}

	found := false
	for _, pr := range sc.Snapshot().PRs {
		if pr.Repo == "repo-a" && pr.Title == "after-repo-a-1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("refreshed PR not in the snapshot: %+v", sc.Snapshot().PRs)
	}
}

func TestSnapshotSortsByUpdatedOnDesc(t *testing.T) {
	f := newFakeBB(t, reps("repo-a")...)
	f.setPRs("repo-a", 3, "x")
	st, _ := newTestStore(t)
	clock := newFakeClock()
	sc := newTestScanner(t, f, st, clock)

	if err := sc.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	prs := sc.Snapshot().PRs
	if len(prs) != 3 {
		t.Fatalf("prs = %d, want 3", len(prs))
	}
	for i := 1; i < len(prs); i++ {
		if prs[i-1].UpdatedOn < prs[i].UpdatedOn {
			t.Fatalf("prs not sorted desc: %q then %q", prs[i-1].UpdatedOn, prs[i].UpdatedOn)
		}
	}
}

func TestScanningProgressDuringFirstSweep(t *testing.T) {
	f := newFakeBB(t, reps("repo-a", "repo-b", "repo-c")...)
	f.setPRs("repo-a", 1, "a")
	f.setPRs("repo-b", 1, "b")
	release := f.blockRepo("repo-c")
	defer close(release)

	st, _ := newTestStore(t)
	clock := newFakeClock()
	sc := newTestScanner(t, f, st, clock)

	done := make(chan error, 1)
	go func() { done <- sc.Tick(context.Background()) }()

	waitFor(t, 2*time.Second, "the first two repos", func() bool {
		snapshot := sc.Snapshot()
		return snapshot.Scanning && snapshot.Done == 2
	})
	snapshot := sc.Snapshot()
	if snapshot.Total != 3 {
		t.Fatalf("total = %d, want 3", snapshot.Total)
	}
	if len(snapshot.PRs) != 2 {
		t.Fatalf("partial results = %d, want 2", len(snapshot.PRs))
	}

	release <- struct{}{}
	if err := <-done; err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if sc.Snapshot().Scanning {
		t.Fatal("Scanning should be false once the sweep finished")
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
