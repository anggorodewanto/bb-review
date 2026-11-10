import {useState} from 'react';
import type {Draft} from '../types';
import type {CommentThread as Thread} from '../lib/comments';
import type {ThreadActions} from './CommentThread';
import {CommentThread, DraftCard} from './CommentThread';
import {CommentEditor} from './CommentEditor';
import type {CommentMode} from './CommentEditor';

export type GeneralCommentsProps = {
    threads: Thread[];
    drafts: Draft[];
    draftReplies: Map<number, Draft[]>;
    actions: ThreadActions;
    onAdd: (raw: string, mode: CommentMode) => Promise<void>;
};

export function GeneralComments({
    threads,
    drafts,
    draftReplies,
    actions,
    onAdd,
}: GeneralCommentsProps) {
    const [editing, setEditing] = useState(false);
    const count = threads.length + drafts.length;

    return (
        <section className="general-comments" id="general-comments">
            <div className="section-head">
                <h2>General comments</h2>
                <span className="muted">{count === 0 ? 'none yet' : count}</span>
            </div>

            {threads.map((thread) => (
                <CommentThread
                    key={`general-${thread.root.id}`}
                    thread={thread}
                    replyDrafts={draftReplies.get(thread.root.id) ?? []}
                    actions={actions}
                />
            ))}

            {drafts.map((draft) => (
                <DraftCard key={`general-draft-${draft.id}`} draft={draft} actions={actions} />
            ))}

            {editing ? (
                <CommentEditor
                    placeholder="Leave a comment on the PR…"
                    onSubmit={async (raw, mode) => {
                        await onAdd(raw, mode);
                        setEditing(false);
                    }}
                    onCancel={() => setEditing(false)}
                />
            ) : (
                <button type="button" className="btn" onClick={() => setEditing(true)}>
                    Add general comment
                </button>
            )}
        </section>
    );
}
