package bitbucket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultBaseURL is the Bitbucket Cloud REST 2.0 API root.
	DefaultBaseURL = "https://api.bitbucket.org"
	// DefaultTimeout bounds a single upstream HTTP call.
	DefaultTimeout = 30 * time.Second
	// DefaultConcurrency bounds concurrent upstream calls per batch.
	DefaultConcurrency = 8

	listCacheTTL    = 15 * time.Second
	statusCacheTTL  = 15 * time.Second
	cacheSoftLimit  = 512
	maxPages        = 100
	maxErrorSnippet = 200
)

// Options configure a Client.
type Options struct {
	// BaseURL is the API root, e.g. https://api.bitbucket.org. Injectable for tests.
	BaseURL string
	// Email is the Atlassian account email used for HTTP Basic auth.
	Email string
	// Token is the Atlassian API token. It is never logged or returned in errors.
	Token string
	// HTTPClient overrides the default client (30s timeout).
	HTTPClient *http.Client
	// Logger receives non-fatal warnings about partial failures.
	Logger *slog.Logger
}

// Client is a Bitbucket Cloud REST 2.0 client. It is safe for concurrent use.
type Client struct {
	baseURL string
	email   string
	token   string
	hc      *http.Client
	log     *slog.Logger

	meMu sync.Mutex
	me   *User

	cacheMu     sync.Mutex
	listCache   map[string]listEntry
	statusCache map[string]statusEntry
}

type listEntry struct {
	at  time.Time
	prs []PRSummary
}

type statusEntry struct {
	at     time.Time
	builds []BuildStatus
}

// New returns a Client. A missing BaseURL falls back to DefaultBaseURL.
func New(opts Options) *Client {
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: DefaultTimeout}
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	return &Client{
		baseURL:     baseURL,
		email:       opts.Email,
		token:       opts.Token,
		hc:          hc,
		log:         logger,
		listCache:   make(map[string]listEntry),
		statusCache: make(map[string]statusEntry),
	}
}

// Me returns the authenticated user and caches it for the process lifetime.
func (c *Client) Me(ctx context.Context) (User, error) {
	c.meMu.Lock()
	defer c.meMu.Unlock()
	if c.me != nil {
		return *c.me, nil
	}
	var u apiUser
	if err := c.getJSON(ctx, "/2.0/user", &u); err != nil {
		return User{}, err
	}
	me := u.toUser()
	c.me = &me
	return me, nil
}

// ListResult is the outcome of a cross-repo pull request listing.
type ListResult struct {
	PRs []PRSummary
	// Failed lists the repos that could not be fetched ("repo" or "ws/repo").
	Failed []string
}

// ListPRs returns open pull requests for the given repos, sorted by updated_on
// descending. Repos are fetched concurrently (limit DefaultConcurrency); a failing
// repo is reported in ListResult.Failed and does not fail the whole call.
//
// Results are cached for 15s; any write to a pull request busts the cache.
func (c *Client) ListPRs(ctx context.Context, repos []Repo, role, state string) (ListResult, error) {
	me, err := c.Me(ctx)
	if err != nil {
		return ListResult{}, err
	}
	if state == "" {
		state = "OPEN"
	}
	q := fmt.Sprintf(`reviewers.uuid="%s"`, me.UUID)
	if role == "author" {
		q = fmt.Sprintf(`author.uuid="%s"`, me.UUID)
	}

	key := listCacheKey(role, state, repos)
	if prs, ok := c.cachedList(key); ok {
		return ListResult{PRs: prs}, nil
	}

	lists := make([][]PRSummary, len(repos))
	hashes := make([][]string, len(repos))
	errs := make([]error, len(repos))
	runConcurrently(len(repos), func(i int) {
		prs, commits, err := c.listRepoPRs(ctx, repos[i], q, state, me.UUID)
		lists[i], hashes[i], errs[i] = prs, commits, err
	})

	result := ListResult{PRs: make([]PRSummary, 0, 16), Failed: []string{}}
	flatRepos := make([]Repo, 0, 16)
	flatHashes := make([]string, 0, 16)
	for i, repo := range repos {
		if errs[i] != nil {
			result.Failed = append(result.Failed, repoLabel(repos, repo))
			c.log.Warn("list pull requests failed", "repo", repo.String(), "err", errs[i].Error())
			continue
		}
		for j, pr := range lists[i] {
			result.PRs = append(result.PRs, pr)
			flatRepos = append(flatRepos, repo)
			flatHashes = append(flatHashes, hashes[i][j])
		}
	}

	c.fillBuildStates(ctx, flatRepos, flatHashes, result.PRs)
	sortPRs(result.PRs)

	if len(repos) > 0 && len(result.Failed) == len(repos) {
		for _, err := range errs {
			if err != nil {
				return result, err
			}
		}
	}
	if len(result.Failed) > 0 {
		return result, nil
	}
	c.storeList(key, result.PRs)
	return result, nil
}

