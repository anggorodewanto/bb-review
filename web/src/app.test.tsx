import {describe, expect, it, vi, beforeEach, afterEach} from 'vitest';
import {render, screen, fireEvent, waitFor} from '@testing-library/react';
import {MemoryRouter} from 'react-router-dom';
import {QueryClient, QueryClientProvider} from '@tanstack/react-query';
import {App} from './App';
import type {Comment, PRDetail, User} from './types';

const DIFF = `diff --git a/src/a.ts b/src/a.ts
index 1111111..2222222 100644
--- a/src/a.ts
+++ b/src/a.ts
@@ -1,3 +1,4 @@
 line1
+added line
 line2
 line3
`;

const USER: User = {
    uuid: 'u1',
    account_id: 'a1',
    display_name: 'Ada Lovelace',
    nickname: 'ada',
    avatar_url: '',
};

const DETAIL: PRDetail = {
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
    comment_count: 1,
    task_count: 0,
    my_state: 'none',
    approvals: 0,
    build_state: 'SUCCESSFUL',
    source_commit: 'abc123def456',
    url: 'https://bitbucket.org/ws/repo/pull-requests/1',
    description_raw: 'desc',
    description_html: '<p onclick="evil()">Description <script>alert(1)</script></p>',
    participants: [{user: USER, role: 'REVIEWER', state: 'none'}],
    dest_commit: 'def456abc123',
    merge_base: '999888777666',
    builds: [],
};

const COMMENT: Comment = {
    id: 1,
    parent_id: null,
    author: USER,
    raw: 'Nice change',
    html: '<p>Nice change</p>',
    created_on: new Date().toISOString(),
    updated_on: new Date().toISOString(),
    inline: {path: 'src/a.ts', from: null, to: 2},
    deleted: false,
    outdated: false,
};

function fakeResponse(body: unknown, contentType = 'application/json'): Response {
    return {
        ok: true,
        status: 200,
        statusText: 'OK',
        headers: {get: () => contentType},
        json: async () => body,
        text: async () => (typeof body === 'string' ? body : JSON.stringify(body)),
    } as unknown as Response;
}

beforeEach(() => {
    window.localStorage.clear();
    vi.stubGlobal(
        'fetch',
        vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
            const url = String(input);
            const method = init?.method ?? 'GET';
            if (url.endsWith('/drafts') && method === 'POST') {
                return fakeResponse({
                    id: 'd1',
                    raw: 'Looks good to me',
                    inline: {path: 'src/a.ts', from: null, to: 1},
                    parent_id: null,
                    created_on: new Date().toISOString(),
                });
            }
            if (url.endsWith('/drafts/submit')) {
                return fakeResponse({posted: [], failed: []});
            }
            if (url.startsWith('/api/me')) return fakeResponse(USER);
            if (url.startsWith('/api/prs?')) return fakeResponse([DETAIL]);
            if (url.endsWith('/diff')) return fakeResponse(DIFF, 'text/plain');
            if (url.endsWith('/comments')) return fakeResponse([COMMENT]);
            if (url.endsWith('/drafts')) return fakeResponse([]);
            if (url.endsWith('/viewed')) return fakeResponse({files: {}});
            if (/\/api\/prs\/ws\/repo\/1$/.test(url)) return fakeResponse(DETAIL);
            return fakeResponse({error: `unhandled ${url}`, status: 0});
        }),
    );
    vi.stubGlobal('scrollTo', vi.fn());
});

afterEach(() => {
    vi.unstubAllGlobals();
});

function renderAt(path: string) {
    return renderAtEntries([path]);
}

type RouteEntry = string | {pathname: string; search?: string; state?: unknown};

function renderAtEntries(entries: RouteEntry[]) {
    const client = new QueryClient({
        defaultOptions: {queries: {retry: false, gcTime: 0}},
    });
    return render(
        <QueryClientProvider client={client}>
            <MemoryRouter initialEntries={entries} future={{v7_startTransition: true, v7_relativeSplatPath: true}}>
                <App />
            </MemoryRouter>
        </QueryClientProvider>,
    );
}

