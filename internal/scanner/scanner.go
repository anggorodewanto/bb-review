// Package scanner keeps the reviewer pull request list fresh by scanning Bitbucket
// incrementally: a tick re-reads the active repos, but only queries repos that are
// new, changed, hot (still have open review PRs) or explicitly configured.
//
// The scanner owns the reviewer list state, persists it in the store file under
// the "scanner" key, and survives restarts.
package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anggorodewanto/bb-review/internal/bitbucket"
	"github.com/anggorodewanto/bb-review/internal/store"
)

const (
	// DefaultScanInterval is how often a tick runs.
	DefaultScanInterval = 60 * time.Second
	// DefaultFullSweepInterval is how often every active repo is re-queried.
	DefaultFullSweepInterval = 6 * time.Hour
	// DefaultHotInterval is how often a hot repo (one with open review PRs) is
	// re-queried when nothing changed.
	DefaultHotInterval = 5 * time.Minute
	// DefaultRequestBudget is the upstream requests/hour ceiling. Above it the
	// scanner skips hot re-checks and full sweeps for that tick.
	DefaultRequestBudget = 800
	// DefaultConcurrency bounds concurrent repo queries. Deliberately lower than
	// the old fan-out limit so ticks stay gentle on Bitbucket.
	DefaultConcurrency = 4
	// DefaultActiveDays is the discovery window when Options.ActiveDays is unset.
	DefaultActiveDays = bitbucket.DefaultActiveDays

	// triggerDebounce ignores a manual /api/scan when a tick started this recently.
	triggerDebounce = 10 * time.Second
	// baseBackoff is the first 429 backoff; it doubles on repeated 429s.
	baseBackoff = 60 * time.Second
	// maxBackoff caps the 429 backoff at 10 minutes.
	maxBackoff = 10 * time.Minute
	// tickTimeout bounds one triggered tick.
	tickTimeout = 5 * time.Minute
)

// ErrBusy is returned when a tick is already running (single-flight).
var ErrBusy = errors.New("scanner: a tick is already running")

// RepoState is what the scanner remembers about one repository.
type RepoState struct {
	// LastScannedUpdatedOn is the repo's updated_on value when it was last scanned.
	LastScannedUpdatedOn string `json:"last_scanned_updated_on,omitempty"`
	// LastScannedAt is when that scan happened (RFC3339).
	LastScannedAt string `json:"last_scanned_at,omitempty"`
	// OpenReviewPRs are the open PRs where the current user is a reviewer.
	OpenReviewPRs []bitbucket.PRSummary `json:"open_review_prs"`
}

// State is the persisted scanner state (state file key "scanner").
type State struct {
	Repos map[string]RepoState `json:"repos"`
	// LastFullSweepAt is when every active repo was last queried (RFC3339).
	LastFullSweepAt string `json:"last_full_sweep_at,omitempty"`
	// LastTickAt is when the last tick finished (RFC3339).
	LastTickAt string `json:"last_tick_at,omitempty"`
}

// Snapshot is an immutable view of the scanner for the HTTP layer.
type Snapshot struct {
	PRs         []bitbucket.PRSummary
	FetchedAt   time.Time
	Failed      []string
	RateLimited bool
	// Scanning is true while the first full sweep is still running; Done/Total
	// report its progress.
	Scanning bool
	Done     int
	Total    int
}

// Options configure a Scanner.
type Options struct {
	Client *bitbucket.Client
	Store  *store.Store
	Logger *slog.Logger
	// Workspace is the workspace used for discovery.
	Workspace string
	// ConfigRepos are the "always check" repos from the config file.
	ConfigRepos []bitbucket.Repo
	// ActiveDays is the repo-discovery window in days.
	ActiveDays int
	// ScanInterval is the tick interval (default 60s).
	ScanInterval time.Duration
	// FullSweepInterval is how often every active repo is re-queried (default 6h).
	FullSweepInterval time.Duration
	// HotInterval is how often a hot repo is re-queried when nothing changed
	// (default 5m). Changed repos and ConfigRepos are still queried every tick.
	HotInterval time.Duration
	// RequestBudget is the upstream requests/hour ceiling (default 800). Above it
	// a tick skips hot re-checks and full sweeps.
	RequestBudget int
	// Concurrency bounds concurrent repo queries (default 4).
	Concurrency int
	// Now is the clock, injectable for tests.
	Now func() time.Time
}

