import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest';
import {render, screen, waitFor} from '@testing-library/react';
import {QueryClient, QueryClientProvider} from '@tanstack/react-query';
import {MarkdownFile} from './MarkdownFile';
import type {MarkdownContext} from './MarkdownFile';
import {buildFileEntries} from '../lib/diff';
import type {NavItem} from '../lib/commentNav';

const SOURCE = '# Title\n\nIntro\n\nNew paragraph\n';

const DIFF = [
    'diff --git a/README.md b/README.md',
    'index 1111111..2222222 100644',
    '--- a/README.md',
    '+++ b/README.md',
    '@@ -1,4 +1,5 @@',
    ' # Title',
    ' ',
    ' Intro',
    ' ',
    '+New paragraph',
    '',
].join('\n');

const DELETED_DIFF = [
    'diff --git a/gone.md b/gone.md',
    'deleted file mode 100644',
    'index 1111111..0000000',
    '--- a/gone.md',
    '+++ /dev/null',
    '@@ -1,2 +0,0 @@',
    '-# Gone',
    '-old text',
    '',
].join('\n');

function fakeText(body: string): Response {
    return {
        ok: true,
        status: 200,
        statusText: 'OK',
        headers: {get: () => 'text/plain'},
        text: async () => body,
        json: async () => body,
    } as unknown as Response;
}

function commentItem(overrides: Partial<NavItem> = {}): NavItem {
    return {
        id: 'c-1',
        kind: 'inline',
        fileIndex: 0,
        path: 'README.md',
        line: 3,
        author: null,
        authorName: 'Ada',
        excerpt: 'Looks good',
        replyCount: 0,
        draft: false,
        isMine: false,
        commentId: 1,
        draftId: null,
        changeKey: 'N3',
        anchor: {path: 'README.md', from: null, to: 3},
        createdOn: '2026-01-01T00:00:00Z',
        ...overrides,
    };
}

function renderMarkdown(entryIndex = 0, diff = DIFF, context?: Partial<MarkdownContext>) {
    const entry = buildFileEntries(diff)[entryIndex]!;
    const onJumpToComment = vi.fn();
    const ctx: MarkdownContext = {
        ws: 'ws',
        repo: 'repo',
        prUrl: 'https://bitbucket.org/ws/repo/pull-requests/1',
        sourceCommit: 'newsha',
        mergeBase: 'oldsha',
        comments: [],
        onJumpToComment,
        ...context,
    };
    const client = new QueryClient({
        defaultOptions: {queries: {retry: false, gcTime: 0}},
    });
    const view = render(
        <QueryClientProvider client={client}>
            <MarkdownFile entry={entry} context={ctx} />
        </QueryClientProvider>,
    );
    return {entry, onJumpToComment, view};
}

describe('MarkdownFile', () => {
    beforeEach(() => {
        vi.stubGlobal(
            'fetch',
            vi.fn(async () => fakeText(SOURCE)),
        );
    });

    afterEach(() => {
        vi.unstubAllGlobals();
    });

    it('renders Markdown and marks changed blocks', async () => {
        const {view} = renderMarkdown();

        expect(await screen.findByText('Title')).toBeInTheDocument();
        expect(screen.getByText('Intro')).toBeInTheDocument();
        expect(screen.getByText('New paragraph')).toBeInTheDocument();
        expect(view.container.querySelectorAll('.md-block')).toHaveLength(3);
        const changed = view.container.querySelectorAll('.md-block.is-changed');
        expect(changed).toHaveLength(1);
        expect(changed[0]).toHaveTextContent('New paragraph');
        expect(screen.getByText('changed')).toBeInTheDocument();
    });

    it('shows a comment marker and jumps back to the diff', async () => {
        const item = commentItem();
        const {onJumpToComment} = renderMarkdown(0, DIFF, {comments: [item]});

        const marker = await screen.findByRole('button', {name: /1 comment .* Diff view/});
        marker.click();
        expect(onJumpToComment).toHaveBeenCalledWith(item);
    });

    it('renders the old version with a deleted-file banner', async () => {
        vi.mocked(fetch).mockResolvedValue(fakeText('# Gone\n\nold text\n'));
        renderMarkdown(0, DELETED_DIFF);

        expect(await screen.findByText(/Deleted file/)).toBeInTheDocument();
        expect(await screen.findByText('Gone')).toBeInTheDocument();
        const called = vi.mocked(fetch).mock.calls[0]?.[0];
        expect(String(called)).toContain('/oldsha/gone.md');
    });

    it('refuses to render very large files and links to Bitbucket', async () => {
        vi.mocked(fetch).mockResolvedValue(fakeText('x'.repeat(301 * 1024)));
        renderMarkdown();

        expect(await screen.findByText(/Too large to render/)).toBeInTheDocument();
        const link = screen.getByRole('link', {name: /Open on Bitbucket/});
        expect(link).toHaveAttribute(
            'href',
            'https://bitbucket.org/ws/repo/src/newsha/README.md',
        );
        expect(link).toHaveAttribute('target', '_blank');
    });

    it('switches between New and Old for modified files', async () => {
        const {view} = renderMarkdown();
        await screen.findByText('Title');
        expect(screen.getByRole('button', {name: 'New'})).toHaveAttribute(
            'aria-pressed',
            'true',
        );

        screen.getByRole('button', {name: 'Old'}).click();
        await waitFor(() => {
            const calls = vi.mocked(fetch).mock.calls.map(([url]) => String(url));
            expect(calls.some((url) => url.includes('/oldsha/README.md'))).toBe(true);
        });
        expect(view.container.querySelectorAll('.md-block').length).toBeGreaterThan(0);
    });
});