describe('app smoke', () => {
    it('renders the inbox with a pull request row', async () => {
        renderAt('/');
        expect(await screen.findByText('Improve the diff viewer')).toBeInTheDocument();
        expect(screen.getByText('ws/repo#1')).toBeInTheDocument();
        expect(screen.getByRole('tab', {name: 'To review'})).toHaveAttribute('aria-selected', 'true');
    });

    it('renders the PR view with an inline comment on the diff', async () => {
        renderAt('/pr/ws/repo/1');
        expect(await screen.findByText('Improve the diff viewer')).toBeInTheDocument();
        expect(await screen.findByText('src/a.ts')).toBeInTheDocument();
        expect(await screen.findByText('Nice change')).toBeInTheDocument();
        expect(screen.getByText('Description', {selector: 'summary'})).toBeInTheDocument();
    });

    it('opens and closes the keyboard help overlay', async () => {
        renderAt('/pr/ws/repo/1');
        await screen.findByText('Improve the diff viewer');
        fireEvent.keyDown(window, {key: '?'});
        const dialog = await screen.findByRole('dialog', {name: 'Keyboard shortcuts'});
        expect(dialog).toBeInTheDocument();
        expect(dialog).toHaveTextContent('Toggle line wrapping');
        fireEvent.keyDown(window, {key: 'Escape'});
        await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    });

    it('wraps diff lines by default', async () => {
        renderAt('/pr/ws/repo/1');
        await screen.findByText('Improve the diff viewer');
        const wrapToggle = await screen.findByRole('button', {name: 'Wrap lines'});
        expect(wrapToggle).toHaveAttribute('aria-pressed', 'true');
        await waitFor(() => expect(document.querySelector('.diff')).toHaveClass('diff-wrap'));
    });

    it('toggles line wrapping with w and remembers it', async () => {
        renderAt('/pr/ws/repo/1');
        await screen.findByText('Improve the diff viewer');
        const wrapToggle = await screen.findByRole('button', {name: 'Wrap lines'});
        await waitFor(() => expect(document.querySelector('.diff')).toHaveClass('diff-wrap'));

        fireEvent.keyDown(window, {key: 'w'});

        await waitFor(() => expect(document.querySelector('.diff')).toHaveClass('diff-nowrap'));
        expect(wrapToggle).toHaveAttribute('aria-pressed', 'false');
        expect(window.localStorage.getItem('bb-review:wrap')).toBe('false');
    });

    it('restores a stored wrap preference and toggles it from the toolbar', async () => {
        window.localStorage.setItem('bb-review:wrap', 'false');
        renderAt('/pr/ws/repo/1');
        await screen.findByText('Improve the diff viewer');
        const wrapToggle = await screen.findByRole('button', {name: 'Wrap lines'});
        await waitFor(() => expect(document.querySelector('.diff')).toHaveClass('diff-nowrap'));
        expect(wrapToggle).toHaveAttribute('aria-pressed', 'false');

        fireEvent.click(wrapToggle);

        await waitFor(() => expect(document.querySelector('.diff')).toHaveClass('diff-wrap'));
        expect(window.localStorage.getItem('bb-review:wrap')).toBe('true');
    });

    it('opens the comments panel with C and lists the threads', async () => {
        renderAt('/pr/ws/repo/1');
        await screen.findByText('Improve the diff viewer');

        fireEvent.keyDown(window, {key: 'C'});

        const panel = await screen.findByRole('complementary', {name: 'Comment threads'});
        expect(panel).toHaveTextContent('Nice change');
        expect(screen.getByRole('button', {name: 'All'})).toHaveAttribute('aria-pressed', 'true');
        expect(screen.getByRole('button', {name: "Others'"})).toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'Drafts'})).toBeInTheDocument();

        fireEvent.click(screen.getByRole('button', {name: 'Drafts'}));
        expect(await screen.findByText('No comments match this filter.')).toBeInTheDocument();
    });

    it('shows a position indicator when cycling threads with ]', async () => {
        renderAt('/pr/ws/repo/1');
        await screen.findByText('Improve the diff viewer');

        fireEvent.keyDown(window, {key: ']'});

        expect(await screen.findByText('1/1')).toBeInTheDocument();
    });

    it('filters the inbox list', async () => {
        renderAt('/');
        await screen.findByText('Improve the diff viewer');
        const filter = screen.getByRole('searchbox', {name: 'Filter pull requests'});
        fireEvent.change(filter, {target: {value: 'nothing matches'}});
        expect(await screen.findByText('No matches')).toBeInTheDocument();
    });

    it('drafts a comment on a clicked line and submits the review', async () => {
        const fetchMock = vi.mocked(fetch);
        renderAt('/pr/ws/repo/1');
        await screen.findByText('Improve the diff viewer');

        fireEvent.click(await screen.findByText('line1'));
        const textarea = await screen.findByPlaceholderText('Leave a comment…');
        fireEvent.change(textarea, {target: {value: 'Looks good to me'}});
        fireEvent.click(screen.getByRole('button', {name: 'Add to review'}));

        await waitFor(() => {
            expect(screen.getByText('Looks good to me')).toBeInTheDocument();
            expect(screen.getByText('Draft')).toBeInTheDocument();
        });
        expect(
            fetchMock.mock.calls.some(([url, init]) => {
                const target = String(url);
                return target.endsWith('/drafts') && (init as RequestInit | undefined)?.method === 'POST';
            }),
        ).toBe(true);

        fireEvent.click(screen.getByRole('button', {name: /Submit review/}));
        fireEvent.click(await screen.findByRole('menuitem', {name: /Approve/}));

        await waitFor(() => {
            expect(
                fetchMock.mock.calls.some(([url, init]) => {
                    const target = String(url);
                    const request = init as RequestInit | undefined;
                    return (
                        target.endsWith('/drafts/submit') &&
                        request?.method === 'POST' &&
                        JSON.parse(String(request.body))?.action === 'approve'
                    );
                }),
            ).toBe(true);
        });
    });
});

