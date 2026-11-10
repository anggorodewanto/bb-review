package bitbucket

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"sync"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func testClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return New(Options{
		BaseURL: srv.URL,
		Email:   "me@example.com",
		Token:   "super-secret-token",
		Logger:  discardLogger(),
	})
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

const meJSON = `{"uuid":"uuid-me","account_id":"acc-me","display_name":"Me Myself","nickname":"me","links":{"avatar":{"href":"https://example.com/me.png"}}}`

func prJSON(id int, title, updatedOn string) map[string]any {
	return map[string]any{
		"id":            id,
		"title":         title,
		"description":   "Some *markdown*\n\nsecond para",
		"state":         "OPEN",
		"created_on":    "2026-01-01T09:00:00+00:00",
		"updated_on":    updatedOn,
		"comment_count": 3,
		"task_count":    1,
		"author": map[string]any{
			"uuid":         "uuid-author",
			"account_id":   "acc-author",
			"display_name": "Ann Author",
			"nickname":     "ann",
			"links":        map[string]any{"avatar": map[string]any{"href": "https://example.com/ann.png"}},
		},
		"participants": []map[string]any{
			{"user": map[string]any{"uuid": "uuid-me", "display_name": "Me Myself"}, "role": "REVIEWER", "state": "approved", "approved": true},
			{"user": map[string]any{"uuid": "uuid-other", "display_name": "Other"}, "role": "REVIEWER", "state": "changes_requested", "approved": false},
		},
		"source":      map[string]any{"branch": map[string]any{"name": "feat"}, "commit": map[string]any{"hash": "abc123"}},
		"destination": map[string]any{"branch": map[string]any{"name": "main"}, "commit": map[string]any{"hash": "def456"}},
		"links":       map[string]any{"html": map[string]any{"href": "https://bitbucket.org/ws/repo/pull-requests/1"}},
		"rendered":    map[string]any{"description": map[string]any{"html": "<p>Some <em>markdown</em></p>"}},
	}
}

func TestMeIsCachedForProcessLifetime(t *testing.T) {
	var calls int
	var mu sync.Mutex
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		if r.URL.Path != "/2.0/user" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if user, _, ok := r.BasicAuth(); !ok || user != "me@example.com" {
			t.Errorf("basic auth user = %q ok=%v", user, ok)
		}
		w.Write([]byte(meJSON))
	}))

	first, err := client.Me(context.Background())
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if first.UUID != "uuid-me" || first.AvatarURL != "https://example.com/me.png" {
		t.Fatalf("unexpected user: %+v", first)
	}
	if _, err := client.Me(context.Background()); err != nil {
		t.Fatalf("Me (cached): %v", err)
	}
	if calls != 1 {
		t.Fatalf("/user called %d times, want 1", calls)
	}
}

