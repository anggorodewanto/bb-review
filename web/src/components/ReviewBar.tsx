import type {Draft} from '../types';
import {Spinner} from './ui';

export type SubmitAction = 'comment' | 'approve' | 'request_changes';

export type ReviewBarProps = {
    draftCount: number;
    busy: boolean;
    open: boolean;
    onOpenChange: (open: boolean) => void;
    onSelect: (action: SubmitAction) => void;
    failures: Array<{draft: Draft; error: string}>;
    onDismissFailures: () => void;
};

export function ReviewBar({
    draftCount,
    busy,
    open,
    onOpenChange,
    onSelect,
    failures,
    onDismissFailures,
}: ReviewBarProps) {
    const label = draftCount > 0 ? `${draftCount} draft${draftCount === 1 ? '' : 's'}` : 'No drafts';

    return (
        <div className={`review-bar${failures.length > 0 ? ' has-failures' : ''}`}>
            {failures.length > 0 ? (
                <div className="review-failures" role="alert">
                    <div className="review-failures-head">
                        <strong>{failures.length} draft{failures.length === 1 ? '' : 's'} failed to post</strong>
                        <button type="button" className="btn btn-small btn-ghost" onClick={onDismissFailures}>
                            Dismiss
                        </button>
                    </div>
                    <ul>
                        {failures.map(({draft, error}) => (
                            <li key={draft.id}>
                                <span className="failure-text">{draft.raw.slice(0, 120)}</span>
                                <span className="failure-error">{error}</span>
                            </li>
                        ))}
                    </ul>
                </div>
            ) : null}

            <div className="review-bar-main">
                <button
                    type="button"
                    className="btn btn-primary review-submit"
                    aria-expanded={open}
                    aria-haspopup="menu"
                    disabled={busy}
                    onClick={() => onOpenChange(!open)}
                >
                    {busy ? <Spinner label="Submitting" /> : null}
                    {label} · Submit review <span aria-hidden="true">▾</span>
                </button>
                {open ? (
                    <div className="review-menu" role="menu">
                        <button type="button" role="menuitem" onClick={() => onSelect('comment')} disabled={busy}>
                            Comment
                            <span className="menu-hint">Post drafts without a review state</span>
                        </button>
                        <button type="button" role="menuitem" onClick={() => onSelect('approve')} disabled={busy}>
                            Approve
                            <span className="menu-hint">Post drafts and approve the PR</span>
                        </button>
                        <button
                            type="button"
                            role="menuitem"
                            onClick={() => onSelect('request_changes')}
                            disabled={busy}
                        >
                            Request changes
                            <span className="menu-hint">Post drafts and request changes</span>
                        </button>
                    </div>
                ) : null}
            </div>
        </div>
    );
}
