import {useMemo, useState} from 'react';
import type {Comment, Draft} from '../types';
import type {CommentThread as Thread} from '../lib/comments';
import {relativeAge, isoTitle} from '../lib/format';
import {sanitizeHtml} from '../lib/sanitize';
import {Avatar} from './ui';
import {CommentEditor} from './CommentEditor';
import type {CommentMode} from './CommentEditor';

export type DraftActions = {
    onUpdateDraft: (draft: Draft, raw: string) => Promise<void>;
    onDeleteDraft: (draft: Draft) => Promise<void>;
};

export type ThreadActions = DraftActions & {
    onReply: (parent: Comment, raw: string, mode: CommentMode) => Promise<void>;
};

function CommentBody({html}: {html: string}) {
    const safe = useMemo(() => sanitizeHtml(html), [html]);
    if (!safe) {
        return null;
    }
    return <div className="comment-body" dangerouslySetInnerHTML={{__html: safe}} />;
}

function CommentCard({comment}: {comment: Comment}) {
    if (comment.deleted) {
        return <p className="comment-deleted">Comment deleted</p>;
    }
    return (
        <article className="comment">
            <div className="comment-head">
                <Avatar user={comment.author} size={22} />
                <span className="comment-author">{comment.author.display_name}</span>
                <time className="comment-time" dateTime={comment.created_on} title={isoTitle(comment.created_on)}>
                    {relativeAge(comment.created_on)}
                </time>
                {comment.updated_on !== comment.created_on ? (
                    <span className="comment-edited" title={isoTitle(comment.updated_on)}>
                        edited
                    </span>
                ) : null}
            </div>
            <CommentBody html={comment.html} />
        </article>
    );
}

export function DraftCard({
    draft,
    actions,
    id,
}: {
    draft: Draft;
    actions: DraftActions;
    id?: string;
}) {
    const [editing, setEditing] = useState(false);
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState<string | null>(null);

    async function save(raw: string) {
        setBusy(true);
        setError(null);
        try {
            await actions.onUpdateDraft(draft, raw);
            setEditing(false);
        } catch (err) {
            setError(err instanceof Error ? err.message : 'Could not update the draft');
        } finally {
            setBusy(false);
        }
    }

    async function remove() {
        setBusy(true);
        setError(null);
        try {
            await actions.onDeleteDraft(draft);
        } catch (err) {
            setError(err instanceof Error ? err.message : 'Could not delete the draft');
            setBusy(false);
        }
    }

    if (editing) {
        return (
            <div className="comment comment-draft">
                <CommentEditor
                    initialValue={draft.raw}
                    primaryLabel="Save draft"
                    secondaryLabel={null}
                    onSubmit={(raw) => save(raw)}
                    onCancel={() => setEditing(false)}
                />
            </div>
        );
    }

    return (
        <article className="comment comment-draft" id={id}>
            <div className="comment-head">
                <span className="badge badge-draft">Draft</span>
                <span className="comment-hint">only visible to you</span>
            </div>
            <div className="comment-body comment-plain">{draft.raw}</div>
            {error ? <p className="form-error">{error}</p> : null}
            <div className="comment-actions">
                <button type="button" className="btn btn-small" onClick={() => setEditing(true)} disabled={busy}>
                    Edit
                </button>
                <button type="button" className="btn btn-small btn-ghost" onClick={() => void remove()} disabled={busy}>
                    Delete
                </button>
            </div>
        </article>
    );
}

export function CommentThread({
    thread,
    replyDrafts,
    actions,
    id,
}: {
    thread: Thread;
    replyDrafts: Draft[];
    actions: ThreadActions;
    id?: string;
}) {
    const [replying, setReplying] = useState(false);

    async function reply(raw: string, mode: CommentMode) {
        await actions.onReply(thread.root, raw, mode);
        setReplying(false);
    }

    return (
        <div className="thread" id={id}>
            <CommentCard comment={thread.root} />
            {thread.replies.length > 0 || replyDrafts.length > 0 ? (
                <div className="thread-replies">
                    {thread.replies.map((comment) => (
                        <CommentCard key={comment.id} comment={comment} />
                    ))}
                    {replyDrafts.map((draft) => (
                        <DraftCard key={draft.id} draft={draft} actions={actions} />
                    ))}
                </div>
            ) : null}
            {replying ? (
                <CommentEditor
                    primaryLabel="Add to review"
                    onSubmit={reply}
                    onCancel={() => setReplying(false)}
                />
            ) : (
                <button
                    type="button"
                    className="btn btn-small btn-ghost thread-reply"
                    onClick={() => setReplying(true)}
                >
                    Reply
                </button>
            )}
        </div>
    );
}
