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

	// DefaultActiveDays is the repo-discovery window in days.
	DefaultActiveDays = 14

	statusCacheTTL  = 15 * time.Second
	cacheSoftLimit  = 512
	maxPages        = 100
	maxErrorSnippet = 200
	reposPageLen    = 100
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
	// Now is the clock, injectable for tests (request accounting and cache TTLs).
	Now func() time.Time
}

// Client is a Bitbucket Cloud REST 2.0 client. It is safe for concurrent use.
type Client struct {
	baseURL string
	email   string
	token   string
	hc      *http.Client
	log     *slog.Logger
	now     func() time.Time

	meMu sync.Mutex
	me   *User

	cacheMu     sync.Mutex
	statusCache map[string]statusEntry

	// requests accounts for upstream HTTP traffic (rolling hour).
	requests *requestCounter
	// buildCache caches aggregate build states per source commit.
	buildCache *buildStateCache
	// responses caches PR detail, diff and comments for a short while.
	responses *responseCache
}

// listEntry is one cached PR list.
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
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Client{
		baseURL:     baseURL,
		email:       opts.Email,
		token:       opts.Token,
		hc:          hc,
		log:         logger,
		now:         now,
		statusCache: make(map[string]statusEntry),
		requests:    newRequestCounter(now),
		buildCache:  newBuildStateCache(now),
		responses:   newResponseCache(now),
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

// ActiveRepo is a repository inside the active discovery window.
type ActiveRepo struct {
	Repo      Repo
	UpdatedOn time.Time // zero when Bitbucket omitted the timestamp
}

// ReviewPRs returns the open pull requests of one repo where the current user is a
// reviewer, oldest-updated last (sorted by updated_on desc). my_state and approvals
// are filled in.
//
// Build states are intentionally not fetched here: that costs one request per PR,
// and the scanner (which calls this for every active repo) must stay rate-limit
// friendly. The list handlers fill them from the build-state cache, and the PR
// detail endpoint still returns real build states.
func (c *Client) ReviewPRs(ctx context.Context, repo Repo, state string) ([]PRSummary, error) {
	me, err := c.Me(ctx)
	if err != nil {
		return nil, err
	}
	if state == "" {
		state = "OPEN"
	}
	q := fmt.Sprintf(`reviewers.uuid="%s"`, me.UUID)
	prs, err := c.listRepoPRs(ctx, repo, q, state, me.UUID)
	if err != nil {
		return nil, err
	}
	sortPRs(prs)
	return prs, nil
}

// AuthorPRs lists pull requests from the workspace-wide endpoint (no per-repo
// fan-out) with build states filled in from the cache.
func (c *Client) AuthorPRs(ctx context.Context, workspace, state string) ([]PRSummary, error) {
	me, err := c.Me(ctx)
	if err != nil {
		return nil, err
	}
	if state == "" {
		state = "OPEN"
	}

	values := url.Values{}
	values.Set("state", state)
	values.Set("pagelen", "50")
	values.Set("fields", "+values.participants")
	target := fmt.Sprintf("/2.0/workspaces/%s/pullrequests/%s?%s",
		url.PathEscape(workspace), url.PathEscape(me.UUID), values.Encode())

	raw, err := getPaged[apiPR](ctx, c, target)
	if err != nil {
		return nil, err
	}
	fallback := Repo{Workspace: workspace}
	prs := make([]PRSummary, 0, len(raw))
	for _, a := range raw {
		repo := repoFromPR(a, fallback)
		if repo.Repo == "" {
			// Without a repository the row cannot be linked or opened.
			continue
		}
		prs = append(prs, a.toSummary(repo, me.UUID))
	}
	c.FillBuildStates(ctx, prs, DefaultBuildBudget)
	sortPRs(prs)
	return prs, nil
}

// listRepoPRs fetches every page of one repo's pull request list.
func (c *Client) listRepoPRs(ctx context.Context, repo Repo, q, state, meUUID string) ([]PRSummary, error) {
	// Bitbucket ignores the state parameter whenever q is present, so the state
	// filter must live inside q or merged/declined PRs come back too.
	values := url.Values{}
	values.Set("pagelen", "50")
	values.Set("q", withState(q, state))
	values.Set("fields", "+values.participants")

	var prs []PRSummary
	next := fmt.Sprintf("%s/pullrequests?%s", repoPath(repo), values.Encode())
	for pageNo := 0; next != "" && pageNo < maxPages; pageNo++ {
		var payload page[apiPR]
		if err := c.getJSON(ctx, next, &payload); err != nil {
			return nil, err
		}
		for _, a := range payload.Values {
			prs = append(prs, a.toSummary(repo, meUUID))
		}
		next = payload.Next
	}
	if prs == nil {
		prs = []PRSummary{}
	}
	return prs, nil
}

// withState adds a state="..." clause to a BBQL query.
func withState(q, state string) string {
	if state == "" {
		return q
	}
	clause := fmt.Sprintf(`state="%s"`, state)
	if q == "" {
		return clause
	}
	return q + " AND " + clause
}

// DiscoverActiveRepos lists the repos updated inside the active window, sorted by
// -updated_on. Paging stops at the first repo older than the cutoff, so it never
// walks the whole workspace (1-4 requests for a busy workspace).
//
// No caching: the scanner calls this every tick and needs fresh updated_on values.
func (c *Client) DiscoverActiveRepos(ctx context.Context, workspace string, activeDays int) ([]ActiveRepo, error) {
	if strings.TrimSpace(workspace) == "" {
		return nil, errors.New("workspace is required for repository discovery")
	}
	if activeDays <= 0 {
		activeDays = DefaultActiveDays
	}
	cutoff := time.Now().AddDate(0, 0, -activeDays)

	values := url.Values{}
	values.Set("sort", "-updated_on")
	values.Set("pagelen", strconv.Itoa(reposPageLen))
	values.Set("fields", "next,values.slug,values.updated_on")

	repos := make([]ActiveRepo, 0, reposPageLen)
	next := fmt.Sprintf("/2.0/repositories/%s?%s", url.PathEscape(workspace), values.Encode())
	for pageNo := 0; next != "" && pageNo < maxPages; pageNo++ {
		var payload page[apiRepo]
		if err := c.getJSON(ctx, next, &payload); err != nil {
			return nil, err
		}
		if len(payload.Values) == 0 {
			break
		}
		for _, item := range payload.Values {
			slug := strings.TrimSpace(item.Slug)
			if slug == "" {
				continue
			}
			if !item.UpdatedOn.IsZero() && item.UpdatedOn.Before(cutoff) {
				// Sorted by -updated_on: everything that follows is older too.
				return repos, nil
			}
			repos = append(repos, ActiveRepo{
				Repo:      Repo{Workspace: workspace, Repo: slug},
				UpdatedOn: item.UpdatedOn.Time,
			})
		}
		next = payload.Next
	}
	return repos, nil
}

// PRDetail fetches a single pull request plus merge base and build statuses.
// The pull request, its merge base and its commit statuses are fetched
// concurrently (statuses once the source commit is known), the result is cached
// for 30s, and the aggregate build state it learns feeds the list cache.
func (c *Client) PRDetail(ctx context.Context, repo Repo, id int) (PRDetail, error) {
	key := repo.PRKey(id)
	if detail, ok := c.responses.detail(key); ok {
		return detail, nil
	}
	me, err := c.Me(ctx)
	if err != nil {
		return PRDetail{}, err
	}

	// The merge base only needs ws/repo/id, so it runs alongside the PR fetch.
	mergeCh := make(chan mergeBaseResult, 1)
	go func() {
		hash, err := c.mergeBase(ctx, repo, id)
		mergeCh <- mergeBaseResult{hash: hash, err: err}
	}()

	var a apiPR
	if err := c.getJSON(ctx, prPath(repo, id), &a); err != nil {
		return PRDetail{}, err
	}
	detail := a.toDetail(repo, me.UUID)

	// Statuses need the source commit, so they start as soon as the PR is in.
	buildsCh := make(chan buildsResult, 1)
	go func() {
		builds, err := c.commitStatuses(ctx, repo, detail.SourceCommit)
		buildsCh <- buildsResult{builds: builds, err: err}
	}()

	merged := <-mergeCh
	switch {
	case merged.err != nil:
		c.log.Debug("merge base lookup failed", "repo", repo.String(), "id", id, "err", merged.err.Error())
	case merged.hash != "":
		detail.MergeBase = merged.hash
	}

	builds := <-buildsCh
	if detail.Builds == nil {
		detail.Builds = []BuildStatus{}
	}
	if builds.err != nil {
		c.log.Warn("commit statuses failed", "repo", repo.String(), "commit", detail.SourceCommit, "err", builds.err.Error())
		c.responses.storeDetail(key, detail)
		return detail, nil
	}
	if builds.builds != nil {
		detail.Builds = builds.builds
	}
	detail.BuildState = aggregateBuilds(builds.builds)
	c.recordBuildState(repo, detail.SourceCommit, detail.BuildState)
	c.responses.storeDetail(key, detail)
	return detail, nil
}

type mergeBaseResult struct {
	hash string
	err  error
}

type buildsResult struct {
	builds []BuildStatus
	err    error
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

// ListComments returns every comment on a pull request, oldest first. The list
// is cached for 30s and busted by any write on the pull request.
func (c *Client) ListComments(ctx context.Context, repo Repo, id int) ([]Comment, error) {
	key := repo.PRKey(id)
	if comments, ok := c.responses.comments(key); ok {
		return comments, nil
	}
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
	c.responses.storeComments(key, comments)
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
	c.InvalidateStatusCache()
	c.responses.invalidate(repo.PRKey(id))
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
	c.InvalidateStatusCache()
	c.responses.invalidate(repo.PRKey(id))
	if err == nil {
		return nil
	}
	// Removing a state that is not set is a no-op for us.
	if !add && IsNotFound(err) {
		return nil
	}
	return err
}

// Diff returns the unified diff of a pull request (redirects followed). The body
// is cached for 30s and dropped when the pull request's source commit changes.
func (c *Client) Diff(ctx context.Context, repo Repo, id int) ([]byte, error) {
	key := repo.PRKey(id)
	if body, ok := c.responses.diff(key); ok {
		return body, nil
	}
	stream, err := c.stream(ctx, prPath(repo, id)+"/diff", "text/plain")
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	body, err := io.ReadAll(stream)
	if err != nil {
		return nil, &APIError{Status: 0, Message: "read diff: " + err.Error()}
	}
	c.responses.storeDiff(key, body)
	return body, nil
}

// File returns the raw file content at a commit.
func (c *Client) File(ctx context.Context, repo Repo, commit, filePath string) (io.ReadCloser, error) {
	target := fmt.Sprintf("%s/src/%s/%s", repoPath(repo), url.PathEscape(commit), escapePath(filePath))
	return c.stream(ctx, target, "*/*")
}

// InvalidateStatusCache drops cached commit statuses and aggregate build states,
// so a write (comment, approve, request changes) is followed by fresh build data
// on the next detail read.
func (c *Client) InvalidateStatusCache() {
	c.cacheMu.Lock()
	c.statusCache = make(map[string]statusEntry)
	c.cacheMu.Unlock()
	c.buildCache.clear()
}

// RequestsLastHour returns the upstream HTTP requests made in the last 60
// minutes. Bitbucket Cloud sends no rate-limit headers, so this is the client's
// own account of its traffic.
func (c *Client) RequestsLastHour() int {
	return c.requests.lastHour()
}

// RetryAfter returns the delay requested by an upstream 429 (0 when absent).
func RetryAfter(err error) time.Duration {
	if apiErr, ok := AsAPIError(err); ok {
		return apiErr.RetryAfter
	}
	return 0
}

func (c *Client) cachedStatuses(key string) ([]BuildStatus, bool) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	entry, ok := c.statusCache[key]
	if !ok || c.now().Sub(entry.at) > statusCacheTTL {
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
	c.statusCache[key] = statusEntry{at: c.now(), builds: append([]BuildStatus(nil), builds...)}
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
	c.requests.record()
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
	return &APIError{
		Status:     resp.StatusCode,
		Message:    message,
		RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
	}
}

// parseRetryAfter understands both Retry-After forms: delta-seconds and HTTP-date.
func parseRetryAfter(value string) time.Duration {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(trimmed); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(trimmed); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}