describe('command palette', () => {
    it('opens with Ctrl+K, filters and runs a command', async () => {
        renderAt('/pr/ws/repo/1');
        await screen.findByText('Improve the diff viewer');
        await waitFor(() => expect(document.querySelector('.diff')).toHaveClass('diff-wrap'));

        fireEvent.keyDown(window, {key: 'k', ctrlKey: true});

        const dialog = await screen.findByRole('dialog', {name: 'Command palette'});
        const input = dialog.querySelector('input');
        expect(input).not.toBeNull();
        fireEvent.change(input!, {target: {value: 'line wrapping'}});
        fireEvent.keyDown(input!, {key: 'Enter'});

        await waitFor(() => expect(document.querySelector('.diff')).toHaveClass('diff-nowrap'));
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();

        const wrapButton = screen.getByRole('button', {name: 'Wrap lines'});
        expect(wrapButton).toHaveAttribute('data-command-flash');
    });

    it('closes with Escape', async () => {
        renderAt('/pr/ws/repo/1');
        await screen.findByText('Improve the diff viewer');
        fireEvent.keyDown(window, {key: 'k', ctrlKey: true});
        const input = await screen.findByRole('combobox', {name: 'Search commands'});
        fireEvent.keyDown(input, {key: 'Escape'});
        await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    });

    it('marks commands unavailable on this page', async () => {
        renderAt('/');
        await screen.findByText('Improve the diff viewer');
        fireEvent.keyDown(window, {key: 'k', ctrlKey: true});
        const input = await screen.findByRole('combobox', {name: 'Search commands'});
        fireEvent.change(input, {target: {value: 'next file'}});
        expect(await screen.findByText('Not available here')).toBeInTheDocument();
    });
});

describe('help page', () => {
    it('renders the shortcut reference and guidance sections', async () => {
        renderAt('/help');
        expect(await screen.findByRole('heading', {name: 'Help', level: 1})).toBeInTheDocument();
        expect(screen.getByText('Toggle line wrapping')).toBeInTheDocument();
        expect(screen.getByText('Reviewing a PR')).toBeInTheDocument();
        expect(screen.getByText('How the To-review list is found')).toBeInTheDocument();
    });

    it('filters the shortcut list', async () => {
        renderAt('/help');
        await screen.findByRole('heading', {name: 'Help', level: 1});
        fireEvent.change(screen.getByRole('searchbox', {name: 'Search shortcuts'}), {
            target: {value: 'wrapping'},
        });
        expect(screen.getByText('Toggle line wrapping')).toBeInTheDocument();
        expect(screen.queryByText('Toggle approve')).not.toBeInTheDocument();
    });
});

describe('navigation state', () => {
    it('g i returns to the remembered inbox URL', async () => {
        renderAt('/?tab=mine&q=diff');
        await screen.findByText('Improve the diff viewer');
        fireEvent.click(screen.getByText('Improve the diff viewer'));
        await screen.findByText('Nice change');

        fireEvent.keyDown(window, {key: 'g'});
        fireEvent.keyDown(window, {key: 'i'});

        await waitFor(() =>
            expect(screen.getByRole('tab', {name: 'Mine'})).toHaveAttribute('aria-selected', 'true'),
        );
        expect(screen.getByRole('searchbox', {name: 'Filter pull requests'})).toHaveValue('diff');
    });

    it('uses history when the PR was opened in-app', async () => {
        renderAtEntries([
            {pathname: '/help'},
            {pathname: '/pr/ws/repo/1', state: {fromApp: true}},
        ]);
        await screen.findByText('Nice change');
        fireEvent.click(screen.getByRole('button', {name: 'Back to inbox'}));
        expect(await screen.findByRole('heading', {name: 'Help', level: 1})).toBeInTheDocument();
    });

    it('falls back to the inbox without in-app history', async () => {
        renderAt('/pr/ws/repo/1');
        await screen.findByText('Nice change');
        fireEvent.click(screen.getByRole('button', {name: 'Back to inbox'}));
        expect(await screen.findByText('Improve the diff viewer')).toBeInTheDocument();
        expect(screen.getByRole('tab', {name: 'To review'})).toBeInTheDocument();
    });
});
