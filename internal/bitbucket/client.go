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

	// DefaultDiscoveryTTL is how long the discovered repository list is cached.
	DefaultDiscoveryTTL = 10 * time.Minute
	// DefaultStaleAfter is how long a cached PR list is served without revalidating.
	DefaultStaleAfter = 60 * time.Second
	// DefaultActiveDays is the repo-discovery window in days.
	DefaultActiveDays = 14
	// defaultRateLimitBackoff applies to a 429 without a usable Retry-After.
	defaultRateLimitBackoff = 60 * time.Second
	// refreshRetryDelay throttles background refreshes after a failure.
	refreshRetryDelay = 30 * time.Second
	// refreshTimeout bounds one background refresh.
	refreshTimeout = 60 * time.Second

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
	// DiscoveryTTL caches the discovered repository list (default 10m).
	DiscoveryTTL time.Duration
	// StaleAfter is how long a cached PR list is served before revalidating
	// in the background (default 60s).
	StaleAfter time.Duration
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

	discoveryTTL time.Duration
	staleAfter   time.Duration

	cacheMu     sync.Mutex
	listCache   map[string]listEntry
	statusCache map[string]statusEntry
	discovery   map[string]discoveryEntry

	rateLimitedUntil time.Time
}

// listEntry is one cached PR list.
type listEntry struct {
	hasData     bool
	fetchedAt   time.Time
	prs         []PRSummary
	failed      []string
	rateLimited bool
	refreshing  bool
	retryAt     time.Time
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
	discoveryTTL := opts.DiscoveryTTL
	if discoveryTTL <= 0 {
		discoveryTTL = DefaultDiscoveryTTL
	}
	staleAfter := opts.StaleAfter
	if staleAfter <= 0 {
		staleAfter = DefaultStaleAfter
	}
	return &Client{
		baseURL:      baseURL,
		email:        opts.Email,
		token:        opts.Token,
		hc:           hc,
		log:          logger,
		discoveryTTL: discoveryTTL,
		staleAfter:   staleAfter,
		listCache:    make(map[string]listEntry),
		statusCache:  make(map[string]statusEntry),
		discovery:    make(map[string]discoveryEntry),
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

// ListOptions selects which pull requests to list.
type ListOptions struct {
	// Role is "reviewer" (per-repo fan-out over discovered + configured repos) or
	// "author" (one workspace-wide call, no per-repo fan-out).
	Role string
	// State is the pull request state filter; empty means OPEN.
	State string
	// Workspace is the workspace slug used for discovery and author lookups.
	Workspace string
	// Repos are the configured "always check" repos, unioned with discovery.
	Repos []Repo
	// ActiveDays is the repo-discovery window in days; 0 means DefaultActiveDays.
	ActiveDays int
}

// ListResult is the outcome of a cross-repo pull request listing.
type ListResult struct {
	PRs []PRSummary
	// Failed lists the repos that could not be fetched ("repo" or "ws/repo").
	Failed []string
	// FetchedAt is when the upstream data behind PRs was fetched. Cached copies
	// keep the original timestamp so the UI can show its age.
	FetchedAt time.Time
	// RateLimited reports that Bitbucket answered 429; PRs may be partial or stale.
	RateLimited bool
}

// ListPRs returns pull requests for the given role, sorted by updated_on
// descending.
//
// Caching is stale-while-revalidate: a copy younger than StaleAfter is returned
// as-is; an older copy is returned immediately and refreshed in the background
// by a single-flight goroutine. Only the first request (no cache yet) blocks.
func (c *Client) ListPRs(ctx context.Context, opts ListOptions) (ListResult, error) {
	if opts.State == "" {
		opts.State = "OPEN"
	}
	key := listCacheKey(opts)
	now := time.Now()

	c.cacheMu.Lock()
	entry := c.listCache[key]
	hasData := entry.hasData
	if hasData && now.Sub(entry.fetchedAt) <= c.staleAfter {
		result := entry.result()
		c.cacheMu.Unlock()
		return result, nil
	}
	backingOff := now.Before(c.rateLimitedUntil)
	if hasData {
		result := entry.result()
		result.RateLimited = result.RateLimited || backingOff
		shouldRefresh := !entry.refreshing && !backingOff && !now.Before(entry.retryAt)
		if shouldRefresh {
			entry.refreshing = true
			c.listCache[key] = entry
		}
		c.cacheMu.Unlock()
		if shouldRefresh {
			go c.refreshList(key, opts)
		}
		return result, nil
	}
	if backingOff {
		c.cacheMu.Unlock()
		return ListResult{PRs: []PRSummary{}, Failed: []string{}, FetchedAt: now, RateLimited: true}, nil
	}
	c.cacheMu.Unlock()

	result, err := c.fetchList(ctx, opts)
	c.finishSyncFetch(key, result, err)
	if err != nil {
		return ListResult{}, err
	}
	return result, nil
}

// fetchList performs the upstream calls for one list request.
func (c *Client) fetchList(ctx context.Context, opts ListOptions) (ListResult, error) {
	started := time.Now()
	me, err := c.Me(ctx)
	if err != nil {
		return ListResult{}, err
	}

	if opts.Role == "author" {
		prs, err := c.authorPRs(ctx, opts, me.UUID)
		if err != nil {
			if !IsRateLimited(err) {
				return ListResult{}, err
			}
			c.startBackoff(retryAfterOf(err))
			return ListResult{PRs: []PRSummary{}, Failed: []string{}, FetchedAt: time.Now(), RateLimited: true}, nil
		}
		c.log.Info("pr list",
			"role", "author",
			"workspace", opts.Workspace,
			"repos_discovered", 0,
			"repos_queried", 0,
			"prs", len(prs),
			"duration_ms", time.Since(started).Milliseconds(),
		)
		return ListResult{PRs: prs, Failed: []string{}, FetchedAt: time.Now()}, nil
	}

	activeDays := opts.ActiveDays
	if activeDays <= 0 {
		activeDays = DefaultActiveDays
	}
	discovered, discoveryErr := c.discoverRepos(ctx, opts.Workspace, activeDays)
	if discoveryErr != nil {
		c.log.Warn("repository discovery failed", "workspace", opts.Workspace, "err", discoveryErr.Error())
	}
	repos := mergeRepos(opts.Repos, discovered)
	if len(repos) == 0 {
		if discoveryErr != nil {
			return ListResult{}, discoveryErr
		}
		return ListResult{PRs: []PRSummary{}, Failed: []string{}, FetchedAt: time.Now()}, nil
	}

	fan := c.fanOut(ctx, repos, opts.State, me.UUID)
	if !fan.rateLimited && len(fan.failed) == len(repos) && fan.firstErr != nil {
		// Every repo failed in the same way: surface the upstream error.
		return ListResult{}, fan.firstErr
	}
	c.log.Info("pr list",
		"role", "reviewer",
		"workspace", opts.Workspace,
		"repos_discovered", len(discovered),
		"repos_queried", len(repos),
		"failed", len(fan.failed),
		"rate_limited", fan.rateLimited,
		"prs", len(fan.prs),
		"duration_ms", time.Since(started).Milliseconds(),
	)
	return ListResult{
		PRs:         fan.prs,
		Failed:      fan.failed,
		FetchedAt:   time.Now(),
		RateLimited: fan.rateLimited,
	}, nil
}

// authorPRs uses the workspace-wide endpoint instead of fanning out per repo.
func (c *Client) authorPRs(ctx context.Context, opts ListOptions, meUUID string) ([]PRSummary, error) {
	values := url.Values{}
	values.Set("state", opts.State)
	values.Set("pagelen", "50")
	values.Set("fields", "+values.participants")
	target := fmt.Sprintf("/2.0/workspaces/%s/pullrequests/%s?%s",
		url.PathEscape(opts.Workspace), url.PathEscape(meUUID), values.Encode())

	raw, err := getPaged[apiPR](ctx, c, target)
	if err != nil {
		return nil, err
	}
	fallback := Repo{Workspace: opts.Workspace}
	prs := make([]PRSummary, 0, len(raw))
	repos := make([]Repo, 0, len(raw))
	hashes := make([]string, 0, len(raw))
	for _, a := range raw {
		repo := repoFromPR(a, fallback)
		if repo.Repo == "" {
			// Without a repository the row cannot be linked or opened.
			continue
		}
		prs = append(prs, a.toSummary(repo, meUUID))
		repos = append(repos, repo)
		hashes = append(hashes, a.Source.Commit.Hash)
	}
	c.fillBuildStates(ctx, repos, hashes, prs)
	sortPRs(prs)
	return prs, nil
}

// fanOutResult is the aggregate of the per-repo reviewer queries.
type fanOutResult struct {
	prs         []PRSummary
	failed      []string
	rateLimited bool
	retryAfter  time.Duration
	firstErr    error
}

// fanOut queries every repo concurrently (limit DefaultConcurrency). A 429 stops
// the fan-out, keeps what already arrived and starts a backoff.
func (c *Client) fanOut(ctx context.Context, repos []Repo, state, meUUID string) fanOutResult {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	q := fmt.Sprintf(`reviewers.uuid="%s"`, meUUID)
	lists := make([][]PRSummary, len(repos))
	hashes := make([][]string, len(repos))
	errs := make([]error, len(repos))

	var mu sync.Mutex
	rateLimited := false
	var retryAfter time.Duration

	runConcurrently(len(repos), func(i int) {
		if ctx.Err() != nil {
			errs[i] = ctx.Err()
			return
		}
		prs, commits, err := c.listRepoPRs(ctx, repos[i], q, state, meUUID)
		if err == nil {
			lists[i], hashes[i] = prs, commits
			return
		}
		errs[i] = err
		if !IsRateLimited(err) {
			return
		}
		mu.Lock()
		rateLimited = true
		if apiErr, ok := AsAPIError(err); ok && apiErr.RetryAfter > retryAfter {
			retryAfter = apiErr.RetryAfter
		}
		mu.Unlock()
		cancel()
	})

	out := fanOutResult{
		prs:         make([]PRSummary, 0, 32),
		failed:      []string{},
		rateLimited: rateLimited,
		retryAfter:  retryAfter,
	}
	flatRepos := make([]Repo, 0, 16)
	flatHashes := make([]string, 0, 16)
	for i, repo := range repos {
		err := errs[i]
		abandoned := errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
		if err != nil && !abandoned && !IsRateLimited(err) {
			out.failed = append(out.failed, repoLabel(repos, repo))
			if out.firstErr == nil {
				out.firstErr = err
			}
			c.log.Warn("list pull requests failed", "repo", repo.String(), "err", err.Error())
		}
		for j, pr := range lists[i] {
			out.prs = append(out.prs, pr)
			flatRepos = append(flatRepos, repo)
			flatHashes = append(flatHashes, hashes[i][j])
		}
	}

	if rateLimited {
		c.startBackoff(retryAfter)
	}
	if out.rateLimited {
		return out
	}
	c.fillBuildStates(ctx, flatRepos, flatHashes, out.prs)
	sortPRs(out.prs)
	return out
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

// discoveryEntry is the cached result of a workspace repository listing.
type discoveryEntry struct {
	at         time.Time
	activeDays int
	repos      []Repo
}

// discoverRepos lists recently active repos in a workspace, cached for DiscoveryTTL.
// Paging stops as soon as a repo is older than the active window (the listing is
// sorted by -updated_on), so it never walks the whole workspace.
func (c *Client) discoverRepos(ctx context.Context, workspace string, activeDays int) ([]Repo, error) {
	if strings.TrimSpace(workspace) == "" {
		return nil, errors.New("workspace is required for repository discovery")
	}
	if activeDays <= 0 {
		activeDays = DefaultActiveDays
	}

	c.cacheMu.Lock()
	entry, ok := c.discovery[workspace]
	c.cacheMu.Unlock()
	if ok && entry.activeDays == activeDays && time.Since(entry.at) < c.discoveryTTL {
		return append([]Repo(nil), entry.repos...), nil
	}

	repos, err := c.fetchWorkspaceRepos(ctx, workspace, activeDays)
	if err != nil {
		return nil, err
	}
	c.log.Debug("repository discovery", "workspace", workspace, "repos", len(repos), "active_days", activeDays)

	c.cacheMu.Lock()
	if len(c.discovery) > cacheSoftLimit {
		c.discovery = make(map[string]discoveryEntry)
	}
	c.discovery[workspace] = discoveryEntry{
		at:         time.Now(),
		activeDays: activeDays,
		repos:      append([]Repo(nil), repos...),
	}
	c.cacheMu.Unlock()
	return repos, nil
}

// fetchWorkspaceRepos pages through the workspace repository listing.
func (c *Client) fetchWorkspaceRepos(ctx context.Context, workspace string, activeDays int) ([]Repo, error) {
	cutoff := time.Now().AddDate(0, 0, -activeDays)

	values := url.Values{}
	values.Set("sort", "-updated_on")
	values.Set("pagelen", strconv.Itoa(reposPageLen))
	values.Set("fields", "next,values.slug,values.updated_on")

	repos := make([]Repo, 0, 64)
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
			repos = append(repos, Repo{Workspace: workspace, Repo: slug})
		}
		next = payload.Next
	}
	return repos, nil
}

// mergeRepos unions configured and discovered repos, de-duplicated, config first.
func mergeRepos(configured, discovered []Repo) []Repo {
	out := make([]Repo, 0, len(configured)+len(discovered))
	seen := make(map[string]struct{}, len(configured)+len(discovered))
	add := func(repo Repo) {
		if repo.Workspace == "" || repo.Repo == "" {
			return
		}
		key := strings.ToLower(repo.String())
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, repo)
	}
	for _, repo := range configured {
		add(repo)
	}
	for _, repo := range discovered {
		add(repo)
	}
	return out
}

