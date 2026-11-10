package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/anggorodewanto/bb-review/internal/bitbucket"
	"github.com/anggorodewanto/bb-review/internal/config"
	"github.com/anggorodewanto/bb-review/internal/store"
)

const (
	meJSON   = `{"uuid":"uuid-me","account_id":"acc-me","display_name":"Me Myself","nickname":"me","links":{"avatar":{"href":"https://example.com/me.png"}}}`
	testHost = "127.0.0.1:7777"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// testEnv wires a Server against a fake Bitbucket upstream.
type testEnv struct {
	handler http.Handler
	store   *store.Store
	cfg     *config.Config
}

func newTestEnv(t *testing.T, upstream http.Handler) *testEnv {
	t.Helper()
	env := newTestEnvWithConfig(t, upstream, &config.Config{
		Email:        "me@example.com",
		Token:        "tok",
		Workspace:    "ws",
		Repos:        []string{"repo-a"},
		AllowedHosts: []string{"box.tail1234.ts.net"},
		Listen:       testHost,
	})
	return env
}

func newTestEnvWithConfig(t *testing.T, upstream http.Handler, cfg *config.Config) *testEnv {
	t.Helper()
	if upstream == nil {
		upstream = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected upstream call: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		})
	}
	bbSrv := httptest.NewServer(upstream)
	t.Cleanup(bbSrv.Close)

	st, err := store.Open(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	client := bitbucket.New(bitbucket.Options{
		BaseURL: bbSrv.URL,
		Email:   cfg.Email,
		Token:   cfg.Token,
		Logger:  discardLogger(),
	})
	srv := New(Options{Config: cfg, Client: client, Store: st, Logger: discardLogger()})
	return &testEnv{handler: srv.Handler(), store: st, cfg: cfg}
}

// do sends a request through the full middleware chain.
func (e *testEnv) do(t *testing.T, method, target string, body any, mutators ...func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
		reader = &buf
	}
	req := httptest.NewRequest(method, target, reader)
	req.Host = testHost
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if !isSafeMethod(method) {
		req.Header.Set("X-BBR", "1")
	}
	for _, mutate := range mutators {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return out
}

func upstreamJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encode upstream response: %v", err)
	}
}

func prDetailUpstream(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests/7", func(w http.ResponseWriter, r *http.Request) {
		upstreamJSON(t, w, 200, map[string]any{
			"id": 7, "title": "Detail PR", "state": "OPEN",
			"description": "hello",
			"created_on":  "2026-01-01T09:00:00+00:00",
			"updated_on":  "2026-01-02T09:00:00+00:00",
			"author":      map[string]any{"uuid": "uuid-author", "display_name": "Ann"},
			"participants": []any{
				map[string]any{"user": map[string]any{"uuid": "uuid-me"}, "role": "REVIEWER", "state": "changes_requested"},
				map[string]any{"user": map[string]any{"uuid": "uuid-x"}, "role": "REVIEWER", "approved": true},
			},
			"source":      map[string]any{"branch": map[string]any{"name": "feat"}, "commit": map[string]any{"hash": "src111"}},
			"destination": map[string]any{"branch": map[string]any{"name": "main"}, "commit": map[string]any{"hash": "dst222"}},
			"links":       map[string]any{"html": map[string]any{"href": "https://bitbucket.org/ws/repo-a/pull-requests/7"}},
		})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests/7/merge-base", func(w http.ResponseWriter, r *http.Request) {
		upstreamJSON(t, w, 200, map[string]any{"hash": "base000"})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/commit/src111/statuses", func(w http.ResponseWriter, r *http.Request) {
		upstreamJSON(t, w, 200, map[string]any{
			"values": []any{
				map[string]any{"key": "ci", "name": "CI", "state": "SUCCESSFUL", "url": "https://ci/1"},
				map[string]any{"key": "e2e", "name": "E2E", "state": "FAILED", "url": "https://ci/2"},
			},
		})
	})
	mux.HandleFunc("POST /2.0/repositories/ws/repo-a/pullrequests/7/approve", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func TestSecurityMiddleware(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		target     string
		host       string
		noXBBR     bool
		wantStatus int
	}{
		{name: "get ok", method: http.MethodGet, target: "/api/me", host: testHost, wantStatus: 200},
		{name: "localhost with port ok", method: http.MethodGet, target: "/api/me", host: "localhost:7777", wantStatus: 200},
		{name: "ipv6 loopback ok", method: http.MethodGet, target: "/api/me", host: "[::1]:7777", wantStatus: 200},
		{name: "tailnet host ok", method: http.MethodGet, target: "/api/me", host: "box.tail1234.ts.net:7777", wantStatus: 200},
		{name: "unknown host 421", method: http.MethodGet, target: "/api/me", host: "evil.example.com", wantStatus: 421},
		{name: "dns rebinding style host 421", method: http.MethodGet, target: "/api/me", host: "127.0.0.1.evil.com", wantStatus: 421},
		{name: "post without x-bbr 403", method: http.MethodPost, target: "/api/prs/ws/repo-a/7/approve", host: testHost, noXBBR: true, wantStatus: 403},
		{name: "delete without x-bbr 403", method: http.MethodDelete, target: "/api/prs/ws/repo-a/7/drafts/abc", host: testHost, noXBBR: true, wantStatus: 403},
		{name: "post with x-bbr passes", method: http.MethodPost, target: "/api/prs/ws/repo-a/7/approve", host: testHost, wantStatus: 200},
		{name: "options is never answered", method: http.MethodOptions, target: "/api/me", host: testHost, noXBBR: true, wantStatus: 403},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, prDetailUpstream(t))
			opts := []func(*http.Request){func(r *http.Request) { r.Host = tc.host }}
			if tc.noXBBR {
				opts = append(opts, func(r *http.Request) { r.Header.Del("X-BBR") })
			}
			rec := env.do(t, tc.method, tc.target, nil, opts...)
			if rec.Code != tc.wantStatus {
				t.Fatalf("%s %s -> %d, want %d (body %s)", tc.method, tc.target, rec.Code, tc.wantStatus, rec.Body.String())
			}
			if rec.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("server must never answer CORS")
			}
			if tc.wantStatus == 403 || tc.wantStatus == 421 {
				var body errorBody
				if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
					t.Fatalf("error body is not JSON: %v", err)
				}
				if body.Error == "" {
					t.Fatal("error message missing")
				}
				if body.Status != 0 {
					t.Fatalf("local error status = %d, want 0 per docs/API.md", body.Status)
				}
			}
		})
	}
}

