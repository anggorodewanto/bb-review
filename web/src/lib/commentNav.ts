import type {Comment, Draft, InlineAnchor, User} from '../types';
import type {FileEntry} from './diff';
import type {CommentThread} from './comments';
import {buildLineIndex, groupComments, groupDrafts} from './comments';
import {sanitizeHtml} from './sanitize';

/** Where a navigable item lives in the PR. */
export type NavKind = 'inline' | 'outdated' | 'general';

export type NavItem = {
    /** Stable key within the list (comment or draft root). */
    id: string;
    kind: NavKind;
    /** Index into the file list, or -1 for general / unknown files. */
    fileIndex: number;
    path: string;
    /** Display line: the new side when present, else the old side. */
    line: number | null;
    author: User | null;
    authorName: string;
    excerpt: string;
    replyCount: number;
    draft: boolean;
    isMine: boolean;
    commentId: number | null;
    draftId: string | null;
    /** Rendered change key for inline items, when it maps to the diff. */
    changeKey: string | null;
    anchor: InlineAnchor | null;
    createdOn: string;
};

export type NavFilter = 'all' | 'mine' | 'others' | 'drafts';

const EXCERPT_LIMIT = 120;

function isMe(author: User | null | undefined, me: User | null): boolean {
    if (!author || !me) {
        return false;
    }
    return author.account_id === me.account_id || author.uuid === me.uuid;
}

/** One-line plain-text preview of a comment body (never HTML). */
export function excerptOf(raw: string, limit = EXCERPT_LIMIT): string {
    const text = raw.replace(/\s+/g, ' ').trim();
    if (text.length <= limit) {
        return text;
    }
    return `${text.slice(0, Math.max(0, limit - 1)).trimEnd()}…`;
}

/**
 * Preview from Bitbucket's rendered comment HTML: sanitize it, read the text
 * content, collapse whitespace and trim. Markdown headings/bold/links/code
 * become plain words instead of literal `##` / `**` characters.
 */
export function excerptFromHtml(html: string, limit = EXCERPT_LIMIT): string {
    const safe = sanitizeHtml(html);
    if (!safe) {
        return '';
    }
    if (typeof document === 'undefined') {
        return '';
    }
    const container = document.createElement('div');
    container.innerHTML = safe;
    return excerptOf(container.textContent ?? '', limit);
}

/** Root comment preview: rendered HTML first, raw markdown as a fallback. */
export function previewOf(comment: {html: string; raw: string}, limit = EXCERPT_LIMIT): string {
    return excerptFromHtml(comment.html, limit) || excerptOf(comment.raw, limit);
}

/** Last two path segments are enough to identify a file in a narrow panel. */
export function shortPath(path: string): string {
    if (path.length === 0) {
        return 'General';
    }
    const parts = path.split('/').filter(Boolean);
    if (parts.length <= 2) {
        return path;
    }
    return parts.slice(-2).join('/');
}

function lineOf(anchor: InlineAnchor | null): number | null {
    if (!anchor) {
        return null;
    }
    return anchor.to ?? anchor.from;
}

function commentItem(
    thread: CommentThread,
    replyDraftCount: number,
    me: User | null,
    position: {kind: NavKind; fileIndex: number; changeKey: string | null},
): NavItem {
    const {root} = thread;
    return {
        id: `c-${root.id}`,
        kind: position.kind,
        fileIndex: position.fileIndex,
        path: root.inline?.path ?? '',
        line: lineOf(root.inline),
        author: root.author,
        authorName: root.author.display_name,
        excerpt: previewOf(root),
        replyCount: thread.replies.length + replyDraftCount,
        draft: false,
        isMine: isMe(root.author, me),
        commentId: root.id,
        draftId: null,
        changeKey: position.changeKey,
        anchor: root.inline,
        createdOn: root.created_on,
    };
}

function draftItem(
    draft: Draft,
    me: User | null,
    position: {kind: NavKind; fileIndex: number; changeKey: string | null},
): NavItem {
    return {
        id: `d-${draft.id}`,
        kind: position.kind,
        fileIndex: position.fileIndex,
        path: draft.inline?.path ?? '',
        line: lineOf(draft.inline),
        author: me,
        authorName: me?.display_name ?? 'You',
        excerpt: excerptOf(draft.raw),
        replyCount: 0,
        draft: true,
        isMine: true,
        commentId: null,
        draftId: draft.id,
        changeKey: position.changeKey,
        anchor: draft.inline,
        createdOn: draft.created_on,
    };
}

function comparePosition(a: NavItem, b: NavItem): number {
    const fileA = a.fileIndex < 0 ? Number.MAX_SAFE_INTEGER : a.fileIndex;
    const fileB = b.fileIndex < 0 ? Number.MAX_SAFE_INTEGER : b.fileIndex;
    if (fileA !== fileB) {
        return fileA - fileB;
    }
    const lineA = a.line ?? Number.MAX_SAFE_INTEGER;
    const lineB = b.line ?? Number.MAX_SAFE_INTEGER;
    if (lineA !== lineB) {
        return lineA - lineB;
    }
    return a.createdOn.localeCompare(b.createdOn);
}

/**
 * One ordered list of everything a reviewer can jump to.
 *
 * Inline threads first (file order in the diff, then line), then outdated
 * threads, then general PR comments. Drafts are included and flagged, so a
 * draft anchored in a file sits next to the posted comments on that line.
 */
