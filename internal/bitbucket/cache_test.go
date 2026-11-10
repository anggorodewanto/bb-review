package bitbucket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingFake is a small upstream that counts one call per endpoint.
type countingFake struct {
	mu       sync.Mutex
	calls    map[string]int
	delay    time.Duration
	diffBody string
	comments []any
}

func newCountingFake() *countingFake {
	return &countingFake{calls: map[string]int{}, diffBody: "diff --git a/x b/x\n+x\n"}
}

func (f *countingFake) count(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[name]++
}

func (f *countingFake) callCount(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[name]
}

func (f *countingFake) wait() {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
}

// prWithSource returns a PR payload whose source commit is hash.
func prWithSource(id int, hash string) map[string]any {
	pr := prJSON(id, "pr", "2026-01-03T09:00:00+00:00")
	pr["source"] = map[string]any{
		"branch": map[string]any{"name": "feat"},
		"commit": map[string]any{"hash": hash},
	}
	return pr
}

// fakeClient serves user, PR, merge-base, statuses, comments and diff endpoints.
func (f *countingFake) client(t *testing.T, hash func() string, now func() time.Time) (*Client, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(meJSON))
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests/1", func(w http.ResponseWriter, r *http.Request) {
		f.count("pr")
		f.wait()
		writeJSON(t, w, 200, prWithSource(1, hash()))
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests/1/merge-base", func(w http.ResponseWriter, r *http.Request) {
		f.count("merge-base")
		f.wait()
		writeJSON(t, w, 200, map[string]any{"hash": "base999"})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/commit/{hash}/statuses", func(w http.ResponseWriter, r *http.Request) {
		f.count("statuses")
		f.wait()
		writeJSON(t, w, 200, map[string]any{
			"values": []any{map[string]any{"key": "ci", "name": "CI", "state": "SUCCESSFUL"}},
		})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests/1/comments", func(w http.ResponseWriter, r *http.Request) {
		f.count("comments")
		f.wait()
		writeJSON(t, w, 200, map[string]any{"values": f.comments})
	})
	mux.HandleFunc("POST /2.0/repositories/ws/repo-a/pullrequests/1/comments", func(w http.ResponseWriter, r *http.Request) {
		f.count("post-comment")
		writeJSON(t, w, 201, map[string]any{"id": 9, "content": map[string]any{"raw": "hi"}})
	})
	mux.HandleFunc("POST /2.0/repositories/ws/repo-a/pullrequests/1/approve", func(w http.ResponseWriter, r *http.Request) {
		f.count("approve")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests/1/diff", func(w http.ResponseWriter, r *http.Request) {
		f.count("diff")
		f.wait()
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("diff for " + hash()))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	opts := Options{BaseURL: srv.URL, Email: "me@example.com", Token: "tok", Logger: discardLogger()}
	if now != nil {
		opts.Now = now
	}
	return New(opts), srv
}

func fixedHash(hash string) func() string {
	return func() string { return hash }
}

func TestPRDetailFetchesConcurrently(t *testing.T) {
	fake := newCountingFake()
	fake.delay = 300 * time.Millisecond
	client, _ := fake.client(t, fixedHash("abc123"), nil)
	repo := Repo{Workspace: "ws", Repo: "repo-a"}

	start := time.Now()
	detail, err := client.PRDetail(context.Background(), repo, 1)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("PRDetail: %v", err)
	}
	// Sequential fetches would need at least 3x300ms; concurrent ones ~300ms.
	if elapsed > 700*time.Millisecond {
		t.Fatalf("PRDetail took %v: PR, merge-base and statuses are not concurrent", elapsed)
	}
	if elapsed < fake.delay {
		t.Fatalf("PRDetail took %v, faster than one upstream call", elapsed)
	}
	if detail.MergeBase != "base999" || detail.BuildState != BuildSuccessful || detail.SourceCommit != "abc123" {
		t.Fatalf("detail = %+v", detail)
	}
	for _, name := range []string{"pr", "merge-base", "statuses"} {
		if got := fake.callCount(name); got != 1 {
			t.Fatalf("%s calls = %d, want 1", name, got)
		}
	}
}

