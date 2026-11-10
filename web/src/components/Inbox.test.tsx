import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';
import {MemoryRouter, Route, Routes} from 'react-router-dom';
import {QueryClient, QueryClientProvider} from '@tanstack/react-query';
import {Inbox, SCAN_SETTLE_MS} from './Inbox';
import type {PRSummary, User} from '../types';

const USER: User = {
    uuid: 'u1',
    account_id: 'a1',
    display_name: 'Ada Lovelace',
    nickname: 'ada',
    avatar_url: '',
};

const SUMMARY: PRSummary = {
    workspace: 'ws',
    repo: 'repo',
    id: 1,
    title: 'Improve the diff viewer',
    author: USER,
    state: 'OPEN',
    source_branch: 'feature/diff',
    dest_branch: 'main',
    created_on: new Date(Date.now() - 3_600_000).toISOString(),
    updated_on: new Date(Date.now() - 60_000).toISOString(),
    comment_count: 2,
    task_count: 0,
    my_state: 'none',
    approvals: 0,
    build_state: 'SUCCESSFUL',
    url: 'https://bitbucket.org/ws/repo/pull-requests/1',
};

type Reply = {
    body: unknown;
    status?: number;
    headers?: Record<string, string>;
};

function fakeResponse({body, status = 200, headers = {}}: Reply): Response {
    const all = new Map(
        Object.entries({'content-type': 'application/json', ...headers}).map(([k, v]) => [
            k.toLowerCase(),
            v,
        ]),
    );
    return {
        ok: status >= 200 && status < 300,
        status,
        statusText: 'OK',
        headers: {get: (name: string) => all.get(name.toLowerCase()) ?? null},
        json: async () => body,
        text: async () => JSON.stringify(body),
    } as unknown as Response;
}

const PR_URL = 'https://bitbucket.org/ws/repo/pull-requests/5';

let prHeaders: Record<string, string>;
let prsCalls: number;
let scanCalls: number;
let resolveCalls: string[];

beforeEach(() => {
    prHeaders = {};
    prsCalls = 0;
    scanCalls = 0;
    resolveCalls = [];
    vi.stubGlobal(
        'fetch',
        vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
            const url = String(input);
            const method = init?.method ?? 'GET';
            if (url.startsWith('/api/me')) {
                return fakeResponse({body: USER});
            }
            if (url.startsWith('/api/prs?')) {
                prsCalls += 1;
                return fakeResponse({body: [SUMMARY], headers: prHeaders});
            }
            if (url === '/api/scan' && method === 'POST') {
                scanCalls += 1;
                return fakeResponse({body: {}, status: 202});
            }
            if (url.startsWith('/api/resolve?')) {
                resolveCalls.push(url);
                return fakeResponse({body: {workspace: 'ws', repo: 'repo', id: 5}});
            }
            return fakeResponse({body: {error: `unhandled ${url}`, status: 0}, status: 500});
        }),
    );
});

afterEach(() => {
    vi.unstubAllGlobals();
});

function renderInbox(path = '/') {
    const client = new QueryClient({
        defaultOptions: {queries: {retry: false, gcTime: 0}},
    });
    return render(
        <QueryClientProvider client={client}>
            <MemoryRouter
                initialEntries={[path]}
                future={{v7_startTransition: true, v7_relativeSplatPath: true}}
            >
                <Routes>
                    <Route path="/" element={<Inbox />} />
                    <Route path="/pr/:ws/:repo/:id" element={<div>PR page</div>} />
                </Routes>
            </MemoryRouter>
        </QueryClientProvider>,
    );
}

describe('Inbox freshness', () => {
    it('shows the fetch age from X-BBR-Fetched-At', async () => {
        prHeaders = {
            'X-BBR-Fetched-At': new Date(Date.now() - 45_000).toISOString(),
        };
        renderInbox();
        expect(await screen.findByText(/updated \d+s ago/)).toBeInTheDocument();
    });

    it('shows a subtle rate-limit banner', async () => {
        prHeaders = {'X-BBR-Rate-Limited': '1'};
        renderInbox();
        expect(await screen.findByText(/rate limiting requests/i)).toBeInTheDocument();
    });

    it('scans then refetches on the To-review tab', async () => {
        vi.useFakeTimers({shouldAdvanceTime: true});
        try {
            renderInbox();
            await screen.findByText('Improve the diff viewer');
            expect(prsCalls).toBe(1);
            fireEvent.click(screen.getByRole('button', {name: 'Refresh pull request list'}));
            await waitFor(() => expect(scanCalls).toBe(1));
            await act(async () => {
                await vi.advanceTimersByTimeAsync(SCAN_SETTLE_MS + 50);
            });
            await waitFor(() => expect(prsCalls).toBe(2));
        } finally {
            vi.useRealTimers();
        }
    });

    it('refetches directly on the Mine tab without scanning', async () => {
        renderInbox('/?role=author');
        await screen.findByText('Improve the diff viewer');
        fireEvent.click(screen.getByRole('button', {name: 'Refresh pull request list'}));
        await waitFor(() => expect(prsCalls).toBe(2));
        expect(scanCalls).toBe(0);
    });
});

