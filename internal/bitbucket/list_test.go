package bitbucket

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"sync"
	"testing"
	"time"
)

func mustParseQuery(t *testing.T, raw string) neturl.Values {
	t.Helper()
	values, err := neturl.ParseQuery(raw)
	if err != nil {
		t.Fatalf("parse query %q: %v", raw, err)
	}
	return values
}

// discoveryFake serves a paged workspace repository listing.
type discoveryFake struct {
	t        *testing.T
	repos    []fakeRepo
	pageSize int
	server   *httptest.Server

	mu    sync.Mutex
	calls int
}

type fakeRepo struct {
	slug      string
	updatedOn time.Time
}

func newDiscoveryFake(t *testing.T, repos []fakeRepo, pageSize int) *discoveryFake {
	t.Helper()
	f := &discoveryFake{t: t, repos: repos, pageSize: pageSize}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/repositories/ws", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if page := query.Get("page"); page == "" {
			if got := query.Get("sort"); got != "-updated_on" {
				t.Errorf("sort = %q, want -updated_on", got)
			}
			if got := query.Get("pagelen"); got != "100" {
				t.Errorf("pagelen = %q, want 100", got)
			}
			if got := query.Get("fields"); got != "next,values.slug,values.updated_on" {
				t.Errorf("fields = %q", got)
			}
		}

		f.mu.Lock()
		f.calls++
		page := 1
		fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
		if page == 0 {
			page = 1
		}
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
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *discoveryFake) client() *Client {
	return New(Options{
		BaseURL: f.server.URL,
		Email:   "me@example.com",
		Token:   "tok",
		Logger:  discardLogger(),
	})
}