func TestPRDetailCacheHitAndWriteBust(t *testing.T) {
	fake := newCountingFake()
	clock := newClock()
	client, _ := fake.client(t, fixedHash("abc123"), clock.Now)
	repo := Repo{Workspace: "ws", Repo: "repo-a"}
	ctx := context.Background()

	if _, err := client.PRDetail(ctx, repo, 1); err != nil {
		t.Fatalf("first PRDetail: %v", err)
	}
	if _, err := client.PRDetail(ctx, repo, 1); err != nil {
		t.Fatalf("cached PRDetail: %v", err)
	}
	if got := fake.callCount("pr"); got != 1 {
		t.Fatalf("pr calls after a cache hit = %d, want 1", got)
	}

	// A comment busts the detail, diff and comment caches of that PR.
	if _, err := client.PostComment(ctx, repo, 1, "hi", nil, nil); err != nil {
		t.Fatalf("PostComment: %v", err)
	}
	if _, err := client.PRDetail(ctx, repo, 1); err != nil {
		t.Fatalf("PRDetail after comment: %v", err)
	}
	if got := fake.callCount("pr"); got != 2 {
		t.Fatalf("pr calls after a write = %d, want 2", got)
	}

	// Approve / request changes bust it too.
	if err := client.Approve(ctx, repo, 1, true); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if _, err := client.PRDetail(ctx, repo, 1); err != nil {
		t.Fatalf("PRDetail after approve: %v", err)
	}
	if got := fake.callCount("pr"); got != 3 {
		t.Fatalf("pr calls after approve = %d, want 3", got)
	}

	// The entry expires after 30s even without a write.
	clock.at = clock.at.Add(responseCacheTTL + time.Second)
	if _, err := client.PRDetail(ctx, repo, 1); err != nil {
		t.Fatalf("PRDetail after expiry: %v", err)
	}
	if got := fake.callCount("pr"); got != 4 {
		t.Fatalf("pr calls after expiry = %d, want 4", got)
	}
}

func TestCommentsCacheHitAndWriteBust(t *testing.T) {
	fake := newCountingFake()
	fake.comments = []any{map[string]any{
		"id": 1, "content": map[string]any{"raw": "first"},
		"created_on": "2026-01-01T10:00:00+00:00", "updated_on": "2026-01-01T10:00:00+00:00",
	}}
	client, _ := fake.client(t, fixedHash("abc123"), nil)
	repo := Repo{Workspace: "ws", Repo: "repo-a"}
	ctx := context.Background()

	first, err := client.ListComments(ctx, repo, 1)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	second, err := client.ListComments(ctx, repo, 1)
	if err != nil {
		t.Fatalf("cached ListComments: %v", err)
	}
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("comments = %d / %d, want 1 / 1", len(first), len(second))
	}
	if got := fake.callCount("comments"); got != 1 {
		t.Fatalf("comments calls after a cache hit = %d, want 1", got)
	}

	if _, err := client.PostComment(ctx, repo, 1, "reply", nil, nil); err != nil {
		t.Fatalf("PostComment: %v", err)
	}
	if _, err := client.ListComments(ctx, repo, 1); err != nil {
		t.Fatalf("ListComments after a write: %v", err)
	}
	if got := fake.callCount("comments"); got != 2 {
		t.Fatalf("comments calls after a write = %d, want 2", got)
	}
}

func TestDiffCacheDropsOnNewSourceCommit(t *testing.T) {
	fake := newCountingFake()
	clock := newClock()
	var hash atomic.Value
	hash.Store("sha1")
	client, _ := fake.client(t, func() string { return hash.Load().(string) }, clock.Now)
	repo := Repo{Workspace: "ws", Repo: "repo-a"}
	ctx := context.Background()

	first, err := client.Diff(ctx, repo, 1)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if got := string(first); got != "diff for sha1" {
		t.Fatalf("diff = %q", got)
	}
	if _, err := client.Diff(ctx, repo, 1); err != nil {
		t.Fatalf("cached Diff: %v", err)
	}
	if got := fake.callCount("diff"); got != 1 {
		t.Fatalf("diff calls after a cache hit = %d, want 1", got)
	}

	// A detail fetch without a known commit drops the diff (it cannot be tied to
	// a commit), then the new diff is cached with that commit.
	if _, err := client.PRDetail(ctx, repo, 1); err != nil {
		t.Fatalf("PRDetail: %v", err)
	}
	if _, err := client.Diff(ctx, repo, 1); err != nil {
		t.Fatalf("Diff after detail: %v", err)
	}
	if got := fake.callCount("diff"); got != 2 {
		t.Fatalf("diff calls after caching the detail = %d, want 2", got)
	}

	// A new push moves the source commit: the next detail fetch drops the diff.
	hash.Store("sha2")
	clock.at = clock.at.Add(responseCacheTTL + time.Second)
	if _, err := client.PRDetail(ctx, repo, 1); err != nil {
		t.Fatalf("PRDetail after push: %v", err)
	}
	after, err := client.Diff(ctx, repo, 1)
	if err != nil {
		t.Fatalf("Diff after push: %v", err)
	}
	if got := string(after); got != "diff for sha2" {
		t.Fatalf("diff after push = %q, want the new commit's diff", got)
	}
	if got := fake.callCount("diff"); got != 3 {
		t.Fatalf("diff calls after a push = %d, want 3", got)
	}
}