describe('Inbox scanning progress', () => {
    it('shows a slim progress bar from X-BBR-Scanning', async () => {
        prHeaders = {'X-BBR-Scanning': '120/337'};
        renderInbox();
        expect(await screen.findByText('Scanning repos 120/337')).toBeInTheDocument();
        const bar = screen.getByRole('progressbar', {name: 'Repository scan progress'});
        expect(bar).toHaveAttribute('aria-valuenow', '120');
        expect(bar).toHaveAttribute('aria-valuemax', '337');
    });

    it('polls every 3s while scanning', async () => {
        prHeaders = {'X-BBR-Scanning': '1/10'};
        vi.useFakeTimers({shouldAdvanceTime: true});
        try {
            renderInbox();
            await screen.findByText(/Scanning repos/);
            await act(async () => {
                await vi.advanceTimersByTimeAsync(3_200);
            });
            await waitFor(() => expect(prsCalls).toBeGreaterThanOrEqual(2));
        } finally {
            vi.useRealTimers();
        }
    });

    it('keeps a malformed scan header visible but indeterminate', async () => {
        prHeaders = {'X-BBR-Scanning': 'starting'};
        renderInbox();
        expect(await screen.findByText('Scanning repos starting')).toBeInTheDocument();
        const bar = screen.getByRole('progressbar', {name: 'Repository scan progress'});
        expect(bar).not.toHaveAttribute('aria-valuenow');
    });
});

describe('Inbox open-by-link', () => {
    it('resolves a pasted URL typed into the input', async () => {
        renderInbox();
        await screen.findByText('Improve the diff viewer');
        const input = screen.getByLabelText('Open a pull request by URL');
        fireEvent.change(input, {target: {value: `${PR_URL}/overview`}});
        fireEvent.click(screen.getByRole('button', {name: 'Open PR'}));
        expect(await screen.findByText('PR page')).toBeInTheDocument();
        expect(resolveCalls).toEqual([`/api/resolve?url=${encodeURIComponent(PR_URL)}`]);
    });

    it('rejects text that is not a bitbucket PR url', async () => {
        renderInbox();
        await screen.findByText('Improve the diff viewer');
        const input = screen.getByLabelText('Open a pull request by URL');
        fireEvent.change(input, {target: {value: 'https://example.com/nope'}});
        fireEvent.click(screen.getByRole('button', {name: 'Open PR'}));
        expect(await screen.findByRole('alert')).toHaveTextContent(/does not look like/);
        expect(resolveCalls).toHaveLength(0);
    });

    it('opens a PR when pasting anywhere on the page', async () => {
        renderInbox();
        await screen.findByText('Improve the diff viewer');
        fireEvent.paste(window, {clipboardData: {getData: () => PR_URL}});
        expect(await screen.findByText('PR page')).toBeInTheDocument();
        expect(resolveCalls).toEqual([`/api/resolve?url=${encodeURIComponent(PR_URL)}`]);
    });

    it('ignores a paste while the filter input is focused', async () => {
        renderInbox();
        await screen.findByText('Improve the diff viewer');
        const filter = screen.getByRole('searchbox', {name: 'Filter pull requests'});
        filter.focus();
        fireEvent.paste(filter, {clipboardData: {getData: () => PR_URL}});
        await new Promise((resolve) => setTimeout(resolve, 20));
        expect(screen.queryByText('PR page')).not.toBeInTheDocument();
        expect(resolveCalls).toHaveLength(0);
    });

    it('focuses the link input with the o key', async () => {
        renderInbox();
        await screen.findByText('Improve the diff viewer');
        fireEvent.keyDown(window, {key: 'o'});
        expect(screen.getByLabelText('Open a pull request by URL')).toHaveFocus();
    });
});
