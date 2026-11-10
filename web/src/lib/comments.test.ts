import {describe, expect, it} from 'vitest';
import {parseDiff} from 'react-diff-view';
import {
    anchorChangeKey,
    anchorForChange,
    buildLineIndex,
    commentCounts,
    groupComments,
    groupDrafts,
} from './comments';
import type {Comment, Draft} from '../types';

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
`;

const PATH = 'src/a.ts';
const file = parseDiff(DIFF)[0]!;
const index = buildLineIndex(file.hunks);

function comment(overrides: Partial<Comment> & Pick<Comment, 'id'>): Comment {
    const {id, ...rest} = overrides;
    return {
        id,
        parent_id: null,
        author: {
            uuid: 'u',
            account_id: 'a',
            display_name: 'Ada',
            nickname: 'ada',
            avatar_url: '',
        },
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

describe('buildLineIndex', () => {
    it('maps added lines on the new side', () => {
        expect(anchorChangeKey(index, {path: PATH, from: null, to: 2})).toBe('I2');
    });

    it('maps removed lines on the old side', () => {
        expect(anchorChangeKey(index, {path: PATH, from: 10, to: null})).toBe('D10');
    });

    it('maps context lines on both sides', () => {
        const toKey = anchorChangeKey(index, {path: PATH, from: null, to: 4});
        const fromKey = anchorChangeKey(index, {path: PATH, from: 3, to: null});
        expect(toKey).toBe('N3');
        expect(fromKey).toBe('N3');
    });

    it('returns null for lines outside the diff', () => {
        expect(anchorChangeKey(index, {path: PATH, from: null, to: 999})).toBeNull();
        expect(anchorChangeKey(index, null)).toBeNull();
    });
});

describe('anchorForChange', () => {
    it('creates a new-side anchor for an inserted line', () => {
        const change = file.hunks[0]!.changes.find((item) => item.type === 'insert')!;
        expect(anchorForChange(PATH, change, 'new')).toEqual({path: PATH, from: null, to: 2});
    });

    it('creates an old-side anchor for a deleted line', () => {
        const change = file.hunks[1]!.changes.find((item) => item.type === 'delete')!;
        expect(anchorForChange(PATH, change, 'new')).toEqual({path: PATH, from: 10, to: null});
    });

    it('honours the clicked side for a context line', () => {
        const change = file.hunks[0]!.changes.find((item) => item.type === 'normal')!;
        expect(anchorForChange(PATH, change, 'old')).toEqual({path: PATH, from: 1, to: null});
        expect(anchorForChange(PATH, change, 'new')).toEqual({path: PATH, from: null, to: 1});
    });
});

describe('groupComments', () => {
    it('attaches comments to the right change key', () => {
        const comments = [
            comment({id: 1, inline: {path: PATH, from: null, to: 2}}),
            comment({id: 2, inline: {path: PATH, from: 10, to: null}}),
        ];
        const grouped = groupComments(comments, index, PATH);
        expect(grouped.byKey.get('I2')?.map((t) => t.root.id)).toEqual([1]);
        expect(grouped.byKey.get('D10')?.map((t) => t.root.id)).toEqual([2]);
        expect(grouped.outdated).toHaveLength(0);
    });

    it('separates comments flagged outdated', () => {
        const comments = [comment({id: 3, outdated: true, inline: {path: PATH, from: null, to: 2}})];
        const grouped = groupComments(comments, index, PATH);
        expect(grouped.byKey.size).toBe(0);
        expect(grouped.outdated[0]?.root.id).toBe(3);
    });

    it('treats anchors missing from the diff as outdated', () => {
        const comments = [comment({id: 4, inline: {path: PATH, from: null, to: 400}})];
        const grouped = groupComments(comments, index, PATH);
        expect(grouped.outdated[0]?.root.id).toBe(4);
    });

    it('nests replies under their parent and sorts them', () => {
        const comments = [
            comment({id: 5, inline: {path: PATH, from: null, to: 2}}),
            comment({id: 6, parent_id: 5, created_on: '2026-01-02T00:00:00Z'}),
            comment({id: 7, parent_id: 5, created_on: '2026-01-01T00:00:00Z'}),
        ];
        const grouped = groupComments(comments, index, PATH);
        expect(grouped.byKey.get('I2')?.[0]?.replies.map((r) => r.id)).toEqual([7, 6]);
    });

    it('keeps general comments out of the inline widgets', () => {
        const grouped = groupComments([comment({id: 8})], index, PATH);
        expect(grouped.general[0]?.root.id).toBe(8);
        expect(grouped.byKey.size).toBe(0);
    });

    it('ignores comments on other files', () => {
        const grouped = groupComments(
            [comment({id: 9, inline: {path: 'src/other.ts', from: null, to: 2}})],
            index,
            PATH,
        );
        expect(grouped.byKey.size).toBe(0);
        expect(grouped.outdated).toHaveLength(0);
        expect(grouped.general).toHaveLength(0);
    });

    it('hides deleted roots without replies', () => {
        const grouped = groupComments(
            [comment({id: 10, deleted: true, inline: {path: PATH, from: null, to: 2}})],
            index,
            PATH,
        );
        expect(grouped.byKey.size).toBe(0);
    });
});

describe('groupDrafts', () => {
    it('maps inline drafts to change keys', () => {
        const grouped = groupDrafts(
            [draft({id: 'd1', inline: {path: PATH, from: null, to: 2}})],
            index,
            PATH,
        );
        expect(grouped.byKey.get('I2')?.map((d) => d.id)).toEqual(['d1']);
    });

    it('collects reply drafts by parent id', () => {
        const grouped = groupDrafts([draft({id: 'd2', parent_id: 5})], index, PATH);
        expect(grouped.replies.get(5)?.map((d) => d.id)).toEqual(['d2']);
    });

    it('puts drafts with stale anchors in unmapped', () => {
        const grouped = groupDrafts(
            [draft({id: 'd3', inline: {path: PATH, from: null, to: 500}})],
            index,
            PATH,
        );
        expect(grouped.unmapped.map((d) => d.id)).toEqual(['d3']);
    });

    it('skips drafts belonging to another file', () => {
        const grouped = groupDrafts(
            [draft({id: 'd4', inline: {path: 'src/other.ts', from: null, to: 2}})],
            index,
            PATH,
        );
        expect(grouped.byKey.size).toBe(0);
        expect(grouped.unmapped).toHaveLength(0);
    });
});

describe('commentCounts', () => {
    it('counts live inline comments per path', () => {
        const counts = commentCounts([
            comment({id: 1, inline: {path: PATH, from: null, to: 2}}),
            comment({id: 2, inline: {path: PATH, from: null, to: 2}}),
            comment({id: 3, deleted: true, inline: {path: PATH, from: null, to: 2}}),
            comment({id: 4}),
        ]);
        expect(counts[PATH]).toBe(2);
    });
});