export function buildCommentNav(
    files: FileEntry[],
    comments: Comment[],
    drafts: Draft[],
    me: User | null = null,
): NavItem[] {
    const replyDrafts = new Map<number, Draft[]>();
    for (const draft of drafts) {
        if (draft.parent_id == null) {
            continue;
        }
        const list = replyDrafts.get(draft.parent_id);
        if (list) {
            list.push(draft);
            continue;
        }
        replyDrafts.set(draft.parent_id, [draft]);
    }

    const fileIndexByPath = new Map<string, number>();
    files.forEach((entry, index) => fileIndexByPath.set(entry.path, index));

    const inline: NavItem[] = [];
    const outdated: NavItem[] = [];
    const general: NavItem[] = [];
    const seenComments = new Set<number>();
    const seenDrafts = new Set<string>();
    const emptyIndex = {newLines: new Map<number, string>(), oldLines: new Map<number, string>()};

    files.forEach((entry, fileIndex) => {
        const index = buildLineIndex(entry.hunks);
        const groups = groupComments(comments, index, entry.path);
        for (const [key, threads] of groups.byKey) {
            for (const thread of threads) {
                seenComments.add(thread.root.id);
                const replyDraftCount = replyDrafts.get(thread.root.id)?.length ?? 0;
                inline.push(
                    commentItem(thread, replyDraftCount, me, {
                        kind: 'inline',
                        fileIndex,
                        changeKey: key,
                    }),
                );
            }
        }
        for (const thread of groups.outdated) {
            seenComments.add(thread.root.id);
            const replyDraftCount = replyDrafts.get(thread.root.id)?.length ?? 0;
            outdated.push(
                commentItem(thread, replyDraftCount, me, {
                    kind: 'outdated',
                    fileIndex,
                    changeKey: null,
                }),
            );
        }

        const draftGroups = groupDrafts(drafts, index, entry.path);
        for (const [key, list] of draftGroups.byKey) {
            for (const draft of list) {
                seenDrafts.add(draft.id);
                inline.push(
                    draftItem(draft, me, {kind: 'inline', fileIndex, changeKey: key}),
                );
            }
        }
        for (const draft of draftGroups.unmapped) {
            seenDrafts.add(draft.id);
            outdated.push(
                draftItem(draft, me, {kind: 'outdated', fileIndex, changeKey: null}),
            );
        }
    });

    // Comments anchored to a file that is no longer part of the diff.
    const generalComments = groupComments(comments, emptyIndex, '').general;
    for (const thread of generalComments) {
        seenComments.add(thread.root.id);
        const replyDraftCount = replyDrafts.get(thread.root.id)?.length ?? 0;
        general.push(
            commentItem(thread, replyDraftCount, me, {
                kind: 'general',
                fileIndex: -1,
                changeKey: null,
            }),
        );
    }
    for (const draft of drafts) {
        if (draft.parent_id != null || draft.inline) {
            continue;
        }
        seenDrafts.add(draft.id);
        general.push(draftItem(draft, me, {kind: 'general', fileIndex: -1, changeKey: null}));
    }

    for (const comment of comments) {
        if (comment.parent_id != null || seenComments.has(comment.id) || !comment.inline) {
            continue;
        }
        if (fileIndexByPath.has(comment.inline.path)) {
            continue;
        }
        const replies = comments.filter((item) => item.parent_id === comment.id);
        if (comment.deleted && replies.length === 0) {
            continue;
        }
        seenComments.add(comment.id);
        outdated.push(
            commentItem(
                {root: comment, replies},
                replyDrafts.get(comment.id)?.length ?? 0,
                me,
                {kind: 'outdated', fileIndex: -1, changeKey: null},
            ),
        );
    }
    for (const draft of drafts) {
        if (draft.parent_id != null || seenDrafts.has(draft.id) || !draft.inline) {
            continue;
        }
        if (fileIndexByPath.has(draft.inline.path)) {
            continue;
        }
        seenDrafts.add(draft.id);
        outdated.push(draftItem(draft, me, {kind: 'outdated', fileIndex: -1, changeKey: null}));
    }

    inline.sort(comparePosition);
    outdated.sort(comparePosition);
    general.sort((a, b) => a.createdOn.localeCompare(b.createdOn));

    return [...inline, ...outdated, ...general];
}

export function filterNavItems(items: NavItem[], filter: NavFilter): NavItem[] {
    if (filter === 'all') {
        return items;
    }
    if (filter === 'drafts') {
        return items.filter((item) => item.draft);
    }
    if (filter === 'mine') {
        return items.filter((item) => item.isMine);
    }
    return items.filter((item) => !item.isMine && !item.draft);
}

/** DOM id used for jumping / deep links. Comments keep Bitbucket's shape. */
export function navItemDomId(item: NavItem): string {
    if (item.draftId != null) {
        return `comment-draft-${item.draftId}`;
    }
    return `comment-${item.commentId}`;
}

export function navItemHash(item: NavItem): string {
    return `#${navItemDomId(item)}`;
}

export type CommentHash = {type: 'comment' | 'draft'; value: string};

/** Parse `#comment-123` (Bitbucket) and `#comment-draft-abc` links. */
export function parseCommentHash(hash: string): CommentHash | null {
    const match = /^#comment-(draft-)?([A-Za-z0-9_-]+)$/.exec(hash.trim());
    if (!match) {
        return null;
    }
    return {type: match[1] ? 'draft' : 'comment', value: match[2]!};
}

export function findNavItem(items: NavItem[], parsed: CommentHash): NavItem | null {
    if (parsed.type === 'draft') {
        return items.find((item) => item.draftId === parsed.value) ?? null;
    }
    const id = Number(parsed.value);
    if (!Number.isInteger(id)) {
        return null;
    }
    return items.find((item) => item.commentId === id) ?? null;
}
