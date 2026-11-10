import {describe, expect, it} from 'vitest';
import {parseDiff} from 'react-diff-view';
import type {FileEntry} from './diff';
import type {Comment, Draft, User} from '../types';
import {
    buildCommentNav,
    excerptFromHtml,
    excerptOf,
    filterNavItems,
    findNavItem,
    navItemHash,
    parseCommentHash,
    previewOf,
    shortPath,
} from './commentNav';

const DIFF = `diff --git a/src/a.ts b/src/a.ts
index 1111111..2222222 100644
--- a/src/a.ts
+++ b/src/a.ts
@@ -1,3 +1,4 @@
 line1
+added
 line2
 line3
@@ -10,2 +11,2 @@
-old
+new
diff --git a/src/b.ts b/src/b.ts
index 3333333..4444444 100644
--- a/src/b.ts
+++ b/src/b.ts
@@ -1,2 +1,3 @@
 b1
+b2
 b3
`;

const ME: User = {
    uuid: 'u-me',
    account_id: 'me',
    display_name: 'Me',
    nickname: 'me',
    avatar_url: '',
};

const OTHER: User = {
    uuid: 'u-other',
    account_id: 'other',
    display_name: 'Ada',
    nickname: 'ada',
    avatar_url: '',
};

function files(): FileEntry[] {
    return parseDiff(DIFF).map((file) => ({
        path: file.newPath ?? file.oldPath ?? '',
        hunks: file.hunks,
    })) as unknown as FileEntry[];
}

function comment(overrides: Partial<Comment> & Pick<Comment, 'id'>): Comment {
    const {id, ...rest} = overrides;
    return {
        id,
        parent_id: null,
        author: OTHER,
        raw: 'comment',
        html: '<p>comment</p>',
        created_on: '2026-01-01T00:00:00Z',
        updated_on: '2026-01-01T00:00:00Z',
        inline: null,
        deleted: false,
        outdated: false,
        ...rest,
    };
}

function draft(overrides: Partial<Draft> & Pick<Draft, 'id'>): Draft {
    const {id, ...rest} = overrides;
    return {
        id,
        raw: 'draft',
        inline: null,
        parent_id: null,
        created_on: '2026-01-01T00:00:00Z',
        ...rest,
    };
}

describe('buildCommentNav ordering', () => {
    it('orders files, lines, outdated, general and drafts', () => {
        const comments = [
            comment({id: 3, inline: {path: 'src/b.ts', from: null, to: 2}}),
            comment({id: 2, inline: {path: 'src/a.ts', from: 10, to: null}}),
            comment({id: 1, inline: {path: 'src/a.ts', from: null, to: 2}}),
            comment({id: 4, inline: {path: 'src/a.ts', from: null, to: 500}, outdated: true}),
            comment({id: 5, created_on: '2026-01-02T00:00:00Z'}),
        ];
        const drafts = [
            draft({id: 'd1', inline: {path: 'src/a.ts', from: null, to: 2}, created_on: '2026-01-03T00:00:00Z'}),
            draft({id: 'd2', created_on: '2026-01-04T00:00:00Z'}),
        ];

        const items = buildCommentNav(files(), comments, drafts, ME);

        expect(items.map((item) => item.id)).toEqual([
            'c-1',
            'd-d1',
            'c-2',
            'c-3',
            'c-4',
            'c-5',
            'd-d2',
        ]);
        expect(items.map((item) => item.kind)).toEqual([
            'inline',
            'inline',
            'inline',
            'inline',
            'outdated',
            'general',
            'general',
        ]);
        expect(items[1]?.draft).toBe(true);
        expect(items[5]?.draft).toBe(false);
    });

    it('keeps replies as part of the root thread', () => {
        const comments = [
            comment({id: 10, inline: {path: 'src/a.ts', from: null, to: 2}}),
            comment({id: 11, parent_id: 10, created_on: '2026-01-02T00:00:00Z'}),
        ];
        const drafts = [draft({id: 'dr', parent_id: 10})];
        const items = buildCommentNav(files(), comments, drafts, ME);
        expect(items).toHaveLength(1);
        expect(items[0]?.replyCount).toBe(2);
    });

    it('flags the current user', () => {
        const comments = [
            comment({id: 1, author: ME, inline: {path: 'src/a.ts', from: null, to: 2}}),
            comment({id: 2, author: OTHER, inline: {path: 'src/a.ts', from: 10, to: null}}),
        ];
        const items = buildCommentNav(files(), comments, [], ME);
        expect(items.find((item) => item.id === 'c-1')?.isMine).toBe(true);
        expect(items.find((item) => item.id === 'c-2')?.isMine).toBe(false);
    });

    it('keeps comments on files outside the diff as outdated', () => {
        const comments = [
            comment({id: 1, inline: {path: 'src/gone.ts', from: null, to: 2}}),
            comment({id: 2, inline: {path: 'src/a.ts', from: null, to: 2}}),
        ];
        const items = buildCommentNav(files(), comments, [], ME);
        expect(items.map((item) => item.id)).toEqual(['c-2', 'c-1']);
        expect(items[1]?.kind).toBe('outdated');
        expect(items[1]?.fileIndex).toBe(-1);
    });

    it('shows deleted roots only when they still have replies', () => {
        const comments = [comment({id: 1, deleted: true, inline: {path: 'src/a.ts', from: null, to: 2}})];
        expect(buildCommentNav(files(), comments, [], ME)).toHaveLength(0);
    });

    it('previews the rendered HTML instead of raw markdown', () => {
        const comments = [
            comment({
                id: 1,
                raw: '## **CI Bot - Security Scan Passed**',
                html: '<h2><strong>CI Bot - Security Scan Passed</strong></h2>',
                inline: {path: 'src/a.ts', from: null, to: 2},
            }),
        ];
        const items = buildCommentNav(files(), comments, [], ME);
        expect(items[0]?.excerpt).toBe('CI Bot - Security Scan Passed');
    });
});