func (f *discoveryFake) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestDiscoverActiveReposStopsAtCutoff(t *testing.T) {
	now := time.Now()
	f := newDiscoveryFake(t, []fakeRepo{
		{slug: "repo-a", updatedOn: now.Add(-24 * time.Hour)},
		{slug: "repo-b", updatedOn: now.Add(-3 * 24 * time.Hour)},
		{slug: "repo-old", updatedOn: now.Add(-30 * 24 * time.Hour)},
		{slug: "repo-ancient", updatedOn: now.Add(-90 * 24 * time.Hour)},
	}, 2)

	repos, err := f.client().DiscoverActiveRepos(context.Background(), "ws", 14)
	if err != nil {
		t.Fatalf("DiscoverActiveRepos: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("repos = %+v, want 2", repos)
	}
	if repos[0].Repo.Repo != "repo-a" || repos[1].Repo.Repo != "repo-b" {
		t.Fatalf("order = %q, %q", repos[0].Repo.Repo, repos[1].Repo.Repo)
	}
	if repos[0].UpdatedOn.IsZero() {
		t.Fatal("updated_on was not parsed")
	}
	if got := f.callCount(); got != 2 {
		t.Fatalf("discovery pages = %d, want 2 (stop at cutoff)", got)
	}
}

func TestDiscoverActiveReposNeverWalksTheWorkspace(t *testing.T) {
	now := time.Now()
	repos := make([]fakeRepo, 0, 500)
	for i := 0; i < 500; i++ {
		repos = append(repos, fakeRepo{
			slug:      fmt.Sprintf("repo-%03d", i),
			updatedOn: now.Add(-time.Duration(i) * 24 * time.Hour),
		})
	}
	f := newDiscoveryFake(t, repos, reposPageLen)

	got, err := f.client().DiscoverActiveRepos(context.Background(), "ws", 14)
	if err != nil {
		t.Fatalf("DiscoverActiveRepos: %v", err)
	}
	if len(got) != 14 {
		t.Fatalf("active repos = %d, want 14", len(got))
	}
	if calls := f.callCount(); calls != 1 {
		t.Fatalf("discovery pages = %d, want 1", calls)
	}
}

func TestDiscoverActiveReposIsNotCached(t *testing.T) {
	f := newDiscoveryFake(t, []fakeRepo{{slug: "repo-a", updatedOn: time.Now().Add(-time.Hour)}}, 100)
	client := f.client()

	if _, err := client.DiscoverActiveRepos(context.Background(), "ws", 14); err != nil {
		t.Fatalf("first discovery: %v", err)
	}
	if _, err := client.DiscoverActiveRepos(context.Background(), "ws", 14); err != nil {
		t.Fatalf("second discovery: %v", err)
	}
	if calls := f.callCount(); calls != 2 {
		t.Fatalf("discovery calls = %d, want 2 (no cache)", calls)
	}
}

func TestDiscoverActiveReposRejectsEmptyWorkspace(t *testing.T) {
	client := New(Options{BaseURL: "http://127.0.0.1:1", Email: "me@example.com", Token: "tok", Logger: discardLogger()})
	if _, err := client.DiscoverActiveRepos(context.Background(), "", 14); err == nil {
		t.Fatal("expected an error for an empty workspace")
	}
}

func TestReviewPRsQueryPaginationAndMapping(t *testing.T) {
	var queries []string
	var srvURL string

	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		if r.URL.Query().Get("page") == "2" {
			writeJSON(t, w, 200, map[string]any{
				"values": []any{prJSON(3, "third", "2026-01-05T09:00:00+00:00")},
				"next":   "",
			})
			return
		}
		writeJSON(t, w, 200, map[string]any{
			"values": []any{
				prJSON(1, "first", "2026-01-03T09:00:00+00:00"),
				prJSON(2, "second", "2026-01-04T09:00:00+00:00"),
			},
			"next": srvURL + "/2.0/repositories/ws/repo-a/pullrequests?page=2",
		})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/commit/{hash}/statuses", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("ReviewPRs must not fetch commit statuses")
		writeJSON(t, w, 500, map[string]any{"error": map[string]any{"message": "unexpected"}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL

	client := New(Options{BaseURL: srv.URL, Email: "me@example.com", Token: "tok", Logger: discardLogger()})
	prs, err := client.ReviewPRs(context.Background(), Repo{Workspace: "ws", Repo: "repo-a"}, "OPEN")
	if err != nil {
		t.Fatalf("ReviewPRs: %v", err)
	}
	if len(prs) != 3 {
		t.Fatalf("prs = %d, want 3", len(prs))
	}
	if queries[0] == "" {
		t.Fatal("no query recorded")
	}
	parsed := mustParseQuery(t, queries[0])
	if got, want := parsed.Get("q"), `reviewers.uuid="uuid-me"`; got != want {
		t.Fatalf("q = %q, want %q", got, want)
	}
	if got := parsed.Get("state"); got != "OPEN" {
		t.Fatalf("state = %q", got)
	}
	if got := parsed.Get("pagelen"); got != "50" {
		t.Fatalf("pagelen = %q", got)
	}
	if got := parsed.Get("fields"); got != "+values.participants" {
		t.Fatalf("fields = %q", got)
	}

	wantOrder := []int{3, 2, 1}
	for i, want := range wantOrder {
		if prs[i].ID != want {
			t.Fatalf("order[%d] = %d, want %d", i, prs[i].ID, want)
		}
	}
	if prs[2].MyState != ReviewApproved || prs[2].Approvals != 1 {
		t.Fatalf("my_state/approvals = %q/%d", prs[2].MyState, prs[2].Approvals)
	}
	if prs[2].BuildState != BuildNone {
		t.Fatalf("build_state = %q, want NONE (not fetched by the scanner path)", prs[2].BuildState)
	}
}

func TestReviewPRsPropagatesRateLimit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		writeJSON(t, w, 429, map[string]any{"error": map[string]any{"message": "slow down"}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Options{BaseURL: srv.URL, Email: "me@example.com", Token: "tok", Logger: discardLogger()})
	_, err := client.ReviewPRs(context.Background(), Repo{Workspace: "ws", Repo: "repo-a"}, "OPEN")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !IsRateLimited(err) {
		t.Fatalf("IsRateLimited = false for %v", err)
	}
	if got := RetryAfter(err); got != 7*time.Second {
		t.Fatalf("RetryAfter = %v, want 7s", got)
	}
}

func TestAuthorPRsUsesWorkspaceEndpoint(t *testing.T) {
	var escapedPath, query, selectedUser string
	var repoCalls int

	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"uuid":"{1234-abcd}","display_name":"Me"}`))
	})
	mux.HandleFunc("GET /2.0/workspaces/ws/pullrequests/{user}", func(w http.ResponseWriter, r *http.Request) {
		escapedPath = r.URL.EscapedPath()
		selectedUser = r.PathValue("user")
		query = r.URL.RawQuery
		writeJSON(t, w, 200, map[string]any{
			"values": []any{
				map[string]any{
					"id": 11, "title": "authored", "state": "OPEN",
					"updated_on":  "2026-01-02T09:00:00+00:00",
					"destination": map[string]any{"repository": map[string]any{"full_name": "ws/from-full-name"}},
					"source":      map[string]any{"branch": map[string]any{"name": "feat"}},
				},
				map[string]any{
					"id": 12, "title": "fallback", "state": "OPEN",
					"updated_on": "2026-01-03T09:00:00+00:00",
					"links":      map[string]any{"html": map[string]any{"href": "https://bitbucket.org/ws/from-html/pull-requests/12"}},
				},
			},
			"next": "",
		})
	})
	mux.HandleFunc("GET /2.0/repositories/", func(w http.ResponseWriter, r *http.Request) {
		repoCalls++
		writeJSON(t, w, 500, map[string]any{"error": map[string]any{"message": "should not be called"}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Options{BaseURL: srv.URL, Email: "me@example.com", Token: "tok", Logger: discardLogger()})
	prs, err := client.AuthorPRs(context.Background(), "ws", "OPEN")
	if err != nil {
		t.Fatalf("AuthorPRs: %v", err)
	}
	if repoCalls != 0 {
		t.Fatalf("author role must not fan out per repo (calls = %d)", repoCalls)
	}
	if want := "/2.0/workspaces/ws/pullrequests/%7B1234-abcd%7D"; escapedPath != want {
		t.Fatalf("path = %q, want %q", escapedPath, want)
	}
	if selectedUser != "{1234-abcd}" {
		t.Fatalf("selected user = %q, want the decoded uuid", selectedUser)
	}

	parsed := mustParseQuery(t, query)
	if got := parsed.Get("state"); got != "OPEN" {
		t.Fatalf("state = %q", got)
	}
	if got := parsed.Get("pagelen"); got != "50" {
		t.Fatalf("pagelen = %q", got)
	}
	if got := parsed.Get("fields"); got != "+values.participants" {
		t.Fatalf("fields = %q", got)
	}
	if got := parsed.Get("q"); got != "" {
		t.Fatalf("author listing must not send a q filter, got %q", got)
	}

	if len(prs) != 2 {
		t.Fatalf("prs = %d, want 2", len(prs))
	}
	byID := make(map[int]PRSummary, len(prs))
	for _, pr := range prs {
		byID[pr.ID] = pr
	}
	if got := byID[11].Repo; got != "from-full-name" {
		t.Fatalf("repo from destination.repository = %q", got)
	}
	if got := byID[12].Repo; got != "from-html" {
		t.Fatalf("repo from html link = %q", got)
	}
	for id, pr := range byID {
		if pr.Workspace != "ws" {
			t.Fatalf("pr %d workspace = %q", id, pr.Workspace)
		}
	}
}
