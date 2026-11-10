import {getChangeKey, isDelete, isInsert, isNormal} from 'react-diff-view';
import type {ChangeData, HunkData} from 'react-diff-view';
import type {Comment, Draft, InlineAnchor} from '../types';

/** change keys for both diff sides, keyed by absolute line number. */
export type LineIndex = {
    newLines: Map<number, string>;
    oldLines: Map<number, string>;
};

export type CommentThread = {
    root: Comment;
    replies: Comment[];
};

export type FileComments = {
    /** change key -> threads anchored to that rendered line */
    byKey: Map<string, CommentThread[]>;
    /** outdated flag set, or the anchor no longer exists in the diff */
    outdated: CommentThread[];
    general: CommentThread[];
};

export type DraftGroups = {
    /** change key -> drafts anchored to that rendered line */
    byKey: Map<string, Draft[]>;
    /** parent comment id -> reply drafts */
    replies: Map<number, Draft[]>;
    general: Draft[];
    /** inline drafts whose anchor is not part of the rendered diff */
    unmapped: Draft[];
};

export function buildLineIndex(hunks: HunkData[]): LineIndex {
    const newLines = new Map<number, string>();
    const oldLines = new Map<number, string>();

    for (const hunk of hunks) {
        for (const change of hunk.changes) {
            const key = getChangeKey(change);
            if (isInsert(change)) {
                newLines.set(change.lineNumber, key);
                continue;
            }
            if (isDelete(change)) {
                oldLines.set(change.lineNumber, key);
                continue;
            }
            newLines.set(change.newLineNumber, key);
            oldLines.set(change.oldLineNumber, key);
        }
    }

    return {newLines, oldLines};
}

export function anchorChangeKey(index: LineIndex, anchor: InlineAnchor | null): string | null {
    if (!anchor) {
        return null;
    }
    if (anchor.to != null) {
        return index.newLines.get(anchor.to) ?? null;
    }
    if (anchor.from != null) {
        return index.oldLines.get(anchor.from) ?? null;
    }
    return null;
}

/** The anchor a new comment should use for a clicked change. */
export function anchorForChange(
    path: string,
    change: ChangeData,
    side: 'old' | 'new',
): InlineAnchor {
    if (isInsert(change)) {
        return {path, from: null, to: change.lineNumber};
    }
    if (isDelete(change)) {
        return {path, from: change.lineNumber, to: null};
    }
    if (isNormal(change)) {
        return side === 'old'
            ? {path, from: change.oldLineNumber, to: null}
            : {path, from: null, to: change.newLineNumber};
    }
    return {path, from: null, to: null};
}

const byCreated = <T extends {created_on: string}>(a: T, b: T) =>
    a.created_on.localeCompare(b.created_on);

function pushThread(map: Map<string, CommentThread[]>, key: string, thread: CommentThread): void {
    const list = map.get(key);
    if (list) {
        list.push(thread);
        return;
    }
    map.set(key, [thread]);
}

/**
 * Group the comments of a single file into inline widgets, outdated ones and
 * general (PR-level) ones. `comments` may contain other files' comments; they
 * are ignored here.
 */
export function groupComments(
    comments: Comment[],
    index: LineIndex,
    path: string,
): FileComments {
    const repliesByParent = new Map<number, Comment[]>();
    for (const comment of comments) {
        if (comment.parent_id == null) {
            continue;
        }
        const list = repliesByParent.get(comment.parent_id);
        if (list) {
            list.push(comment);
            continue;
        }
        repliesByParent.set(comment.parent_id, [comment]);
    }

    const byKey = new Map<string, CommentThread[]>();
    const outdated: CommentThread[] = [];
    const general: CommentThread[] = [];

    for (const comment of comments) {
        if (comment.parent_id != null) {
            continue;
        }
        const replies = (repliesByParent.get(comment.id) ?? []).sort(byCreated);
        if (comment.deleted && replies.length === 0) {
            continue;
        }
        const thread: CommentThread = {root: comment, replies};

        if (!comment.inline) {
            general.push(thread);
            continue;
        }
        if (comment.inline.path !== path) {
            continue;
        }
        if (comment.outdated) {
            outdated.push(thread);
            continue;
        }

        const key = anchorChangeKey(index, comment.inline);
        if (!key) {
            outdated.push(thread);
            continue;
        }
        pushThread(byKey, key, thread);
    }

    for (const list of byKey.values()) {
        list.sort((a, b) => byCreated(a.root, b.root));
    }

    return {byKey, outdated, general};
}

export function groupDrafts(drafts: Draft[], index: LineIndex, path: string): DraftGroups {
    const byKey = new Map<string, Draft[]>();
    const replies = new Map<number, Draft[]>();
    const general: Draft[] = [];
    const unmapped: Draft[] = [];

    for (const draft of drafts) {
        if (draft.parent_id != null) {
            const list = replies.get(draft.parent_id);
            if (list) {
                list.push(draft);
                continue;
            }
            replies.set(draft.parent_id, [draft]);
            continue;
        }
        if (!draft.inline) {
            general.push(draft);
            continue;
        }
        if (draft.inline.path !== path) {
            continue;
        }
        const key = anchorChangeKey(index, draft.inline);
        if (!key) {
            unmapped.push(draft);
            continue;
        }
        const list = byKey.get(key);
        if (list) {
            list.push(draft);
            continue;
        }
        byKey.set(key, [draft]);
    }

    return {byKey, replies, general, unmapped};
}

/** Non-deleted inline comment count per file path, for the file tree. */
export function commentCounts(comments: Comment[]): Record<string, number> {
    const counts: Record<string, number> = {};
    for (const comment of comments) {
        if (!comment.inline || comment.deleted) {
            continue;
        }
        const path = comment.inline.path;
        counts[path] = (counts[path] ?? 0) + 1;
    }
    return counts;
}