describe('filterNavItems', () => {
    const comments = [
        comment({id: 1, author: ME, inline: {path: 'src/a.ts', from: null, to: 2}}),
        comment({id: 2, author: OTHER, inline: {path: 'src/a.ts', from: 10, to: null}}),
    ];
    const drafts = [draft({id: 'd1', inline: {path: 'src/a.ts', from: null, to: 2}})];
    const items = buildCommentNav(files(), comments, drafts, ME);

    it('returns everything for all', () => {
        expect(filterNavItems(items, 'all')).toHaveLength(3);
    });

    it('separates others, mine and drafts', () => {
        expect(filterNavItems(items, 'others').map((item) => item.id)).toEqual(['c-2']);
        expect(filterNavItems(items, 'mine').map((item) => item.id)).toEqual(['c-1', 'd-d1']);
        expect(filterNavItems(items, 'drafts').map((item) => item.id)).toEqual(['d-d1']);
    });
});

describe('comment deep links', () => {
    it('parses Bitbucket comment hashes', () => {
        expect(parseCommentHash('#comment-42')).toEqual({type: 'comment', value: '42'});
        expect(parseCommentHash('#comment-draft-abc')).toEqual({type: 'draft', value: 'abc'});
        expect(parseCommentHash('#comment-')).toBeNull();
        expect(parseCommentHash('#something-else')).toBeNull();
        expect(parseCommentHash('')).toBeNull();
    });

    it('finds the matching nav item and builds its hash', () => {
        const comments = [comment({id: 42, inline: {path: 'src/a.ts', from: null, to: 2}})];
        const items = buildCommentNav(files(), comments, [], ME);
        const parsed = parseCommentHash('#comment-42');
        expect(parsed).not.toBeNull();
        const item = findNavItem(items, parsed!);
        expect(item?.id).toBe('c-42');
        expect(navItemHash(item!)).toBe('#comment-42');
        expect(findNavItem(items, parseCommentHash('#comment-999')!)).toBeNull();
    });
});

describe('preview helpers', () => {
    it('collapses whitespace and truncates the excerpt', () => {
        expect(excerptOf('line one\n\nline   two')).toBe('line one line two');
        const long = 'x'.repeat(300);
        expect(excerptOf(long)).toHaveLength(120);
        expect(excerptOf(long).endsWith('…')).toBe(true);
    });

    it('shortens long paths to the last two segments', () => {
        expect(shortPath('')).toBe('General');
        expect(shortPath('src/a.ts')).toBe('src/a.ts');
        expect(shortPath('internal/server/handlers.go')).toBe('server/handlers.go');
    });

    it('turns a heading with bold text into a plain preview', () => {
        const html = '<h2>✅ <strong>CI Bot - Security Scan Passed</strong></h2>';
        const excerpt = excerptFromHtml(html);
        expect(excerpt).toBe('✅ CI Bot - Security Scan Passed');
        expect(excerpt).not.toContain('##');
        expect(excerpt).not.toContain('**');
    });

    it('strips inline markup and keeps the link text', () => {
        expect(
            excerptFromHtml('<p>See <strong>this</strong> and <a href="https://example.com">a link</a>.</p>'),
        ).toBe('See this and a link.');
    });

    it('flattens code blocks onto one line', () => {
        expect(excerptFromHtml('<pre><code>line one\nline two</code></pre>')).toBe('line one line two');
    });

    it('reads table cells as plain text', () => {
        const html = `<table>
            <tbody>
                <tr>
                    <td>Build</td>
                    <td>Passed</td>
                </tr>
            </tbody>
        </table>`;
        expect(excerptFromHtml(html)).toBe('Build Passed');
    });

    it('drops unsafe markup and decodes entities', () => {
        expect(excerptFromHtml('<p>Tom &amp; Jerry</p><script>alert(1)</script>')).toBe('Tom & Jerry');
    });

    it('truncates long rendered comments', () => {
        const excerpt = excerptFromHtml(`<p>${'word '.repeat(60)}</p>`);
        expect(excerpt.length).toBeLessThanOrEqual(120);
        expect(excerpt.endsWith('…')).toBe(true);
    });

    it('falls back to raw markdown when there is no rendered HTML', () => {
        expect(previewOf({html: '', raw: 'plain **text** now'})).toBe('plain **text** now');
        expect(previewOf({html: '<p>rendered</p>', raw: 'raw'})).toBe('rendered');
    });
});