// Scanner owns the reviewer PR list state and the background scan loop.
type Scanner struct {
	client            *bitbucket.Client
	store             *store.Store
	log               *slog.Logger
	workspace         string
	configRepos       []bitbucket.Repo
	activeDays        int
	scanInterval      time.Duration
	fullSweepInterval time.Duration
	hotInterval       time.Duration
	requestBudget     int
	concurrency       int
	now               func() time.Time

	mu               sync.Mutex
	state            State
	ticking          bool
	lastTickStarted  time.Time
	lastTickAt       time.Time
	failed           []string
	scanning         bool
	done             int
	total            int
	backoff          time.Duration
	rateLimitedUntil time.Time
	baseCtx          context.Context
}

// New loads persisted state and returns a Scanner. Call Run to start the loop.
func New(opts Options) *Scanner {
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	s := &Scanner{
		client:            opts.Client,
		store:             opts.Store,
		log:               logger,
		workspace:         opts.Workspace,
		configRepos:       append([]bitbucket.Repo(nil), opts.ConfigRepos...),
		activeDays:        opts.ActiveDays,
		scanInterval:      opts.ScanInterval,
		fullSweepInterval: opts.FullSweepInterval,
		hotInterval:       opts.HotInterval,
		requestBudget:     opts.RequestBudget,
		concurrency:       opts.Concurrency,
		now:               opts.Now,
		state:             State{Repos: map[string]RepoState{}},
	}
	if s.activeDays <= 0 {
		s.activeDays = DefaultActiveDays
	}
	if s.scanInterval <= 0 {
		s.scanInterval = DefaultScanInterval
	}
	if s.fullSweepInterval <= 0 {
		s.fullSweepInterval = DefaultFullSweepInterval
	}
	if s.hotInterval <= 0 {
		s.hotInterval = DefaultHotInterval
	}
	if s.requestBudget <= 0 {
		s.requestBudget = DefaultRequestBudget
	}
	if s.concurrency <= 0 {
		s.concurrency = DefaultConcurrency
	}
	if s.now == nil {
		s.now = time.Now
	}
	s.load()
	return s
}

// load restores the persisted scanner state.
func (s *Scanner) load() {
	raw := s.store.ScannerState()
	if len(raw) == 0 {
		return
	}
	var state State
	if err := json.Unmarshal(raw, &state); err != nil {
		s.log.Warn("scanner state is unreadable, starting fresh", "err", err.Error())
		return
	}
	if state.Repos == nil {
		state.Repos = map[string]RepoState{}
	}
	s.state = state
	if at, ok := parseTime(state.LastTickAt); ok {
		s.lastTickAt = at
	}
	s.log.Info("scanner state loaded", "repos", len(state.Repos), "last_tick", state.LastTickAt, "last_full_sweep", state.LastFullSweepAt)
}

// Run scans immediately and then once per ScanInterval until ctx is cancelled.
func (s *Scanner) Run(ctx context.Context) {
	s.mu.Lock()
	s.baseCtx = ctx
	s.mu.Unlock()

	ticker := time.NewTicker(s.scanInterval)
	defer ticker.Stop()

	s.tickAndLog(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tickAndLog(ctx)
		}
	}
}

func (s *Scanner) tickAndLog(ctx context.Context) {
	if err := s.Tick(ctx); err != nil && !errors.Is(err, ErrBusy) && !errors.Is(err, context.Canceled) {
		s.log.Warn("scan tick failed", "err", err.Error())
	}
}

// Trigger starts a tick in the background. It returns false when a tick is running
// or when one started less than 10s ago.
func (s *Scanner) Trigger(ctx context.Context) bool {
	s.mu.Lock()
	now := s.now()
	if s.ticking || (!s.lastTickStarted.IsZero() && now.Sub(s.lastTickStarted) < triggerDebounce) {
		s.mu.Unlock()
		return false
	}
	// Reserve the slot so a second trigger is debounced even before the goroutine
	// has set the ticking flag.
	s.lastTickStarted = now
	base := s.baseCtx
	s.mu.Unlock()

	if base == nil {
		base = context.WithoutCancel(ctx)
	}
	go func() {
		scanCtx, cancel := context.WithTimeout(base, tickTimeout)
		defer cancel()
		if err := s.Tick(scanCtx); err != nil && !errors.Is(err, ErrBusy) {
			s.log.Warn("scan tick failed", "err", err.Error())
		}
	}()
	return true
}

// Tick runs one scan synchronously. It returns ErrBusy when another tick is
// running, and nil when the tick was skipped because of a 429 backoff.
func (s *Scanner) Tick(ctx context.Context) error {
	startedAt := s.now()

	s.mu.Lock()
	if s.ticking {
		s.mu.Unlock()
		return ErrBusy
	}
	if startedAt.Before(s.rateLimitedUntil) {
		s.mu.Unlock()
		return nil
	}
	s.ticking = true
	s.lastTickStarted = startedAt
	s.failed = nil
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.ticking = false
		s.scanning = false
		s.mu.Unlock()
	}()

	return s.tick(ctx, startedAt)
}