func TestPRDetailMapping(t *testing.T) {
	env := newTestEnv(t, prDetailUpstream(t))
	rec := env.do(t, http.MethodGet, "/api/prs/ws/repo-a/7", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	detail := decodeBody[bitbucket.PRDetail](t, rec)

	if detail.MyState != bitbucket.ReviewChangesRequested {
		t.Fatalf("my_state = %q, want changes_requested", detail.MyState)
	}
	if detail.BuildState != bitbucket.BuildFailed {
		t.Fatalf("build_state = %q, want FAILED", detail.BuildState)
	}
	if detail.Approvals != 1 {
		t.Fatalf("approvals = %d, want 1", detail.Approvals)
	}
	if detail.SourceCommit != "src111" || detail.DestCommit != "dst222" || detail.MergeBase != "base000" {
		t.Fatalf("commits = %q %q %q", detail.SourceCommit, detail.DestCommit, detail.MergeBase)
	}
	if len(detail.Builds) != 2 || detail.Builds[1].State != bitbucket.BuildFailed {
		t.Fatalf("builds = %+v", detail.Builds)
	}
	if len(detail.Participants) != 2 {
		t.Fatalf("participants = %+v", detail.Participants)
	}
	if detail.Workspace != "ws" || detail.Repo != "repo-a" || detail.ID != 7 {
		t.Fatalf("identity = %+v", detail.PRSummary)
	}
	if detail.URL != "https://bitbucket.org/ws/repo-a/pull-requests/7" {
		t.Fatalf("url = %q", detail.URL)
	}
}

func TestPRDetailUpstreamError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests/404", func(w http.ResponseWriter, r *http.Request) {
		upstreamJSON(t, w, 404, map[string]any{"error": map[string]any{"message": "Pull request not found"}})
	})
	env := newTestEnv(t, mux)
	rec := env.do(t, http.MethodGet, "/api/prs/ws/repo-a/404", nil)
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	body := decodeBody[errorBody](t, rec)
	if body.Error != "Pull request not found" || body.Status != 404 {
		t.Fatalf("error body = %+v", body)
	}
}