// listRepoPRs fetches every page of one repo's pull request list. It returns the
// summaries plus each PR's source commit hash (empty when the API omitted it).
func (c *Client) listRepoPRs(ctx context.Context, repo Repo, q, state, meUUID string) ([]PRSummary, []string, error) {
	values := url.Values{}
	values.Set("state", state)
	values.Set("pagelen", "50")
	values.Set("q", q)
	values.Set("fields", "+values.participants")

	var prs []PRSummary
	var hashes []string
	next := fmt.Sprintf("%s/pullrequests?%s", repoPath(repo), values.Encode())
	for pageNo := 0; next != "" && pageNo < maxPages; pageNo++ {
		var payload page[apiPR]
		if err := c.getJSON(ctx, next, &payload); err != nil {
			return nil, nil, err
		}
		for _, a := range payload.Values {
			prs = append(prs, a.toSummary(repo, meUUID))
			hashes = append(hashes, a.Source.Commit.Hash)
		}
		next = payload.Next
	}
	if prs == nil {
		prs = []PRSummary{}
	}
	if hashes == nil {
		hashes = []string{}
	}
	return prs, hashes, nil
}

// fillBuildStates aggregates commit statuses onto each summary, concurrently.
func (c *Client) fillBuildStates(ctx context.Context, repos []Repo, hashes []string, prs []PRSummary) {
	states := make([]BuildState, len(prs))
	runConcurrently(len(prs), func(i int) {
		if hashes[i] == "" {
			return
		}
		builds, err := c.commitStatuses(ctx, repos[i], hashes[i])
		if err != nil {
			c.log.Warn("commit statuses failed", "repo", repos[i].String(), "commit", hashes[i], "err", err.Error())
			return
		}
		states[i] = aggregateBuilds(builds)
	})
	for i := range prs {
		if states[i] == "" {
			prs[i].BuildState = BuildNone
			continue
		}
		prs[i].BuildState = states[i]
	}
}

// PRDetail fetches a single pull request plus merge base and build statuses.
func (c *Client) PRDetail(ctx context.Context, repo Repo, id int) (PRDetail, error) {
	me, err := c.Me(ctx)
	if err != nil {
		return PRDetail{}, err
	}
	var a apiPR
	if err := c.getJSON(ctx, prPath(repo, id), &a); err != nil {
		return PRDetail{}, err
	}
	detail := a.toDetail(repo, me.UUID)

	if hash, err := c.mergeBase(ctx, repo, id); err == nil && hash != "" {
		detail.MergeBase = hash
	} else if err != nil {
		c.log.Debug("merge base lookup failed", "repo", repo.String(), "id", id, "err", err.Error())
	}

	builds, err := c.commitStatuses(ctx, repo, detail.SourceCommit)
	if err != nil {
		c.log.Warn("commit statuses failed", "repo", repo.String(), "commit", detail.SourceCommit, "err", err.Error())
		return detail, nil
	}
	detail.Builds = builds
	detail.BuildState = aggregateBuilds(builds)
	return detail, nil
}

func (c *Client) mergeBase(ctx context.Context, repo Repo, id int) (string, error) {
	var payload struct {
		Hash string `json:"hash"`
	}
	path := fmt.Sprintf("%s/merge-base", prPath(repo, id))
	if err := c.getJSON(ctx, path, &payload); err != nil {
		return "", err
	}
	return payload.Hash, nil
}

// commitStatuses returns all statuses for a commit, cached for 15s.
func (c *Client) commitStatuses(ctx context.Context, repo Repo, hash string) ([]BuildStatus, error) {
	if hash == "" {
		return []BuildStatus{}, nil
	}
	key := repo.String() + "@" + hash
	if builds, ok := c.cachedStatuses(key); ok {
		return builds, nil
	}

	path := fmt.Sprintf("%s/commit/%s/statuses", repoPath(repo), url.PathEscape(hash))
	values := url.Values{}
	values.Set("pagelen", "100")
	raw, err := getPaged[apiStatus](ctx, c, path+"?"+values.Encode())
	if err != nil {
		return nil, err
	}
	builds := make([]BuildStatus, 0, len(raw))
	for _, s := range raw {
		builds = append(builds, s.toBuildStatus())
	}
	c.storeStatuses(key, builds)
	return builds, nil
}