// tick is one scan pass. Callers hold the ticking flag.
func (s *Scanner) tick(ctx context.Context, startedAt time.Time) error {
	active, err := s.client.DiscoverActiveRepos(ctx, s.workspace, s.activeDays)
	if err != nil {
		if bitbucket.IsRateLimited(err) {
			s.startBackoff(bitbucket.RetryAfter(err))
		}
		return fmt.Errorf("discover repositories: %w", err)
	}

	now := s.now()
	upstreamLastHour := 0
	if s.client != nil {
		upstreamLastHour = s.client.RequestsLastHour()
	}
	overBudget := upstreamLastHour > s.requestBudget

	s.mu.Lock()
	state := s.state
	firstSweep := len(state.Repos) == 0
	fullSweep := firstSweep
	if at, ok := parseTime(state.LastFullSweepAt); !ok || now.Sub(at) >= s.fullSweepInterval {
		fullSweep = true
	}
	if overBudget {
		// Non-essential work waits for a cheaper tick: changed repos are still
		// queried, hot re-checks and full sweeps are not.
		fullSweep = false
	}

	activeKeys := make(map[string]struct{}, len(active))
	for _, item := range active {
		activeKeys[item.Repo.String()] = struct{}{}
	}
	// Repos that left the window keep their PRs only while they still have some.
	for key, repoState := range state.Repos {
		if len(repoState.OpenReviewPRs) > 0 {
			continue
		}
		if _, ok := activeKeys[key]; ok {
			continue
		}
		delete(state.Repos, key)
	}

	targets := s.targetsLocked(active, activeKeys, fullSweep, !overBudget, now)
	scanning := firstSweep && len(targets) > 0
	if scanning {
		s.scanning = true
		s.done = 0
		s.total = len(targets)
	}
	s.mu.Unlock()

	if overBudget {
		s.log.Warn("upstream request budget spent, skipping hot re-checks and full sweep",
			"upstream_last_hour", upstreamLastHour,
			"request_budget", s.requestBudget,
		)
	}

	rateLimited, retryAfter, failed := s.query(ctx, targets, now)
	finishedAt := s.now()

	s.mu.Lock()
	s.failed = failed
	s.scanning = false
	if !rateLimited {
		// A clean tick clears the backoff; a 429 keeps doubling it in startBackoff.
		s.backoff = 0
		s.rateLimitedUntil = time.Time{}
		if fullSweep {
			s.state.LastFullSweepAt = now.UTC().Format(time.RFC3339)
		}
	}
	s.state.LastTickAt = finishedAt.UTC().Format(time.RFC3339)
	s.lastTickAt = finishedAt
	s.mu.Unlock()

	if rateLimited {
		s.startBackoff(retryAfter)
	}
	if err := s.persist(); err != nil {
		s.log.Warn("persist scanner state failed", "err", err.Error())
	}

	s.log.Info("scan tick",
		"repos_active", len(active),
		"repos_scanned", len(targets),
		"full_sweep", fullSweep,
		"failed", len(failed),
		"rate_limited", rateLimited,
		"upstream_last_hour", s.upstreamLastHour(),
		"duration_ms", s.now().Sub(startedAt).Milliseconds(),
	)
	return nil
}

// target is one repo query for this tick.
type target struct {
	repo      bitbucket.Repo
	updatedOn time.Time
	inWindow  bool
}

// targetsLocked decides which repos to query: everything on a full sweep, otherwise
// new/changed repos, hot repos whose hot_interval elapsed (when includeHot is set),
// plus the configured "always check" repos.
func (s *Scanner) targetsLocked(active []bitbucket.ActiveRepo, activeKeys map[string]struct{}, fullSweep, includeHot bool, now time.Time) []target {
	targets := make([]target, 0, len(active)+len(s.configRepos))
	seen := make(map[string]struct{}, len(active)+len(s.configRepos))
	add := func(t target) {
		key := strings.ToLower(t.repo.String())
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		targets = append(targets, t)
	}

	for _, item := range active {
		key := item.Repo.String()
		stored, known := s.state.Repos[key]
		changed := !known || stored.LastScannedUpdatedOn != updatedOnKey(item.UpdatedOn)
		if changed || fullSweep || missingCommit(stored.OpenReviewPRs) {
			add(target{repo: item.Repo, updatedOn: item.UpdatedOn, inWindow: true})
			continue
		}
		if !includeHot {
			continue
		}
		if len(stored.OpenReviewPRs) > 0 && s.hotDue(stored, now) {
			add(target{repo: item.Repo, updatedOn: item.UpdatedOn, inWindow: true})
		}
	}

	// Hot repos that fell out of the active window are re-checked until they drain.
	for key, stored := range s.state.Repos {
		if len(stored.OpenReviewPRs) == 0 {
			continue
		}
		if _, ok := activeKeys[key]; ok {
			continue
		}
		if !includeHot || !s.hotDue(stored, now) {
			continue
		}
		repo, ok := repoFromKey(key, s.workspace)
		if !ok {
			continue
		}
		add(target{repo: repo})
	}

	for _, repo := range s.configRepos {
		add(target{repo: repo})
	}
	return targets
}

