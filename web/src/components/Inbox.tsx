import {useEffect, useMemo, useRef, useState} from 'react';
import {Link, useNavigate, useSearchParams} from 'react-router-dom';
import {useMutation, useQuery} from '@tanstack/react-query';
import * as api from '../api';
import type {PRSummary} from '../types';
import {fuzzyMatch, relativeAge, relativeAgeLong, isoTitle} from '../lib/format';
import {extractPRUrl} from '../lib/prUrl';
import {isTypingTarget} from '../lib/keymap';
import {Avatar, BuildDot, EmptyState, ErrorState, ReviewBadge, Skeleton, Spinner} from './ui';

type Role = 'reviewer' | 'author';

const REFRESH_TICK_MS = 10_000;
const SCAN_POLL_MS = 3_000;
export const SCAN_SETTLE_MS = 2_000;

export function Inbox() {
    const [params, setParams] = useSearchParams();
    const role: Role = params.get('role') === 'author' ? 'author' : 'reviewer';
    const [filter, setFilter] = useState('');
    const [url, setUrl] = useState('');
    const [inputError, setInputError] = useState<string | null>(null);
    const [now, setNow] = useState(() => Date.now());
    const urlInputRef = useRef<HTMLInputElement>(null);
    const navigate = useNavigate();

    const listQuery = useQuery({
        queryKey: ['prs', role],
        queryFn: () => api.listPRs(role),
        // Poll quickly while the backend is still sweeping repos, then relax.
        refetchInterval: (query) => (query.state.data?.scanning ? SCAN_POLL_MS : 60_000),
        refetchOnWindowFocus: true,
    });
    const meQuery = useQuery({
        queryKey: ['me'],
        queryFn: api.getMe,
        staleTime: Infinity,
        retry: 1,
    });
    const resolveMutation = useMutation({
        mutationFn: (rawUrl: string) => api.resolvePR(rawUrl),
        onSuccess: (ref) => {
            navigate(`/pr/${ref.workspace}/${ref.repo}/${ref.id}`);
        },
    });

    const [manualRefreshing, setManualRefreshing] = useState(false);
    const [refreshError, setRefreshError] = useState<string | null>(null);

    const prs = listQuery.data?.data ?? [];
    const fetchedAt = listQuery.data?.fetchedAt ?? null;
    const scanning = listQuery.data?.scanning ?? null;
    const freshness = fetchedAt ? relativeAgeLong(fetchedAt, now) : '';
    const filtered = useMemo(
        () =>
            prs.filter((pr) =>
                fuzzyMatch(filter, [pr.title, `${pr.workspace}/${pr.repo}`, pr.author.display_name]),
            ),
        [prs, filter],
    );

    // "updated 30s ago" keeps ticking between background refreshes.
    useEffect(() => {
        if (!fetchedAt) {
            return;
        }
        setNow(Date.now());
        const timer = window.setInterval(() => setNow(Date.now()), REFRESH_TICK_MS);
        return () => window.clearInterval(timer);
    }, [fetchedAt]);

    // Paste a PR link anywhere on the inbox (except into a text field).
    useEffect(() => {
        function onPaste(event: ClipboardEvent) {
            if (isTypingTarget(event.target)) {
                return;
            }
            const prUrl = extractPRUrl(event.clipboardData?.getData('text') ?? '');
            if (!prUrl) {
                return;
            }
            event.preventDefault();
            setUrl(prUrl);
            setInputError(null);
            resolveMutation.mutate(prUrl);
        }
        window.addEventListener('paste', onPaste);
        return () => window.removeEventListener('paste', onPaste);
    }, [resolveMutation.mutate]);

    // `o` jumps to the open-by-link input.
    useEffect(() => {
        function onKeyDown(event: KeyboardEvent) {
            if (event.key !== 'o' || event.ctrlKey || event.metaKey || event.altKey) {
                return;
            }
            if (isTypingTarget(event.target)) {
                return;
            }
            event.preventDefault();
            urlInputRef.current?.focus();
            urlInputRef.current?.select();
        }
        window.addEventListener('keydown', onKeyDown);
        return () => window.removeEventListener('keydown', onKeyDown);
    }, []);

    function openUrl(raw: string) {
        const prUrl = extractPRUrl(raw);
        if (!prUrl) {
            setInputError('That does not look like a Bitbucket pull request URL.');
            return;
        }
        setInputError(null);
        resolveMutation.mutate(prUrl);
    }

    function submitUrl(event: React.FormEvent<HTMLFormElement>) {
        event.preventDefault();
        openUrl(url);
    }

    function selectRole(next: Role) {
        if (next === role) {
            return;
        }
        setParams(next === 'author' ? {role: 'author'} : {}, {replace: true});
    }

    async function refreshList() {
        if (manualRefreshing) {
            return;
        }
        setManualRefreshing(true);
        setRefreshError(null);
        // On "To review", kick a full rescan first and give it a moment to
        // produce results. Still refetch if the scan request fails.
        if (role === 'reviewer') {
            try {
                await api.scanPRs();
                await sleep(SCAN_SETTLE_MS);
            } catch (err) {
                setRefreshError(`Rescan failed: ${messageOf(err)}`);
            }
        }
        try {
            await listQuery.refetch();
        } finally {
            setManualRefreshing(false);
        }
    }

    const resolveError = inputError ?? (resolveMutation.isError ? messageOf(resolveMutation.error) : null);

    return (
        <div className="page inbox">
            <header className="topbar">
                <span className="brand">bb-review</span>
                <span className="topbar-spacer" />
                {meQuery.data ? (
                    <span className="me">
                        <Avatar user={meQuery.data} size={26} />
                        <span className="me-name">{meQuery.data.display_name}</span>
                    </span>
                ) : null}
            </header>

            <form className="open-pr" onSubmit={submitUrl}>
                <label className="sr-only" htmlFor="open-pr-url">
                    Open a pull request by URL
                </label>
                <input
                    id="open-pr-url"
                    ref={urlInputRef}
                    className="open-pr-input"
                    type="text"
                    inputMode="url"
                    autoComplete="off"
                    spellCheck={false}
                    value={url}
                    onChange={(event) => {
                        setUrl(event.target.value);
                        setInputError(null);
                        resolveMutation.reset();
                    }}
                    placeholder="Paste a bitbucket.org pull request URL… (o)"
                />
                <button
                    type="submit"
                    className="btn btn-primary"
                    disabled={resolveMutation.isPending || url.trim().length === 0}
                >
                    {resolveMutation.isPending ? <Spinner label="Opening" /> : null}
                    Open PR
                </button>
            </form>
            {resolveError ? (
                <p className="form-error open-pr-error" role="alert">
                    {resolveError}
                </p>
            ) : null}

            <div className="inbox-head">
                <div className="tabs" role="tablist" aria-label="Pull request list">
                    <button
                        type="button"
                        role="tab"
                        aria-selected={role === 'reviewer'}
                        className={`tab${role === 'reviewer' ? ' is-active' : ''}`}
                        onClick={() => selectRole('reviewer')}
                    >
                        To review
                    </button>
                    <button
                        type="button"
                        role="tab"
                        aria-selected={role === 'author'}
                        className={`tab${role === 'author' ? ' is-active' : ''}`}
                        onClick={() => selectRole('author')}
                    >
                        Mine
                    </button>
                </div>

                <div className="inbox-fresh">
                    {freshness ? (
                        <span className="freshness" title={isoTitle(fetchedAt ?? '')}>
                            updated {freshness}
                        </span>
                    ) : null}
                    <button
                        type="button"
                        className="btn btn-small refresh-btn"
                        onClick={() => void refreshList()}
                        disabled={manualRefreshing}
                        aria-label="Refresh pull request list"
                    >
                        {manualRefreshing ? <Spinner label="Refreshing" /> : null}
                        Refresh
                    </button>
                </div>

                <input
                    className="filter-input"
                    type="search"
                    value={filter}
                    onChange={(event) => setFilter(event.target.value)}
                    placeholder="Filter by title, repo or author…"
                    aria-label="Filter pull requests"
                />
            </div>

            {refreshError ? (
                <p className="form-error" role="alert">
                    {refreshError}
                </p>
            ) : null}

            {scanning ? <ScanProgress scanning={scanning} /> : null}

            {listQuery.data?.rateLimited ? (
                <div className="info-banner" role="status">
                    Bitbucket is rate limiting requests — showing the most recent list.
                </div>
            ) : null}

            {listQuery.data?.partial ? (
                <div className="warn-banner" role="status">
                    Some repositories could not be loaded — this list may be incomplete.
                </div>
            ) : null}

            {listQuery.isError ? (
                <ErrorState message={messageOf(listQuery.error)} onRetry={() => void listQuery.refetch()} />
            ) : null}

            {listQuery.isPending ? <Skeleton rows={8} /> : null}

            {listQuery.isSuccess && prs.length === 0 ? (
                <EmptyState
                    title={role === 'reviewer' ? 'Nothing waiting for your review' : 'You have no open pull requests'}
                    hint="Pull requests show up here as soon as they are opened."
                />
            ) : null}

            {listQuery.isSuccess && prs.length > 0 && filtered.length === 0 ? (
                <EmptyState
                    title="No matches"
                    hint={
                        <button type="button" className="btn btn-small" onClick={() => setFilter('')}>
                            Clear filter
                        </button>
                    }
                />
            ) : null}

            {filtered.length > 0 ? (
                <ul className="pr-list">
                    {filtered.map((pr) => (
                        <PRRow key={`${pr.workspace}/${pr.repo}/${pr.id}`} pr={pr} />
                    ))}
                </ul>
            ) : null}
        </div>
    );
}