// fillBuildStates aggregates commit statuses onto each summary, concurrently.
func (c *Client) fillBuildStates(ctx context.Context, repos []Repo, hashes []string, prs []PRSummary) {
	if len(prs) == 0 || len(prs) != len(repos) || len(prs) != len(hashes) {
		return
	}
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

// result converts a cache entry into a ListResult with copied slices.
func (e listEntry) result() ListResult {
	fetchedAt := e.fetchedAt
	if fetchedAt.IsZero() {
		fetchedAt = time.Now()
	}
	return ListResult{
		PRs:         append([]PRSummary(nil), e.prs...),
		Failed:      append([]string(nil), e.failed...),
		FetchedAt:   fetchedAt,
		RateLimited: e.rateLimited,
	}
}

// finishSyncFetch stores the result of a blocking fetch, or drops the placeholder.
func (c *Client) finishSyncFetch(key string, result ListResult, err error) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	if err != nil {
		delete(c.listCache, key)
		return
	}
	c.storeEntryLocked(key, result)
}

// refreshList revalidates one list entry in the background (single-flight).
func (c *Client) refreshList(key string, opts ListOptions) {
	ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
	defer cancel()

	result, err := c.fetchList(ctx, opts)

	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	entry := c.listCache[key]
	entry.refreshing = false
	if err != nil {
		entry.retryAt = time.Now().Add(refreshRetryDelay)
		c.listCache[key] = entry
		c.log.Warn("pr list refresh failed", "role", opts.Role, "err", err.Error())
		return
	}
	c.storeEntryLocked(key, result)
}