// hotDue reports whether a hot repo is due for a re-check: its last scan is at
// least hot_interval ago, or it has no recorded scan time.
func (s *Scanner) hotDue(stored RepoState, now time.Time) bool {
	at, ok := parseTime(stored.LastScannedAt)
	if !ok {
		return true
	}
	return now.Sub(at) >= s.hotInterval
}

// repoFromKey parses a "workspace/repo" state key. Keys without a workspace use the
// configured workspace.
func repoFromKey(key, workspace string) (bitbucket.Repo, bool) {
	parts := strings.SplitN(key, "/", 2)
	switch len(parts) {
	case 2:
		if parts[0] == "" || parts[1] == "" {
			return bitbucket.Repo{}, false
		}
		return bitbucket.Repo{Workspace: parts[0], Repo: parts[1]}, true
	default:
		return bitbucket.Repo{}, false
	}
}

// query runs the per-repo queries with the configured concurrency. A 429 stops the
// pass; results already collected are kept.
func (s *Scanner) query(ctx context.Context, targets []target, at time.Time) (bool, time.Duration, []string) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu         sync.Mutex
		rateLimit  bool
		retryAfter time.Duration
		failed     []string
	)
	sem := make(chan struct{}, s.concurrency)
	var wg sync.WaitGroup

	for _, tgt := range targets {
		wg.Add(1)
		go func(tgt target) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			prs, err := s.client.ReviewPRs(ctx, tgt.repo, "OPEN")
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return
				}
				mu.Lock()
				if bitbucket.IsRateLimited(err) {
					rateLimit = true
					if hint := bitbucket.RetryAfter(err); hint > retryAfter {
						retryAfter = hint
					}
				} else {
					failed = append(failed, s.repoLabel(tgt.repo))
				}
				mu.Unlock()
				if bitbucket.IsRateLimited(err) {
					cancel()
				}
				return
			}
			s.applyResult(tgt, prs, at)
		}(tgt)
	}
	wg.Wait()

	sort.Strings(failed)
	return rateLimit, retryAfter, failed
}

// applyResult stores one repo's PRs and advances the progress counter.
func (s *Scanner) applyResult(tgt target, prs []bitbucket.PRSummary, at time.Time) {
	key := tgt.repo.String()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scanning {
		s.done++
	}

	updated := updatedOnKey(tgt.updatedOn)
	if updated == "" {
		if previous, ok := s.state.Repos[key]; ok {
			updated = previous.LastScannedUpdatedOn
		}
	}
	if len(prs) == 0 && !tgt.inWindow {
		// Not active any more and nothing left to review: drop the entry.
		delete(s.state.Repos, key)
		return
	}
	if prs == nil {
		prs = []bitbucket.PRSummary{}
	}
	s.state.Repos[key] = RepoState{
		LastScannedUpdatedOn: updated,
		LastScannedAt:        at.UTC().Format(time.RFC3339),
		OpenReviewPRs:        prs,
	}
}

// RefreshRepo re-queries a single repo, e.g. after the user commented or approved.
func (s *Scanner) RefreshRepo(ctx context.Context, repo bitbucket.Repo) error {
	prs, err := s.client.ReviewPRs(ctx, repo, "OPEN")
	if err != nil {
		return err
	}
	s.applyResult(target{repo: repo, inWindow: true}, prs, s.now())
	return s.persist()
}

// Busy reports whether a tick is currently running.
func (s *Scanner) Busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ticking
}

