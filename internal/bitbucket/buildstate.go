package bitbucket

import (
	"context"
	"sync"
	"time"
)

const (
	// DefaultBuildBudget bounds how long a pull request list request waits for
	// build states before answering with what the cache already knows.
	DefaultBuildBudget = 2 * time.Second
	// buildStateTTL is how long a non-terminal aggregate build state is reused.
	buildStateTTL = 2 * time.Minute
	// buildStateCacheLimit caps the cached commits; eviction is random.
	buildStateCacheLimit = 5000
	// buildStateConcurrency bounds concurrent commit-status fetches.
	buildStateConcurrency = 4
	// buildStateFetchTimeout bounds the background fetches that outlive a request.
	buildStateFetchTimeout = time.Minute
)

// isTerminalBuildState reports whether a state cannot change any more, so it can
// be cached until evicted.
func isTerminalBuildState(state BuildState) bool {
	switch state {
	case BuildSuccessful, BuildFailed, BuildStopped:
		return true
	default:
		return false
	}
}

type buildStateEntry struct {
	state BuildState
	at    time.Time
}

// buildStateCache caches aggregate build states by "ws/repo@commit". Terminal
// states are kept until evicted; INPROGRESS and NONE expire after buildStateTTL
// so the next poll re-fetches them.
type buildStateCache struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]buildStateEntry
}

func newBuildStateCache(now func() time.Time) *buildStateCache {
	if now == nil {
		now = time.Now
	}
	return &buildStateCache{now: now, entries: map[string]buildStateEntry{}}
}

// get returns the cached state, whether it is fresh enough to skip a fetch, and
// whether anything was cached at all.
func (c *buildStateCache) get(key string) (BuildState, bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return BuildNone, false, false
	}
	fresh := isTerminalBuildState(entry.state) || c.now().Sub(entry.at) < buildStateTTL
	return entry.state, fresh, true
}

// put stores one aggregate state, evicting a random entry when the cache is full.
func (c *buildStateCache) put(key string, state BuildState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists && len(c.entries) >= buildStateCacheLimit {
		for victim := range c.entries {
			delete(c.entries, victim)
			break
		}
	}
	c.entries[key] = buildStateEntry{state: state, at: c.now()}
}

// clear drops every entry, e.g. after a write that may change build results.
func (c *buildStateCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[string]buildStateEntry{}
}

// buildStateKey keys the cache by repository and commit hash.
func buildStateKey(repo Repo, commit string) string {
	return repo.String() + "@" + commit
}

type buildTarget struct {
	// indexes are the prs rows that share this commit.
	indexes []int
	repo    Repo
	commit  string
}

type buildResult struct {
	indexes []int
	state   BuildState
}

// FillBuildStates fills BuildState on prs from the build-state cache. Cache
// misses and expired (INPROGRESS/NONE) entries are fetched with a concurrency of
// 4 inside budget; whatever is not ready by then keeps its last known state (or
// NONE) and finishes in the background so the next poll has it.
func (c *Client) FillBuildStates(ctx context.Context, prs []PRSummary, budget time.Duration) {
	if len(prs) == 0 {
		return
	}
	if budget <= 0 {
		budget = DefaultBuildBudget
	}

	targets := make([]buildTarget, 0, len(prs))
	byKey := make(map[string]int, len(prs))
	for i := range prs {
		if prs[i].SourceCommit == "" {
			continue
		}
		repo := Repo{Workspace: prs[i].Workspace, Repo: prs[i].Repo}
		key := buildStateKey(repo, prs[i].SourceCommit)
		state, fresh, found := c.buildCache.get(key)
		if found {
			// Keep the last known value even when it is stale.
			prs[i].BuildState = state
		}
		if at, ok := byKey[key]; ok {
			// Several rows can share a commit; fetch it once.
			targets[at].indexes = append(targets[at].indexes, i)
			continue
		}
		if fresh {
			continue
		}
		byKey[key] = len(targets)
		targets = append(targets, buildTarget{indexes: []int{i}, repo: repo, commit: prs[i].SourceCommit})
	}
	if len(targets) == 0 {
		return
	}
	c.fillBuildStatesWithin(ctx, targets, prs, budget)
}

// fillBuildStatesWithin runs the fetches and copies every result that arrives
// before the budget is spent. The rest keeps running in the background: the
// results channel is buffered, so those goroutines never block or touch prs
// after this function returns.
func (c *Client) fillBuildStatesWithin(ctx context.Context, targets []buildTarget, prs []PRSummary, budget time.Duration) {
	results := make(chan buildResult, len(targets))
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), buildStateFetchTimeout)
	go func() {
		defer cancel()
		defer close(results)
		var wg sync.WaitGroup
		sem := make(chan struct{}, buildStateConcurrency)
		for _, tgt := range targets {
			wg.Add(1)
			go func(tgt buildTarget) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				state, err := c.fetchBuildState(bg, tgt.repo, tgt.commit)
				if err != nil {
					c.log.Debug("build state fetch failed",
						"repo", tgt.repo.String(), "commit", tgt.commit, "err", err.Error())
					return
				}
				results <- buildResult{indexes: tgt.indexes, state: state}
			}(tgt)
		}
		wg.Wait()
	}()

	timer := time.NewTimer(budget)
	defer timer.Stop()
	for {
		select {
		case res, ok := <-results:
			if !ok {
				return
			}
			for _, i := range res.indexes {
				prs[i].BuildState = res.state
			}
		case <-timer.C:
			return
		case <-ctx.Done():
			return
		}
	}
}

// fetchBuildState returns the aggregate build state of one commit, using the
// cache when a concurrent fetch already filled it.
func (c *Client) fetchBuildState(ctx context.Context, repo Repo, commit string) (BuildState, error) {
	key := buildStateKey(repo, commit)
	if state, fresh, found := c.buildCache.get(key); found && fresh {
		return state, nil
	}
	builds, err := c.commitStatuses(ctx, repo, commit)
	if err != nil {
		return BuildNone, err
	}
	state := aggregateBuilds(builds)
	c.buildCache.put(key, state)
	return state, nil
}

// recordBuildState caches an aggregate state learned from a pull request detail
// fetch, so list rows do not have to ask for it again.
func (c *Client) recordBuildState(repo Repo, commit string, state BuildState) {
	if commit == "" {
		return
	}
	c.buildCache.put(buildStateKey(repo, commit), state)
}
