package bitbucket

import (
	"sync"
	"time"
)

const (
	// responseCacheTTL is how long PR detail, diff and comment responses are
	// reused before the next request goes upstream again.
	responseCacheTTL = 30 * time.Second
	// responseCacheLimit caps how many pull requests are held per response kind.
	responseCacheLimit = 512
	// maxCachedDiffSize is the largest single diff worth keeping in memory.
	maxCachedDiffSize = 8 << 20
	// maxCachedDiffBytes caps the total diff bytes kept in memory.
	maxCachedDiffBytes = 32 << 20
)

// responseCache is a short-lived cache for the read endpoints the PR page opens
// with (detail, diff, comments). It is keyed by "ws/repo/id" and busted by any
// write on that pull request.
type responseCache struct {
	mu           sync.Mutex
	now          func() time.Time
	details      map[string]detailEntry
	diffs        map[string]diffEntry
	commentLists map[string]commentsEntry
	// diffBytes is the total size of the bodies in diffs.
	diffBytes int
}

type detailEntry struct {
	at     time.Time
	detail PRDetail
}

type diffEntry struct {
	at     time.Time
	commit string
	body   []byte
}

type commentsEntry struct {
	at       time.Time
	comments []Comment
}

func newResponseCache(now func() time.Time) *responseCache {
	if now == nil {
		now = time.Now
	}
	return &responseCache{
		now:          now,
		details:      map[string]detailEntry{},
		diffs:        map[string]diffEntry{},
		commentLists: map[string]commentsEntry{},
	}
}

// clone returns a copy safe to hand out: the server serialises it, but callers
// must not be able to mutate the cached value.
func (d PRDetail) clone() PRDetail {
	out := d
	// Copy into non-nil slices: a nil slice encodes as JSON null and the
	// frontend expects arrays.
	out.Participants = append([]Participant{}, d.Participants...)
	out.Builds = append([]BuildStatus{}, d.Builds...)
	return out
}

// detail returns a fresh cached detail for key.
func (c *responseCache) detail(key string) (PRDetail, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.details[key]
	if !ok {
		return PRDetail{}, false
	}
	if c.now().Sub(entry.at) > responseCacheTTL {
		delete(c.details, key)
		return PRDetail{}, false
	}
	return entry.detail.clone(), true
}

// storeDetail caches a detail. A changed source commit drops the cached diff, so
// a new push is never masked by an older diff.
func (c *responseCache) storeDetail(key string, detail PRDetail) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.details) >= responseCacheLimit {
		c.dropExpiredDetailsLocked()
		if len(c.details) >= responseCacheLimit {
			c.details = map[string]detailEntry{}
		}
	}
	c.details[key] = detailEntry{at: c.now(), detail: detail.clone()}

	diff, ok := c.diffs[key]
	if !ok {
		return
	}
	// Without both hashes the diff cannot be tied to a commit: drop it to be safe.
	if diff.commit == "" || detail.SourceCommit == "" || diff.commit != detail.SourceCommit {
		c.deleteDiffLocked(key)
	}
}

// diff returns a fresh cached diff, unless the cached detail shows a newer
// source commit.
func (c *responseCache) diff(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.diffs[key]
	if !ok {
		return nil, false
	}
	if c.now().Sub(entry.at) > responseCacheTTL {
		c.deleteDiffLocked(key)
		return nil, false
	}
	if detail, ok := c.details[key]; ok && detail.detail.SourceCommit != entry.commit {
		c.deleteDiffLocked(key)
		return nil, false
	}
	return append([]byte(nil), entry.body...), true
}

// storeDiff caches a diff body. The source commit comes from the cached detail
// when it is known, so the entry can be dropped when the branch moves.
func (c *responseCache) storeDiff(key string, body []byte) {
	if len(body) > maxCachedDiffSize {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	commit := ""
	if detail, ok := c.details[key]; ok {
		commit = detail.detail.SourceCommit
	}
	if len(c.diffs) >= responseCacheLimit || c.diffBytes+len(body) > maxCachedDiffBytes {
		c.diffs = map[string]diffEntry{}
		c.diffBytes = 0
	}
	if old, ok := c.diffs[key]; ok {
		c.diffBytes -= len(old.body)
	}
	c.diffs[key] = diffEntry{at: c.now(), commit: commit, body: append([]byte(nil), body...)}
	c.diffBytes += len(body)
}

// comments returns a fresh cached comment list.
func (c *responseCache) comments(key string) ([]Comment, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.commentLists[key]
	if !ok {
		return nil, false
	}
	if c.now().Sub(entry.at) > responseCacheTTL {
		delete(c.commentLists, key)
		return nil, false
	}
	return append([]Comment(nil), entry.comments...), true
}

// storeComments caches a comment list.
func (c *responseCache) storeComments(key string, comments []Comment) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.commentLists) >= responseCacheLimit {
		c.dropExpiredCommentsLocked()
		if len(c.commentLists) >= responseCacheLimit {
			c.commentLists = map[string]commentsEntry{}
		}
	}
	c.commentLists[key] = commentsEntry{at: c.now(), comments: append([]Comment(nil), comments...)}
}

// invalidate drops every cached response for one pull request, e.g. after a write.
func (c *responseCache) invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.details, key)
	delete(c.commentLists, key)
	c.deleteDiffLocked(key)
}

// dropExpiredDetailsLocked removes stale detail entries. Callers hold c.mu.
func (c *responseCache) dropExpiredDetailsLocked() {
	cutoff := c.now().Add(-responseCacheTTL)
	for key, entry := range c.details {
		if entry.at.Before(cutoff) {
			delete(c.details, key)
		}
	}
}

// dropExpiredCommentsLocked removes stale comment entries. Callers hold c.mu.
func (c *responseCache) dropExpiredCommentsLocked() {
	cutoff := c.now().Add(-responseCacheTTL)
	for key, entry := range c.commentLists {
		if entry.at.Before(cutoff) {
			delete(c.commentLists, key)
		}
	}
}

// deleteDiffLocked removes one diff and keeps the byte accounting honest.
// Callers hold c.mu.
func (c *responseCache) deleteDiffLocked(key string) {
	entry, ok := c.diffs[key]
	if !ok {
		return
	}
	delete(c.diffs, key)
	c.diffBytes -= len(entry.body)
}