function PRRow({pr}: {pr: PRSummary}) {
    return (
        <li>
            <Link className="pr-row" to={`/pr/${pr.workspace}/${pr.repo}/${pr.id}`}>
                <BuildDot state={pr.build_state} />

                <div className="pr-row-main">
                    <div className="pr-row-title">{pr.title}</div>
                    <div className="pr-row-meta">
                        <span className="repo">
                            {pr.workspace}/{pr.repo}#{pr.id}
                        </span>
                        <span className="branches">
                            {pr.source_branch} → {pr.dest_branch}
                        </span>
                        <span className="muted" title={isoTitle(pr.updated_on)}>
                            updated {relativeAge(pr.updated_on)}
                        </span>
                    </div>
                </div>

                <div className="pr-row-bottom">
                    <div className="pr-row-stats">
                        {pr.approvals > 0 ? (
                            <span className="stat stat-ok" title={`${pr.approvals} approvals`}>
                                ✓ {pr.approvals}
                            </span>
                        ) : null}
                        <span className="stat" title={`${pr.comment_count} comments`}>
                            💬 {pr.comment_count}
                        </span>
                        {pr.task_count > 0 ? (
                            <span className="stat" title={`${pr.task_count} open tasks`}>
                                ☑ {pr.task_count}
                            </span>
                        ) : null}
                    </div>

                    <div className="pr-row-side">
                        <ReviewBadge state={pr.my_state} />
                        <span className="row-age" title={isoTitle(pr.created_on)}>
                            {relativeAge(pr.created_on)}
                        </span>
                        <Avatar user={pr.author} size={26} />
                    </div>
                </div>
            </Link>
        </li>
    );
}

