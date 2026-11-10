import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';
import {MemoryRouter, Route, Routes} from 'react-router-dom';
import {QueryClient, QueryClientProvider} from '@tanstack/react-query';
import {Inbox} from './Inbox';
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
let resolveCalls: string[];

beforeEach(() => {
    prHeaders = {};
    prsCalls = 0;
    resolveCalls = [];
    vi.stubGlobal(
        'fetch',
        vi.fn(async (input: RequestInfo | URL) => {
            const url = String(input);
            if (url.startsWith('/api/me')) {
                return fakeResponse({body: USER});
            }
            if (url.startsWith('/api/prs?')) {
                prsCalls += 1;
                return fakeResponse({body: [SUMMARY], headers: prHeaders});
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

function renderInbox() {
    const client = new QueryClient({
        defaultOptions: {queries: {retry: false, gcTime: 0}},
    });
    return render(
        <QueryClientProvider client={client}>
            <MemoryRouter
                initialEntries={['/']}
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

    it('refreshes manually', async () => {
        renderInbox();
        await screen.findByText('Improve the diff viewer');
        expect(prsCalls).toBe(1);
        fireEvent.click(screen.getByRole('button', {name: 'Refresh pull request list'}));
        await waitFor(() => expect(prsCalls).toBe(2));
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