func TestPRListPartialFailureSetsHeader(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		upstreamJSON(t, w, 200, map[string]any{"values": []any{map[string]any{
			"id": 1, "title": "ok", "state": "OPEN",
			"updated_on":  "2026-01-02T09:00:00+00:00",
			"author":      map[string]any{"uuid": "uuid-author"},
			"source":      map[string]any{"branch": map[string]any{"name": "feat"}},
			"destination": map[string]any{"branch": map[string]any{"name": "main"}},
		}}})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-b/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		upstreamJSON(t, w, 500, map[string]any{"error": map[string]any{"message": "repo b exploded"}})
	})
	cfg := &config.Config{
		Email: "me@example.com", Token: "tok", Workspace: "ws",
		Repos: []string{"repo-a", "repo-b"}, Listen: testHost,
	}
	env := newTestEnvWithConfig(t, mux, cfg)
	rec := env.do(t, http.MethodGet, "/api/prs?role=reviewer", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-BBR-Partial"); got != "repo-b" {
		t.Fatalf("X-BBR-Partial = %q, want repo-b", got)
	}
	prs := decodeBody[[]bitbucket.PRSummary](t, rec)
	if len(prs) != 1 {
		t.Fatalf("prs = %d, want 1", len(prs))
	}
}

func TestPRListAllReposFailReturnsUpstreamStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		upstreamJSON(t, w, 500, map[string]any{"error": map[string]any{"message": "all broken"}})
	})
	env := newTestEnv(t, mux)
	rec := env.do(t, http.MethodGet, "/api/prs", nil)
	if rec.Code != 500 {
		t.Fatalf("status = %d, want the upstream status 500", rec.Code)
	}
	body := decodeBody[errorBody](t, rec)
	if body.Error != "all broken" || body.Status != 500 {
		t.Fatalf("error body = %+v", body)
	}
}