func TestListPRsQueryPaginationAndMapping(t *testing.T) {
	var queries []string
	var srvURL string

	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(meJSON))
	})
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
		writeJSON(t, w, 200, map[string]any{
			"values": []any{
				map[string]any{"key": "ci", "name": "CI", "state": "SUCCESSFUL", "url": "https://ci/1"},
				map[string]any{"key": "lint", "name": "Lint", "state": "FAILED", "url": "https://ci/2"},
			},
			"next": "",
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL

	client := New(Options{BaseURL: srv.URL, Email: "me@example.com", Token: "tok", Logger: discardLogger()})
	repos := []Repo{{Workspace: "ws", Repo: "repo-a"}}
	result, err := client.ListPRs(context.Background(), repos, "reviewer", "OPEN")
	if err != nil {
		t.Fatalf("ListPRs: %v", err)
	}
	if len(result.Failed) != 0 {
		t.Fatalf("unexpected failed repos: %v", result.Failed)
	}
	if len(result.PRs) != 3 {
		t.Fatalf("got %d PRs, want 3", len(result.PRs))
	}
	if len(queries) == 0 {
		t.Fatal("no pull request queries recorded")
	}

	// The q filter must be url-encoded, never a raw quote.
	rawQuery := queries[0]
	if strings.Contains(rawQuery, `"`) {
		t.Fatalf("raw query contains unescaped quote: %s", rawQuery)
	}
	if !strings.Contains(rawQuery, "%22") {
		t.Fatalf("raw query does not escape quotes: %s", rawQuery)
	}
	parsed := mustParseQuery(t, rawQuery)
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

	// Sorted by updated_on desc.
	gotOrder := []int{result.PRs[0].ID, result.PRs[1].ID, result.PRs[2].ID}
	wantOrder := []int{3, 2, 1}
	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			t.Fatalf("order = %v, want %v", gotOrder, wantOrder)
		}
	}

	first := result.PRs[2]
	if first.Workspace != "ws" || first.Repo != "repo-a" {
		t.Fatalf("workspace/repo = %q/%q", first.Workspace, first.Repo)
	}
	if first.MyState != ReviewApproved {
		t.Fatalf("my_state = %q, want approved", first.MyState)
	}
	if first.Approvals != 1 {
		t.Fatalf("approvals = %d, want 1", first.Approvals)
	}
	if first.BuildState != BuildFailed {
		t.Fatalf("build_state = %q, want FAILED", first.BuildState)
	}
	if first.SourceBranch != "feat" || first.DestBranch != "main" {
		t.Fatalf("branches = %q -> %q", first.SourceBranch, first.DestBranch)
	}
	if first.CommentCount != 3 || first.TaskCount != 1 {
		t.Fatalf("counts = %d/%d", first.CommentCount, first.TaskCount)
	}
}

func mustParseQuery(t *testing.T, raw string) neturl.Values {
	t.Helper()
	values, err := neturl.ParseQuery(raw)
	if err != nil {
		t.Fatalf("parse query %q: %v", raw, err)
	}
	return values
}

func TestListPRsUsesAuthorFilterForAuthorRole(t *testing.T) {
	var gotQ string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		gotQ = r.URL.Query().Get("q")
		writeJSON(t, w, 200, map[string]any{"values": []any{}, "next": ""})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Options{BaseURL: srv.URL, Email: "me@example.com", Token: "tok", Logger: discardLogger()})
	if _, err := client.ListPRs(context.Background(), []Repo{{Workspace: "ws", Repo: "repo-a"}}, "author", "OPEN"); err != nil {
		t.Fatalf("ListPRs: %v", err)
	}
	if want := `author.uuid="uuid-me"`; gotQ != want {
		t.Fatalf("q = %q, want %q", gotQ, want)
	}
}

func TestListPRsPartialFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 200, map[string]any{"values": []any{prJSON(1, "ok", "2026-01-03T09:00:00+00:00")}, "next": ""})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/commit/{hash}/statuses", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 200, map[string]any{"values": []any{}, "next": ""})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-b/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 403, map[string]any{"error": map[string]any{"message": "no access"}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Options{BaseURL: srv.URL, Email: "me@example.com", Token: "tok", Logger: discardLogger()})
	repos := []Repo{{Workspace: "ws", Repo: "repo-a"}, {Workspace: "ws", Repo: "repo-b"}}
	result, err := client.ListPRs(context.Background(), repos, "reviewer", "OPEN")
	if err != nil {
		t.Fatalf("ListPRs should tolerate a partial failure: %v", err)
	}
	if len(result.PRs) != 1 {
		t.Fatalf("got %d PRs, want 1", len(result.PRs))
	}
	if len(result.Failed) != 1 || result.Failed[0] != "repo-b" {
		t.Fatalf("failed = %v, want [repo-b]", result.Failed)
	}
}

func TestListPRsFailsWhenEveryRepoFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 500, map[string]any{"error": map[string]any{"message": "boom"}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Options{BaseURL: srv.URL, Email: "me@example.com", Token: "tok", Logger: discardLogger()})
	_, err := client.ListPRs(context.Background(), []Repo{{Workspace: "ws", Repo: "repo-a"}}, "reviewer", "OPEN")
	if err == nil {
		t.Fatal("expected an error when every repo fails")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %v, want upstream message", err)
	}
}