function messageOf(error: unknown): string {
    return error instanceof Error ? error.message : 'Could not load pull requests';
}

function sleep(ms: number): Promise<void> {
    return new Promise((resolve) => {
        window.setTimeout(resolve, ms);
    });
}

function parseScanProgress(scanning: string): {done: number; total: number} | null {
    const match = /^(\d+)\s*\/\s*(\d+)$/.exec(scanning.trim());
    if (!match) {
        return null;
    }
    const done = Number(match[1]);
    const total = Number(match[2]);
    if (!Number.isFinite(done) || !Number.isFinite(total) || total <= 0) {
        return null;
    }
    return {done: Math.min(done, total), total};
}

function ScanProgress({scanning}: {scanning: string}) {
    const parsed = parseScanProgress(scanning);
    const pct = parsed ? Math.round((parsed.done / parsed.total) * 100) : null;

    return (
        <div
            className={`scan-progress${parsed ? '' : ' is-indeterminate'}`}
            role="status"
            aria-live="polite"
        >
            <div className="scan-progress-head">
                <span className="scan-progress-label">Scanning repos {scanning}</span>
                {pct !== null ? <span className="scan-progress-pct">{pct}%</span> : null}
            </div>
            <div
                className="scan-track"
                role="progressbar"
                aria-label="Repository scan progress"
                aria-valuemin={0}
                aria-valuemax={parsed?.total}
                aria-valuenow={parsed?.done}
                aria-valuetext={scanning}
            >
                <div className="scan-fill" style={pct === null ? undefined : {width: `${pct}%`}} />
            </div>
        </div>
    );
}