// ListComments returns every comment on a pull request, oldest first.
func (c *Client) ListComments(ctx context.Context, repo Repo, id int) ([]Comment, error) {
	values := url.Values{}
	values.Set("pagelen", "100")
	raw, err := getPaged[apiComment](ctx, c, prPath(repo, id)+"/comments?"+values.Encode())
	if err != nil {
		return nil, err
	}
	comments := make([]Comment, 0, len(raw))
	for _, a := range raw {
		comments = append(comments, a.toComment())
	}
	sort.SliceStable(comments, func(i, j int) bool {
		if comments[i].CreatedOn != comments[j].CreatedOn {
			return comments[i].CreatedOn < comments[j].CreatedOn
		}
		return comments[i].ID < comments[j].ID
	})
	return comments, nil
}

// PostComment posts a comment, optionally inline and/or as a reply.
func (c *Client) PostComment(ctx context.Context, repo Repo, id int, raw string, inline *InlineAnchor, parentID *int) (Comment, error) {
	var body apiCommentPost
	body.Content.Raw = raw
	body.Inline = inlineFromAnchor(inline)
	if parentID != nil {
		body.Parent = &apiParent{ID: *parentID}
	}

	var out apiComment
	if err := c.sendJSON(ctx, http.MethodPost, prPath(repo, id)+"/comments", body, &out); err != nil {
		return Comment{}, err
	}
	c.InvalidatePRCache()
	return out.toComment(), nil
}

// Approve adds (approve=true) or removes the current user's approval.
func (c *Client) Approve(ctx context.Context, repo Repo, id int, approve bool) error {
	return c.reviewAction(ctx, repo, id, "approve", approve)
}

// RequestChanges adds (request=true) or removes the current user's change request.
func (c *Client) RequestChanges(ctx context.Context, repo Repo, id int, request bool) error {
	return c.reviewAction(ctx, repo, id, "request-changes", request)
}

func (c *Client) reviewAction(ctx context.Context, repo Repo, id int, action string, add bool) error {
	method := http.MethodPost
	if !add {
		method = http.MethodDelete
	}
	err := c.sendJSON(ctx, method, prPath(repo, id)+"/"+action, nil, nil)
	c.InvalidatePRCache()
	if err == nil {
		return nil
	}
	// Removing a state that is not set is a no-op for us.
	if !add && IsNotFound(err) {
		return nil
	}
	return err
}

// Diff returns the unified diff stream for a pull request (redirects followed).
func (c *Client) Diff(ctx context.Context, repo Repo, id int) (io.ReadCloser, error) {
	return c.stream(ctx, prPath(repo, id)+"/diff", "text/plain")
}

// File returns the raw file content at a commit.
func (c *Client) File(ctx context.Context, repo Repo, commit, filePath string) (io.ReadCloser, error) {
	target := fmt.Sprintf("%s/src/%s/%s", repoPath(repo), url.PathEscape(commit), escapePath(filePath))
	return c.stream(ctx, target, "*/*")
}

// InvalidatePRCache drops cached pull request lists and commit statuses. Call it
// after any write that can change list contents (comment, approve, request changes).
func (c *Client) InvalidatePRCache() {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	c.listCache = make(map[string]listEntry)
	c.statusCache = make(map[string]statusEntry)
}

func (c *Client) cachedList(key string) ([]PRSummary, bool) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	entry, ok := c.listCache[key]
	if !ok || time.Since(entry.at) > listCacheTTL {
		return nil, false
	}
	return append([]PRSummary(nil), entry.prs...), true
}

func (c *Client) storeList(key string, prs []PRSummary) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	if len(c.listCache) > cacheSoftLimit {
		c.listCache = make(map[string]listEntry)
	}
	c.listCache[key] = listEntry{at: time.Now(), prs: append([]PRSummary(nil), prs...)}
}

func (c *Client) cachedStatuses(key string) ([]BuildStatus, bool) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	entry, ok := c.statusCache[key]
	if !ok || time.Since(entry.at) > statusCacheTTL {
		return nil, false
	}
	return append([]BuildStatus(nil), entry.builds...), true
}

func (c *Client) storeStatuses(key string, builds []BuildStatus) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	if len(c.statusCache) > cacheSoftLimit {
		c.statusCache = make(map[string]statusEntry)
	}
	c.statusCache[key] = statusEntry{at: time.Now(), builds: append([]BuildStatus(nil), builds...)}
}