func TestListPRsCachedAndBustedByWrites(t *testing.T) {
	var listCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		listCalls++
		writeJSON(t, w, 200, map[string]any{"values": []any{}, "next": ""})
	})
	mux.HandleFunc("POST /2.0/repositories/ws/repo-a/pullrequests/1/comments", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 200, map[string]any{"id": 10, "content": map[string]any{"raw": "hi", "html": "<p>hi</p>"}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Options{BaseURL: srv.URL, Email: "me@example.com", Token: "tok", Logger: discardLogger()})
	repos := []Repo{{Workspace: "ws", Repo: "repo-a"}}
	for i := 0; i < 3; i++ {
		if _, err := client.ListPRs(context.Background(), repos, "reviewer", "OPEN"); err != nil {
			t.Fatalf("ListPRs: %v", err)
		}
	}
	if listCalls != 1 {
		t.Fatalf("list called %d times, want 1 (cached)", listCalls)
	}

	if _, err := client.PostComment(context.Background(), repos[0], 1, "hi", nil, nil); err != nil {
		t.Fatalf("PostComment: %v", err)
	}
	if _, err := client.ListPRs(context.Background(), repos, "reviewer", "OPEN"); err != nil {
		t.Fatalf("ListPRs after write: %v", err)
	}
	if listCalls != 2 {
		t.Fatalf("list called %d times, want 2 (cache busted)", listCalls)
	}
}

func TestErrorMapping(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 401, map[string]any{"error": map[string]any{"message": "Invalid credentials"}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Options{BaseURL: srv.URL, Email: "me@example.com", Token: "super-secret-token", Logger: discardLogger()})
	_, err := client.Me(context.Background())
	apiErr, ok := AsAPIError(err)
	if !ok {
		t.Fatalf("error is not *APIError: %v", err)
	}
	if apiErr.Status != 401 || apiErr.Message != "Invalid credentials" {
		t.Fatalf("api error = %+v", apiErr)
	}
	if strings.Contains(err.Error(), "super-secret-token") {
		t.Fatalf("error leaks the token: %v", err)
	}
}

func TestErrorMappingNonJSONBody(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/repositories/ws/missing/pullrequests/1", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("plain text failure"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Options{BaseURL: srv.URL, Email: "me@example.com", Token: "tok", Logger: discardLogger()})
	_, err := client.PRDetail(context.Background(), Repo{Workspace: "ws", Repo: "missing"}, 1)
	if !IsNotFound(err) {
		t.Fatalf("expected a 404 APIError, got %v", err)
	}
	if !strings.Contains(err.Error(), "plain text failure") {
		t.Fatalf("error = %v, want body snippet", err)
	}
}