// Stats is the scanner half of GET /api/stats.
type Stats struct {
	// LastTickAt is when the last tick finished (RFC3339), "" when none ran yet.
	LastTickAt string `json:"last_tick_at"`
	// LastFullSweepAt is when every active repo was last queried (RFC3339).
	LastFullSweepAt string `json:"last_full_sweep_at"`
	// ReposTracked is the number of repos in scanner state.
	ReposTracked int `json:"repos_tracked"`
	// HotRepos is the number of tracked repos that still have open review PRs.
	HotRepos int `json:"hot_repos"`
	// RateLimited is true while the scanner is backing off from a 429.
	RateLimited bool `json:"rate_limited"`
}

// Stats reports scanner health for GET /api/stats.
func (s *Scanner) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()

	stats := Stats{
		LastTickAt:      s.state.LastTickAt,
		LastFullSweepAt: s.state.LastFullSweepAt,
		ReposTracked:    len(s.state.Repos),
		RateLimited:     s.now().Before(s.rateLimitedUntil),
	}
	for _, stored := range s.state.Repos {
		if len(stored.OpenReviewPRs) > 0 {
			stats.HotRepos++
		}
	}
	return stats
}

// upstreamLastHour returns the client's upstream request count for the last hour.
func (s *Scanner) upstreamLastHour() int {
	if s.client == nil {
		return 0
	}
	return s.client.RequestsLastHour()
}

// Snapshot returns the current reviewer list and its freshness/progress metadata.
func (s *Scanner) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	prs := make([]bitbucket.PRSummary, 0, 16)
	for _, stored := range s.state.Repos {
		prs = append(prs, stored.OpenReviewPRs...)
	}
	sortPRs(prs)

	snapshot := Snapshot{
		PRs:         prs,
		Failed:      append([]string{}, s.failed...),
		RateLimited: s.now().Before(s.rateLimitedUntil),
	}
	if !s.lastTickAt.IsZero() {
		snapshot.FetchedAt = s.lastTickAt
	}
	if s.scanning {
		snapshot.Scanning = true
		snapshot.Done = s.done
		snapshot.Total = s.total
	}
	return snapshot
}

// startBackoff records a 429 window: Retry-After when given, else 60s doubling to
// 10 minutes.
func (s *Scanner) startBackoff(retryAfter time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.backoff
	switch {
	case retryAfter > 0:
		next = retryAfter
	case next == 0:
		next = baseBackoff
	default:
		next *= 2
	}
	if next > maxBackoff {
		next = maxBackoff
	}
	s.backoff = next
	s.rateLimitedUntil = s.now().Add(next)
	s.log.Warn("rate limited by bitbucket, backing off", "backoff", next.String())
}

// persist writes the state file atomically.
func (s *Scanner) persist() error {
	s.mu.Lock()
	copied := s.state.clone()
	s.mu.Unlock()
	if err := s.store.SetScannerState(copied); err != nil {
		return fmt.Errorf("save scanner state: %w", err)
	}
	return nil
}

// repoLabel names a repo in the X-BBR-Partial header.
func (s *Scanner) repoLabel(repo bitbucket.Repo) string {
	if repo.Workspace == s.workspace {
		return repo.Repo
	}
	return repo.String()
}

// clone deep-copies the state for persistence.
func (st State) clone() State {
	out := State{
		Repos:           make(map[string]RepoState, len(st.Repos)),
		LastFullSweepAt: st.LastFullSweepAt,
		LastTickAt:      st.LastTickAt,
	}
	for key, repoState := range st.Repos {
		out.Repos[key] = RepoState{
			LastScannedUpdatedOn: repoState.LastScannedUpdatedOn,
			LastScannedAt:        repoState.LastScannedAt,
			OpenReviewPRs:        append([]bitbucket.PRSummary(nil), repoState.OpenReviewPRs...),
		}
	}
	return out
}

// updatedOnKey normalises a repo updated_on value for change detection.
func updatedOnKey(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func parseTime(raw string) (time.Time, bool) {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

// sortPRs orders pull requests by updated_on descending.
func sortPRs(prs []bitbucket.PRSummary) {
	sort.SliceStable(prs, func(i, j int) bool {
		return newerFirst(prs[i].UpdatedOn, prs[j].UpdatedOn)
	})
}

func newerFirst(a, b string) bool {
	ta, oka := parseTime(a)
	tb, okb := parseTime(b)
	switch {
	case oka && okb:
		return ta.After(tb)
	case oka != okb:
		return oka
	default:
		return a > b
	}
}

// missingCommit reports whether any stored PR predates the source_commit field.
// Such repos are rescanned once so list rows can show build states.
func missingCommit(prs []bitbucket.PRSummary) bool {
	for _, pr := range prs {
		if pr.SourceCommit == "" {
			return true
		}
	}
	return false
}