func listCacheKey(role, state string, repos []Repo) string {
	parts := make([]string, 0, len(repos))
	for _, r := range repos {
		parts = append(parts, r.String())
	}
	return role + "|" + state + "|" + strings.Join(parts, ",")
}

// repoLabel names a repo in the X-BBR-Partial header: the bare slug when it lives in
// the first repo's workspace, "ws/repo" otherwise.
func repoLabel(repos []Repo, repo Repo) string {
	if len(repos) == 0 {
		return repo.Repo
	}
	if repo.Workspace == repos[0].Workspace {
		return repo.Repo
	}
	return repo.String()
}

func sortPRs(prs []PRSummary) {
	sort.SliceStable(prs, func(i, j int) bool {
		return newerFirst(prs[i].UpdatedOn, prs[j].UpdatedOn)
	})
}

// newerFirst reports whether timestamp a sorts before b (descending).
func newerFirst(a, b string) bool {
	ta, oka := parseTimestamp(a)
	tb, okb := parseTimestamp(b)
	switch {
	case oka && okb:
		return ta.After(tb)
	case oka != okb:
		return oka
	default:
		return a > b
	}
}

func parseTimestamp(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func repoPath(repo Repo) string {
	return fmt.Sprintf("/2.0/repositories/%s/%s", url.PathEscape(repo.Workspace), url.PathEscape(repo.Repo))
}

func prPath(repo Repo, id int) string {
	return repoPath(repo) + "/pullrequests/" + strconv.Itoa(id)
}

// escapePath percent-escapes each slash-separated segment of a file path.
func escapePath(p string) string {
	cleaned := strings.TrimPrefix(p, "/")
	segments := strings.Split(cleaned, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return strings.Join(segments, "/")
}

// runConcurrently runs fn(0..n-1) with at most DefaultConcurrency in flight.
func runConcurrently(n int, fn func(i int)) {
	if n <= 0 {
		return
	}
	sem := make(chan struct{}, DefaultConcurrency)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			fn(i)
		}(i)
	}
	wg.Wait()
}

// page is the Bitbucket pagination envelope.
type page[T any] struct {
	Values []T    `json:"values"`
	Next   string `json:"next"`
}

// getPaged follows `next` links until the list is exhausted.
func getPaged[T any](ctx context.Context, c *Client, target string) ([]T, error) {
	out := make([]T, 0, 32)
	for pageNo := 0; target != "" && pageNo < maxPages; pageNo++ {
		var payload page[T]
		if err := c.getJSON(ctx, target, &payload); err != nil {
			return nil, err
		}
		out = append(out, payload.Values...)
		target = payload.Next
	}
	return out, nil
}

func (c *Client) getJSON(ctx context.Context, target string, out any) error {
	return c.sendJSON(ctx, http.MethodGet, target, nil, out)
}

func (c *Client) sendJSON(ctx context.Context, method, target string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return &APIError{Status: 0, Message: "encode request: " + err.Error()}
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := c.newRequest(ctx, method, target, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return upstreamError(resp)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return &APIError{Status: resp.StatusCode, Message: "decode response: " + err.Error()}
	}
	return nil
}

// stream performs a GET for a non-JSON body (diff, file content).
func (c *Client) stream(ctx context.Context, target, accept string) (io.ReadCloser, error) {
	req, err := c.newRequest(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)

	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		return nil, upstreamError(resp)
	}
	return resp.Body, nil
}

func (c *Client) newRequest(ctx context.Context, method, target string, body io.Reader) (*http.Request, error) {
	u := target
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		u = c.baseURL + target
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, &APIError{Status: 0, Message: "build request: " + err.Error()}
	}
	req.SetBasicAuth(c.email, c.token)
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.hc.Do(req)
	if err != nil {
		if ctxErr := req.Context().Err(); ctxErr != nil {
			err = ctxErr
		}
		return nil, &APIError{Status: 0, Message: "request failed: " + err.Error()}
	}
	return resp, nil
}

// upstreamError maps a non-2xx Bitbucket response to *APIError. The body is read
// and closed by the caller; the API token never appears here.
func upstreamError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))

	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	message := ""
	if json.Unmarshal(body, &payload) == nil {
		message = strings.TrimSpace(payload.Error.Message)
	}
	if message == "" {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > maxErrorSnippet {
			snippet = snippet[:maxErrorSnippet] + "..."
		}
		message = snippet
	}
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	return &APIError{Status: resp.StatusCode, Message: message}
}
