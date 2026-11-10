import {useMemo} from 'react';
import type {PRDetail, ReviewState} from '../types';
import {relativeAge, isoTitle, plural, shortSha} from '../lib/format';
import {sanitizeHtml} from '../lib/sanitize';
import {Avatar, BuildDot, ReviewBadge} from './ui';
import {CommandButton} from './CommandHost';

const STATE_GLYPH: Record<ReviewState, string> = {
    approved: '✓',
    changes_requested: '✗',
    none: '·',
};

export type PRHeaderProps = {
    pr: PRDetail;
    busy: boolean;
    onApprove: () => void;
    onRequestChanges: () => void;
    onBack: () => void;
    /** Total navigable comment threads (falls back to the PR comment count). */
    commentTotal?: number;
    onOpenComments?: () => void;
};

export function PRHeader({
    pr,
    busy,
    onApprove,
    onRequestChanges,
    onBack,
    commentTotal,
    onOpenComments,
}: PRHeaderProps) {
    const description = useMemo(() => sanitizeHtml(pr.description_html), [pr.description_html]);
    const approved = pr.my_state === 'approved';
    const changes = pr.my_state === 'changes_requested';
    const reviewers = pr.participants.filter((p) => p.role === 'REVIEWER');

    return (
        <header className="pr-head">
            <div className="pr-title-row">
                <button
                    type="button"
                    className="icon-btn back-link"
                    onClick={onBack}
                    title="Back to inbox"
                    aria-label="Back to inbox"
                >
                    ←
                </button>
                <h1 className="pr-title">{pr.title}</h1>
                <a className="external-link" href={pr.url} target="_blank" rel="noopener noreferrer">
                    bitbucket.org ↗
                </a>
                <CommandButton />
            </div>

            <div className="pr-meta">
                <Avatar user={pr.author} size={22} />
                <span className="pr-author">{pr.author.display_name}</span>
                <span className="sep">·</span>
                <code className="branch">{pr.source_branch}</code>
                <span aria-hidden="true">→</span>
                <code className="branch">{pr.dest_branch}</code>
                <span className="sep">·</span>
                <span title={isoTitle(pr.created_on)}>opened {relativeAge(pr.created_on)}</span>
                <span className="sep">·</span>
                {onOpenComments ? (
                    <button
                        type="button"
                        className="btn btn-small btn-ghost pr-comments-btn"
                        data-command="toggle-comments"
                        onClick={onOpenComments}
                        title="Comment threads (C)"
                    >
                        💬 {commentTotal ?? pr.comment_count}
                    </button>
                ) : (
                    <span>{plural(pr.comment_count, 'comment')}</span>
                )}
                {pr.task_count > 0 ? (
                    <>
                        <span className="sep">·</span>
                        <span>{plural(pr.task_count, 'task')}</span>
                    </>
                ) : null}
                <span className="sep">·</span>
                <code className="sha" title={`${pr.merge_base}..${pr.source_commit}`}>
                    {shortSha(pr.merge_base)}..{shortSha(pr.source_commit)}
                </code>
            </div>

            <div className="pr-status-row">
                <div className="pr-builds">
                    {pr.builds.length === 0 ? (
                        <BuildDot state={pr.build_state} />
                    ) : (
                        pr.builds.map((build) =>
                            build.url ? (
                                <a
                                    key={build.key}
                                    className="build-link"
                                    href={build.url}
                                    target="_blank"
                                    rel="noopener noreferrer"
                                    title={build.name}
                                >
                                    <BuildDot state={build.state} label={build.name} />
                                    <span>{build.name}</span>
                                </a>
                            ) : (
                                <span key={build.key} className="build-link" title={build.name}>
                                    <BuildDot state={build.state} label={build.name} />
                                    <span>{build.name}</span>
                                </span>
                            ),
                        )
                    )}
                </div>

                <div className="pr-reviewers">
                    {reviewers.map((participant) => (
                        <span
                            key={participant.user.uuid}
                            className={`reviewer reviewer-${participant.state}`}
                            title={`${participant.user.display_name}: ${participant.state}`}
                        >
                            <Avatar user={participant.user} size={18} />
                            <span className="reviewer-name">{participant.user.display_name}</span>
                            <span className="reviewer-glyph" aria-hidden="true">
                                {STATE_GLYPH[participant.state]}
                            </span>
                        </span>
                    ))}
                    {pr.approvals > 0 ? (
                        <span className="badge badge-ok">{pr.approvals} approval{pr.approvals === 1 ? '' : 's'}</span>
                    ) : null}
                    <ReviewBadge state={pr.my_state} />
                </div>

                <div className="pr-actions">
                    <button
                        type="button"
                        className={`btn btn-approve${approved ? ' is-active' : ''}`}
                        data-command="approve"
                        aria-pressed={approved}
                        onClick={onApprove}
                        disabled={busy}
                        title="Toggle approval (a)"
                    >
                        {approved ? 'Approved ✓' : 'Approve'}
                    </button>
                    <button
                        type="button"
                        className={`btn btn-changes${changes ? ' is-active' : ''}`}
                        aria-pressed={changes}
                        onClick={onRequestChanges}
                        disabled={busy}
                    >
                        {changes ? 'Changes requested' : 'Request changes'}
                    </button>
                </div>
            </div>

            {description ? (
                <details className="pr-description">
                    <summary>Description</summary>
                    <div className="markdown-body" dangerouslySetInnerHTML={{__html: description}} />
                </details>
            ) : null}
        </header>
    );
}