func TestPRListRejectsUnknownRole(t *testing.T) {
	env := newTestEnv(t, nil)
	rec := env.do(t, http.MethodGet, "/api/prs?role=wat", nil)
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSubmitDraftsFlow(t *testing.T) {
	var mu sync.Mutex
	var postedRaws []string
	var approveCalls int
	var nextCommentID int

	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("POST /2.0/repositories/ws/repo-a/pullrequests/5/comments", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Content struct {
				Raw string `json:"raw"`
			} `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode comment body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		postedRaws = append(postedRaws, body.Content.Raw)
		if body.Content.Raw == "bad draft" {
			mu.Unlock()
			upstreamJSON(t, w, 422, map[string]any{"error": map[string]any{"message": "comment rejected"}})
			return
		}
		nextCommentID++
		id := nextCommentID
		mu.Unlock()
		upstreamJSON(t, w, 201, map[string]any{
			"id":      id,
			"content": map[string]any{"raw": body.Content.Raw, "html": "<p>" + body.Content.Raw + "</p>"},
		})
	})
	mux.HandleFunc("POST /2.0/repositories/ws/repo-a/pullrequests/5/approve", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		approveCalls++
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})

	env := newTestEnv(t, mux)
	key := "ws/repo-a/5"
	first, err := env.store.AddDraft(key, "first draft", nil, nil)
	if err != nil {
		t.Fatalf("AddDraft: %v", err)
	}
	bad, err := env.store.AddDraft(key, "bad draft", nil, nil)
	if err != nil {
		t.Fatalf("AddDraft: %v", err)
	}
	third, err := env.store.AddDraft(key, "third draft", nil, nil)
	if err != nil {
		t.Fatalf("AddDraft: %v", err)
	}

	rec := env.do(t, http.MethodPost, "/api/prs/ws/repo-a/5/drafts/submit", map[string]any{"action": "approve"})
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}

	var response submitResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode submit response: %v", err)
	}
	if len(response.Posted) != 2 {
		t.Fatalf("posted = %d, want 2", len(response.Posted))
	}
	if response.Posted[0].Raw != first.Raw || response.Posted[1].Raw != third.Raw {
		t.Fatalf("posted order = %q, %q", response.Posted[0].Raw, response.Posted[1].Raw)
	}
	if len(response.Failed) != 1 {
		t.Fatalf("failed = %+v, want 1", response.Failed)
	}
	if response.Failed[0].Draft.ID != bad.ID {
		t.Fatalf("failed draft = %q, want %q", response.Failed[0].Draft.ID, bad.ID)
	}
	if response.Failed[0].Error != "comment rejected" {
		t.Fatalf("failed error = %q", response.Failed[0].Error)
	}

	mu.Lock()
	gotPosted, gotApprovals := append([]string(nil), postedRaws...), approveCalls
	mu.Unlock()
	wantPosted := []string{first.Raw, bad.Raw, third.Raw}
	if fmt.Sprint(gotPosted) != fmt.Sprint(wantPosted) {
		t.Fatalf("upstream post order = %v, want %v", gotPosted, wantPosted)
	}
	if gotApprovals != 1 {
		t.Fatalf("approve calls = %d, want 1", gotApprovals)
	}

	remaining := env.store.Drafts(key)
	if len(remaining) != 1 || remaining[0].ID != bad.ID {
		t.Fatalf("remaining drafts = %+v, want only the failed one", remaining)
	}
}

func TestSubmitDraftsWithoutAction(t *testing.T) {
	var approveCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("POST /2.0/repositories/ws/repo-a/pullrequests/5/comments", func(w http.ResponseWriter, r *http.Request) {
		upstreamJSON(t, w, 201, map[string]any{"id": 1, "content": map[string]any{"raw": "x"}})
	})
	mux.HandleFunc("POST /2.0/repositories/ws/repo-a/pullrequests/5/approve", func(w http.ResponseWriter, r *http.Request) {
		approveCalls++
		w.WriteHeader(http.StatusNoContent)
	})
	env := newTestEnv(t, mux)
	if _, err := env.store.AddDraft("ws/repo-a/5", "x", nil, nil); err != nil {
		t.Fatalf("AddDraft: %v", err)
	}
	rec := env.do(t, http.MethodPost, "/api/prs/ws/repo-a/5/drafts/submit", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if approveCalls != 0 {
		t.Fatalf("approve called %d times, want 0", approveCalls)
	}
	if got := len(env.store.Drafts("ws/repo-a/5")); got != 0 {
		t.Fatalf("drafts left = %d, want 0", got)
	}
}

func TestSubmitDraftsRejectsBadAction(t *testing.T) {
	env := newTestEnv(t, nil)
	rec := env.do(t, http.MethodPost, "/api/prs/ws/repo-a/5/drafts/submit", map[string]any{"action": "merge"})
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestDraftCRUD(t *testing.T) {
	env := newTestEnv(t, nil)
	base := "/api/prs/ws/repo-a/5/drafts"

	created := env.do(t, http.MethodPost, base, map[string]any{
		"raw":    "looks good",
		"inline": map[string]any{"path": "src/a.go", "to": 4},
	})
	if created.Code != 200 {
		t.Fatalf("create status = %d, body %s", created.Code, created.Body.String())
	}
	draft := decodeBody[bitbucket.Draft](t, created)
	if draft.ID == "" || draft.Raw != "looks good" {
		t.Fatalf("draft = %+v", draft)
	}
	if draft.Inline == nil || draft.Inline.Line() != 4 {
		t.Fatalf("inline = %+v", draft.Inline)
	}

	list := env.do(t, http.MethodGet, base, nil)
	drafts := decodeBody[[]bitbucket.Draft](t, list)
	if len(drafts) != 1 {
		t.Fatalf("drafts = %d, want 1", len(drafts))
	}

	updated := env.do(t, http.MethodPut, base+"/"+draft.ID, map[string]any{"raw": "edited"})
	if updated.Code != 200 {
		t.Fatalf("update status = %d, body %s", updated.Code, updated.Body.String())
	}
	if got := decodeBody[bitbucket.Draft](t, updated); got.Raw != "edited" {
		t.Fatalf("updated raw = %q", got.Raw)
	}

	missing := env.do(t, http.MethodPut, base+"/doesnotexist", map[string]any{"raw": "edited"})
	if missing.Code != 404 {
		t.Fatalf("update missing status = %d, want 404", missing.Code)
	}

	deleted := env.do(t, http.MethodDelete, base+"/"+draft.ID, nil)
	if deleted.Code != 204 {
		t.Fatalf("delete status = %d, want 204", deleted.Code)
	}
	if body := deleted.Body.String(); body != "" {
		t.Fatalf("delete body = %q, want empty", body)
	}

	list = env.do(t, http.MethodGet, base, nil)
	if got := decodeBody[[]bitbucket.Draft](t, list); len(got) != 0 {
		t.Fatalf("drafts after delete = %+v", got)
	}
}

func TestCreateCommentValidation(t *testing.T) {
	env := newTestEnv(t, nil)
	base := "/api/prs/ws/repo-a/5/comments"

	tests := []struct {
		name string
		body map[string]any
	}{
		{name: "empty raw", body: map[string]any{"raw": "  "}},
		{name: "both anchors", body: map[string]any{"raw": "x", "inline": map[string]any{"path": "a.go", "from": 1, "to": 2}}},
		{name: "no line", body: map[string]any{"raw": "x", "inline": map[string]any{"path": "a.go"}}},
		{name: "no path", body: map[string]any{"raw": "x", "inline": map[string]any{"to": 3}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := env.do(t, http.MethodPost, base, tc.body)
			if rec.Code != 400 {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestCreateCommentPostsToUpstream(t *testing.T) {
	var gotBody map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("POST /2.0/repositories/ws/repo-a/pullrequests/5/comments", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode: %v", err)
		}
		upstreamJSON(t, w, 201, map[string]any{
			"id":      12,
			"content": map[string]any{"raw": "hi", "html": "<p>hi</p>"},
			"inline":  map[string]any{"path": "a.go", "from": 3},
		})
	})
	env := newTestEnv(t, mux)
	rec := env.do(t, http.MethodPost, "/api/prs/ws/repo-a/5/comments", map[string]any{
		"raw":    "hi",
		"inline": map[string]any{"path": "a.go", "from": 3},
	})
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	comment := decodeBody[bitbucket.Comment](t, rec)
	if comment.ID != 12 || comment.Inline == nil || comment.Inline.From == nil {
		t.Fatalf("comment = %+v", comment)
	}
	if _, ok := gotBody["parent"]; ok {
		t.Fatalf("parent should be omitted: %#v", gotBody)
	}
}

func TestViewedRoundTrip(t *testing.T) {
	env := newTestEnv(t, nil)
	base := "/api/prs/ws/repo-a/5/viewed"

	initial := env.do(t, http.MethodGet, base, nil)
	if got := decodeBody[bitbucket.ViewedState](t, initial); got.Files == nil || len(got.Files) != 0 {
		t.Fatalf("initial viewed = %+v", got)
	}

	put := env.do(t, http.MethodPut, base, map[string]any{"files": map[string]string{"src/a.go": "h1", "src/b.go": "h2"}})
	if put.Code != 200 {
		t.Fatalf("put status = %d, body %s", put.Code, put.Body.String())
	}

	got := env.do(t, http.MethodGet, base, nil)
	state := decodeBody[bitbucket.ViewedState](t, got)
	if len(state.Files) != 2 || state.Files["src/b.go"] != "h2" {
		t.Fatalf("viewed = %+v", state)
	}
}

func TestReviewedStateAfterApprove(t *testing.T) {
	var approved bool
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/user", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(meJSON)) })
	mux.HandleFunc("POST /2.0/repositories/ws/repo-a/pullrequests/7/approve", func(w http.ResponseWriter, r *http.Request) {
		approved = true
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /2.0/repositories/ws/repo-a/pullrequests/7/approve", func(w http.ResponseWriter, r *http.Request) {
		approved = false
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests/7", func(w http.ResponseWriter, r *http.Request) {
		state := ""
		if approved {
			state = "approved"
		}
		upstreamJSON(t, w, 200, map[string]any{
			"id": 7, "title": "t", "state": "OPEN",
			"participants": []any{map[string]any{"user": map[string]any{"uuid": "uuid-me"}, "approved": approved, "state": state}},
			"source":       map[string]any{"commit": map[string]any{"hash": ""}},
			"destination":  map[string]any{"commit": map[string]any{"hash": ""}},
		})
	})
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests/7/merge-base", func(w http.ResponseWriter, r *http.Request) {
		upstreamJSON(t, w, 200, map[string]any{"hash": "base"})
	})
	env := newTestEnv(t, mux)

	rec := env.do(t, http.MethodPost, "/api/prs/ws/repo-a/7/approve", nil)
	if rec.Code != 200 {
		t.Fatalf("approve status = %d, body %s", rec.Code, rec.Body.String())
	}
	body := decodeBody[reviewStateBody](t, rec)
	if body.MyState != bitbucket.ReviewApproved {
		t.Fatalf("my_state = %q, want approved", body.MyState)
	}

	rec = env.do(t, http.MethodDelete, "/api/prs/ws/repo-a/7/approve", nil)
	if rec.Code != 200 {
		t.Fatalf("unapprove status = %d, body %s", rec.Code, rec.Body.String())
	}
	if got := decodeBody[reviewStateBody](t, rec); got.MyState != bitbucket.ReviewNone {
		t.Fatalf("my_state = %q, want none", got.MyState)
	}
}

func TestFileEndpoint(t *testing.T) {
	t.Run("streams text", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /2.0/repositories/ws/repo-a/src/{commit}/{path...}", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("package main\n"))
		})
		env := newTestEnv(t, mux)
		rec := env.do(t, http.MethodGet, "/api/file/ws/repo-a/abc123/cmd/main.go", nil)
		if rec.Code != 200 {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
		}
		if got := rec.Body.String(); got != "package main\n" {
			t.Fatalf("body = %q", got)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Fatalf("content type = %q", ct)
		}
	})

	t.Run("413 for files over 2MB", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /2.0/repositories/ws/repo-a/src/{commit}/{path...}", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.Write(bytes.Repeat([]byte("a"), maxFileSize+1))
		})
		env := newTestEnv(t, mux)
		rec := env.do(t, http.MethodGet, "/api/file/ws/repo-a/abc123/big.txt", nil)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413", rec.Code)
		}
	})

	t.Run("404 from upstream", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /2.0/repositories/ws/repo-a/src/{commit}/{path...}", func(w http.ResponseWriter, r *http.Request) {
			upstreamJSON(t, w, 404, map[string]any{"error": map[string]any{"message": "File not found"}})
		})
		env := newTestEnv(t, mux)
		rec := env.do(t, http.MethodGet, "/api/file/ws/repo-a/abc123/nope.txt", nil)
		if rec.Code != 404 {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

func TestDiffStreamsText(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /2.0/repositories/ws/repo-a/pullrequests/7/diff", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("diff --git a/x b/x\n"))
	})
	env := newTestEnv(t, mux)
	rec := env.do(t, http.MethodGet, "/api/prs/ws/repo-a/7/diff", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "diff --git") {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("content type = %q", ct)
	}
}

func TestSPAPlaceholderWhenFrontendNotBuilt(t *testing.T) {
	env := newTestEnv(t, nil)
	rec := env.do(t, http.MethodGet, "/prs/anything", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "frontend not built") {
		t.Fatalf("body = %q", rec.Body.String())
	}

	rec = env.do(t, http.MethodGet, "/api/unknown", nil)
	if rec.Code != 404 {
		t.Fatalf("unknown api status = %d, want 404", rec.Code)
	}

	rec = env.do(t, http.MethodPost, "/prs/anything", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("post to spa status = %d, want 405", rec.Code)
	}
}

func TestSPAServesEmbeddedFiles(t *testing.T) {
	assets := fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<html>app</html>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log(1)")},
	}
	env := newTestEnvWithAssets(t, assets)

	rec := env.do(t, http.MethodGet, "/", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<html>app</html>") {
		t.Fatalf("index: status %d body %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("index cache-control = %q", got)
	}

	rec = env.do(t, http.MethodGet, "/assets/app.js", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "console.log") {
		t.Fatalf("asset: status %d body %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Fatalf("asset cache-control = %q", got)
	}

	rec = env.do(t, http.MethodGet, "/pull-requests/12/files", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<html>app</html>") {
		t.Fatalf("fallback: status %d body %q", rec.Code, rec.Body.String())
	}
}

func newTestEnvWithAssets(t *testing.T, assets fstest.MapFS) *testEnv {
	t.Helper()
	bbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected upstream call: %s", r.URL.Path)
	}))
	t.Cleanup(bbSrv.Close)
	cfg := &config.Config{
		Email: "me@example.com", Token: "tok", Workspace: "ws",
		Repos: []string{"repo-a"}, Listen: testHost,
	}
	st, err := store.Open(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	client := bitbucket.New(bitbucket.Options{BaseURL: bbSrv.URL, Email: cfg.Email, Token: cfg.Token, Logger: discardLogger()})
	srv := New(Options{Config: cfg, Client: client, Store: st, Logger: discardLogger(), Assets: assets})
	return &testEnv{handler: srv.Handler(), store: st, cfg: cfg}
}

func TestAPIBadIDReturns400(t *testing.T) {
	env := newTestEnv(t, nil)
	rec := env.do(t, http.MethodGet, "/api/prs/ws/repo-a/notanumber", nil)
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