func TestPostCommentBodyShape(t *testing.T) {
	type request struct {
		method string
		path   string
		body   map[string]any
	}
	var got request
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("POST /2.0/repositories/ws/repo-a/pullrequests/3/comments", func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&got.body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		writeJSON(t, w, 200, map[string]any{
			"id":      99,
			"content": map[string]any{"raw": "hello", "html": "<p>hello</p>"},
			"inline":  map[string]any{"path": "a.go", "to": 12},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Options{BaseURL: srv.URL, Email: "me@example.com", Token: "tok", Logger: discardLogger()})
	line := 12
	parent := 7
	comment, err := client.PostComment(context.Background(), Repo{Workspace: "ws", Repo: "repo-a"}, 3,
		"hello", &InlineAnchor{Path: "a.go", To: &line}, &parent)
	if err != nil {
		t.Fatalf("PostComment: %v", err)
	}
	if comment.ID != 99 || comment.Raw != "hello" || comment.Inline == nil || comment.Inline.Line() != 12 {
		t.Fatalf("comment = %+v", comment)
	}
	if got.method != http.MethodPost {
		t.Fatalf("method = %q", got.method)
	}
	if got.path != "/2.0/repositories/ws/repo-a/pullrequests/3/comments" {
		t.Fatalf("path = %q", got.path)
	}

	content, ok := got.body["content"].(map[string]any)
	if !ok || content["raw"] != "hello" {
		t.Fatalf("content = %#v", got.body["content"])
	}
	inline, ok := got.body["inline"].(map[string]any)
	if !ok {
		t.Fatalf("inline missing: %#v", got.body)
	}
	if inline["path"] != "a.go" {
		t.Fatalf("inline.path = %v", inline["path"])
	}
	if to, ok := inline["to"].(float64); !ok || int(to) != 12 {
		t.Fatalf("inline.to = %v", inline["to"])
	}
	if _, present := inline["from"]; present {
		t.Fatalf("inline.from must be omitted for a to-anchor: %#v", inline)
	}
	parentBody, ok := got.body["parent"].(map[string]any)
	if !ok {
		t.Fatalf("parent missing: %#v", got.body)
	}
	if id, ok := parentBody["id"].(float64); !ok || int(id) != 7 {
		t.Fatalf("parent.id = %v", parentBody["id"])
	}
}

func TestPostCommentFromAnchorOmitsTo(t *testing.T) {
	var body map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("POST /2.0/repositories/ws/repo-a/pullrequests/3/comments", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		writeJSON(t, w, 200, map[string]any{"id": 1, "content": map[string]any{"raw": "x"}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Options{BaseURL: srv.URL, Email: "me@example.com", Token: "tok", Logger: discardLogger()})
	line := 4
	if _, err := client.PostComment(context.Background(), Repo{Workspace: "ws", Repo: "repo-a"}, 3,
		"x", &InlineAnchor{Path: "b.go", From: &line}, nil); err != nil {
		t.Fatalf("PostComment: %v", err)
	}
	inline := body["inline"].(map[string]any)
	if from, ok := inline["from"].(float64); !ok || int(from) != 4 {
		t.Fatalf("inline.from = %v", inline["from"])
	}
	if _, present := inline["to"]; present {
		t.Fatalf("inline.to must be omitted for a from-anchor: %#v", inline)
	}
	if _, present := body["parent"]; present {
		t.Fatalf("parent must be omitted when nil: %#v", body)
	}
}

func TestListCommentsPaginationAndMapping(t *testing.T) {
	var mu sync.Mutex
	var pages int
	var srvURL string

	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests/2/comments", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		pages++
		mu.Unlock()
		if r.URL.Query().Get("page") != "2" && r.URL.Query().Get("pagelen") != "100" {
			t.Errorf("pagelen = %q, want 100", r.URL.Query().Get("pagelen"))
		}
		if r.URL.Query().Get("page") == "2" {
			writeJSON(t, w, 200, map[string]any{
				"values": []any{map[string]any{
					"id":         8,
					"content":    map[string]any{"raw": "later", "html": "<p>later</p>"},
					"created_on": "2026-01-02T10:00:00+00:00", "updated_on": "2026-01-02T10:00:00+00:00",
				}},
				"next": "",
			})
			return
		}
		writeJSON(t, w, 200, map[string]any{
			"values": []any{
				map[string]any{
					"id":         5,
					"parent":     map[string]any{"id": 1},
					"user":       map[string]any{"uuid": "uuid-other", "display_name": "Other"},
					"content":    map[string]any{"raw": "a reply", "html": "<p>a reply</p>"},
					"created_on": "2026-01-01T10:00:00+00:00", "updated_on": "2026-01-01T11:00:00+00:00",
					"inline":   map[string]any{"path": "src/a.go", "from": 3},
					"outdated": true,
				},
			},
			"next": srvURL + "/2.0/repositories/ws/repo-a/pullrequests/2/comments?page=2",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL

	client := New(Options{BaseURL: srv.URL, Email: "me@example.com", Token: "tok", Logger: discardLogger()})
	comments, err := client.ListComments(context.Background(), Repo{Workspace: "ws", Repo: "repo-a"}, 2)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if pages != 2 {
		t.Fatalf("pages fetched = %d, want 2", pages)
	}
	if len(comments) != 2 {
		t.Fatalf("got %d comments, want 2", len(comments))
	}
	if comments[0].ID != 5 || comments[1].ID != 8 {
		t.Fatalf("comments not oldest-first: %d, %d", comments[0].ID, comments[1].ID)
	}
	first := comments[0]
	if first.ParentID == nil || *first.ParentID != 1 {
		t.Fatalf("parent_id = %v", first.ParentID)
	}
	if first.Inline == nil || first.Inline.Path != "src/a.go" || first.Inline.From == nil || *first.Inline.From != 3 {
		t.Fatalf("inline = %+v", first.Inline)
	}
	if !first.Outdated {
		t.Fatal("outdated not mapped")
	}
	if first.Author.DisplayName != "Other" {
		t.Fatalf("author = %+v", first.Author)
	}
}

func TestPRDetailMapsMergeBaseAndBuilds(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests/1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 200, prJSON(1, "first", "2026-01-03T09:00:00+00:00"))
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests/1/merge-base", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 200, map[string]any{"hash": "base999"})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/commit/abc123/statuses", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 200, map[string]any{
			"values": []any{
				map[string]any{"key": "ci", "name": "CI", "state": "SUCCESSFUL", "url": "https://ci/1"},
				map[string]any{"key": "deploy", "name": "Deploy", "state": "INPROGRESS", "url": "https://ci/2"},
			},
			"next": "",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Options{BaseURL: srv.URL, Email: "me@example.com", Token: "tok", Logger: discardLogger()})
	detail, err := client.PRDetail(context.Background(), Repo{Workspace: "ws", Repo: "repo-a"}, 1)
	if err != nil {
		t.Fatalf("PRDetail: %v", err)
	}
	if detail.MergeBase != "base999" {
		t.Fatalf("merge_base = %q", detail.MergeBase)
	}
	if detail.SourceCommit != "abc123" || detail.DestCommit != "def456" {
		t.Fatalf("commits = %q / %q", detail.SourceCommit, detail.DestCommit)
	}
	if detail.BuildState != BuildInProgress {
		t.Fatalf("build_state = %q, want INPROGRESS", detail.BuildState)
	}
	if len(detail.Builds) != 2 || detail.Builds[1].Name != "Deploy" {
		t.Fatalf("builds = %+v", detail.Builds)
	}
	if detail.DescriptionHTML != "<p>Some <em>markdown</em></p>" {
		t.Fatalf("description_html = %q", detail.DescriptionHTML)
	}
	if detail.DescriptionRaw == "" {
		t.Fatal("description_raw is empty")
	}
	if len(detail.Participants) != 2 {
		t.Fatalf("participants = %d", len(detail.Participants))
	}
	if detail.Participants[0].State != ReviewApproved {
		t.Fatalf("participant state = %q", detail.Participants[0].State)
	}
	if detail.Participants[1].State != ReviewChangesRequested {
		t.Fatalf("participant state = %q", detail.Participants[1].State)
	}
}

func TestPRDetailFallsBackWhenMergeBaseMissing(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests/1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 200, prJSON(1, "first", "2026-01-03T09:00:00+00:00"))
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests/1/merge-base", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 404, map[string]any{"error": map[string]any{"message": "not found"}})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/commit/abc123/statuses", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, 200, map[string]any{"values": []any{}, "next": ""})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Options{BaseURL: srv.URL, Email: "me@example.com", Token: "tok", Logger: discardLogger()})
	detail, err := client.PRDetail(context.Background(), Repo{Workspace: "ws", Repo: "repo-a"}, 1)
	if err != nil {
		t.Fatalf("PRDetail: %v", err)
	}
	if detail.MergeBase != "def456" {
		t.Fatalf("merge_base = %q, want destination commit fallback", detail.MergeBase)
	}
	if detail.BuildState != BuildNone {
		t.Fatalf("build_state = %q, want NONE", detail.BuildState)
	}
}

func TestReviewActions(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("/2.0/repositories/ws/repo-a/pullrequests/4/approve", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls["approve "+r.Method]++
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/2.0/repositories/ws/repo-a/pullrequests/4/request-changes", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls["request-changes "+r.Method]++
		mu.Unlock()
		if r.Method == http.MethodDelete {
			writeJSON(t, w, 404, map[string]any{"error": map[string]any{"message": "no changes requested"}})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Options{BaseURL: srv.URL, Email: "me@example.com", Token: "tok", Logger: discardLogger()})
	repo := Repo{Workspace: "ws", Repo: "repo-a"}
	if err := client.Approve(context.Background(), repo, 4, true); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := client.Approve(context.Background(), repo, 4, false); err != nil {
		t.Fatalf("Unapprove: %v", err)
	}
	if err := client.RequestChanges(context.Background(), repo, 4, true); err != nil {
		t.Fatalf("RequestChanges: %v", err)
	}
	// A 404 on withdraw is treated as a no-op.
	if err := client.RequestChanges(context.Background(), repo, 4, false); err != nil {
		t.Fatalf("withdraw changes: %v", err)
	}

	want := map[string]int{
		"approve POST":           1,
		"approve DELETE":         1,
		"request-changes POST":   1,
		"request-changes DELETE": 1,
	}
	for key, n := range want {
		if calls[key] != n {
			t.Fatalf("%s calls = %d, want %d", key, calls[key], n)
		}
	}
}

func TestFileEscapesPathSegments(t *testing.T) {
	var escapedPath string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/src/{commit}/{path...}", func(w http.ResponseWriter, r *http.Request) {
		escapedPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("package main\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New(Options{BaseURL: srv.URL, Email: "me@example.com", Token: "tok", Logger: discardLogger()})
	body, err := client.File(context.Background(), Repo{Workspace: "ws", Repo: "repo-a"}, "abc", "dir/my file.go")
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	defer body.Close()
	data, _ := io.ReadAll(body)
	if string(data) != "package main\n" {
		t.Fatalf("body = %q", data)
	}
	if !strings.Contains(escapedPath, "my%20file.go") {
		t.Fatalf("escaped path = %q, want %q", escapedPath, "my%20file.go")
	}
}

func TestAggregateBuildStates(t *testing.T) {
	tests := []struct {
		name   string
		states []string
		want   BuildState
	}{
		{name: "empty", states: nil, want: BuildNone},
		{name: "all successful", states: []string{"SUCCESSFUL", "SUCCESSFUL"}, want: BuildSuccessful},
		{name: "any failed wins", states: []string{"SUCCESSFUL", "FAILED", "INPROGRESS"}, want: BuildFailed},
		{name: "in progress beats success", states: []string{"SUCCESSFUL", "INPROGRESS"}, want: BuildInProgress},
		{name: "stopped is the fallback", states: []string{"STOPPED", "SUCCESSFUL"}, want: BuildStopped},
		{name: "unknown state falls back to stopped", states: []string{"WHATEVER"}, want: BuildStopped},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := AggregateBuildStates(tc.states); got != tc.want {
				t.Fatalf("AggregateBuildStates(%v) = %q, want %q", tc.states, got, tc.want)
			}
		})
	}
}

func TestReviewStateMapping(t *testing.T) {
	tests := []struct {
		name string
		in   apiParticipant
		want ReviewState
	}{
		{name: "approved flag", in: apiParticipant{Approved: true, State: "changes_requested"}, want: ReviewApproved},
		{name: "changes requested", in: apiParticipant{State: "changes_requested"}, want: ReviewChangesRequested},
		{name: "no state", in: apiParticipant{}, want: ReviewNone},
		{name: "unknown state", in: apiParticipant{State: "something"}, want: ReviewNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := reviewStateOf(tc.in); got != tc.want {
				t.Fatalf("reviewStateOf(%+v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	participants := []apiParticipant{
		{User: apiUser{UUID: "uuid-other"}, Approved: true},
		{User: apiUser{UUID: "uuid-me"}, State: "changes_requested"},
	}
	state, approvals := reviewSummary(participants, "uuid-me")
	if state != ReviewChangesRequested {
		t.Fatalf("state = %q, want changes_requested", state)
	}
	if approvals != 1 {
		t.Fatalf("approvals = %d, want 1", approvals)
	}
}
