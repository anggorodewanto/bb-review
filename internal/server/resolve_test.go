package server

import (
	"net/http"
	"net/url"
	"testing"
)

func TestParsePRURL(t *testing.T) {
	valid := []struct {
		name string
		url  string
		ws   string
		repo string
		id   int
	}{
		{name: "plain", url: "https://bitbucket.org/ws/repo/pull-requests/12", ws: "ws", repo: "repo", id: 12},
		{name: "sub path", url: "https://bitbucket.org/ws/repo/pull-requests/12/overview", ws: "ws", repo: "repo", id: 12},
		{name: "diff fragment", url: "https://bitbucket.org/ws/repo/pull-requests/12/diff#L10", ws: "ws", repo: "repo", id: 12},
		{name: "query", url: "https://bitbucket.org/ws/repo/pull-requests/12?tab=files", ws: "ws", repo: "repo", id: 12},
		{name: "trailing slash", url: "https://bitbucket.org/ws/repo/pull-requests/7/", ws: "ws", repo: "repo", id: 7},
		{name: "http and www", url: "http://www.bitbucket.org/ws/repo/pull-requests/7", ws: "ws", repo: "repo", id: 7},
		{name: "uppercase host", url: "https://BITBUCKET.ORG/ws/repo/pull-requests/7", ws: "ws", repo: "repo", id: 7},
		{name: "surrounding whitespace", url: "  https://bitbucket.org/ws/repo/pull-requests/7  ", ws: "ws", repo: "repo", id: 7},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			repo, id, err := parsePRURL(tc.url)
			if err != nil {
				t.Fatalf("parsePRURL(%q): %v", tc.url, err)
			}
			if repo.Workspace != tc.ws || repo.Repo != tc.repo || id != tc.id {
				t.Fatalf("parsePRURL(%q) = %+v/%d, want %s/%s/%d", tc.url, repo, id, tc.ws, tc.repo, tc.id)
			}
		})
	}

	invalid := []string{
		"",
		"   ",
		"bitbucket.org/ws/repo/pull-requests/1",
		"https://github.com/ws/repo/pull-requests/1",
		"https://api.bitbucket.org/ws/repo/pull-requests/1",
		"ftp://bitbucket.org/ws/repo/pull-requests/1",
		"https://bitbucket.org/ws/repo/issues/1",
		"https://bitbucket.org/ws/repo/pull-requests/abc",
		"https://bitbucket.org/ws/repo/pull-requests/0",
		"https://bitbucket.org/ws/repo/pull-requests/-3",
		"https://bitbucket.org/ws/pull-requests/1",
		"https://bitbucket.org/",
		"not a url at all",
	}
	for _, raw := range invalid {
		t.Run("invalid "+raw, func(t *testing.T) {
			if _, _, err := parsePRURL(raw); err == nil {
				t.Fatalf("parsePRURL(%q) should fail", raw)
			}
		})
	}
}

func TestResolveEndpoint(t *testing.T) {
	env := newTestEnv(t, nil)

	rec := env.do(t, http.MethodGet, "/api/resolve?url="+url.QueryEscape("https://bitbucket.org/ws/repo/pull-requests/42"), nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	body := decodeBody[resolveBody](t, rec)
	if body.Workspace != "ws" || body.Repo != "repo" || body.ID != 42 {
		t.Fatalf("resolve = %+v", body)
	}

	rec = env.do(t, http.MethodGet, "/api/resolve?url="+url.QueryEscape("https://example.com/ws/repo/pull-requests/42"), nil)
	if rec.Code != 400 {
		t.Fatalf("bad url status = %d, want 400", rec.Code)
	}
	errBody := decodeBody[errorBody](t, rec)
	if errBody.Error == "" || errBody.Status != 0 {
		t.Fatalf("bad url body = %+v", errBody)
	}

	rec = env.do(t, http.MethodGet, "/api/resolve", nil)
	if rec.Code != 400 {
		t.Fatalf("missing url status = %d, want 400", rec.Code)
	}
}