// storeEntryLocked writes a cache entry. Callers must hold c.cacheMu.
func (c *Client) storeEntryLocked(key string, result ListResult) {
	if len(c.listCache) > cacheSoftLimit {
		c.listCache = make(map[string]listEntry)
	}
	fetchedAt := result.FetchedAt
	if fetchedAt.IsZero() {
		fetchedAt = time.Now()
	}
	c.listCache[key] = listEntry{
		hasData:     true,
		fetchedAt:   fetchedAt,
		prs:         append([]PRSummary(nil), result.PRs...),
		failed:      append([]string(nil), result.Failed...),
		rateLimited: result.RateLimited,
	}
}

// startBackoff records a 429 backoff window (Retry-After wins when present).
func (c *Client) startBackoff(d time.Duration) {
	if d <= 0 {
		d = defaultRateLimitBackoff
	}
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	until := time.Now().Add(d)
	if until.After(c.rateLimitedUntil) {
		c.rateLimitedUntil = until
	}
}

// retryAfterOf extracts the Retry-After hint from an upstream error.
func retryAfterOf(err error) time.Duration {
	if apiErr, ok := AsAPIError(err); ok {
		return apiErr.RetryAfter
	}
	return 0
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

// listCacheKey identifies a list request: role, state, workspace, window and repos.
func listCacheKey(opts ListOptions) string {
	parts := make([]string, 0, len(opts.Repos))
	for _, r := range opts.Repos {
		parts = append(parts, r.String())
	}
	return strings.Join([]string{
		opts.Role,
		opts.State,
		opts.Workspace,
		strconv.Itoa(opts.ActiveDays),
		strings.Join(parts, ","),
	}, "|")
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
