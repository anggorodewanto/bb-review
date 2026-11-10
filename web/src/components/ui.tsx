import {useState} from 'react';
import type {ReactNode} from 'react';
import type {BuildState, ReviewState, User} from '../types';
import {initials} from '../lib/format';

export function Avatar({user, size = 24}: {user: User; size?: number}) {
    const [broken, setBroken] = useState(false);
    const style = {width: size, height: size, fontSize: Math.max(10, size * 0.4)};
    return (
        <span className="avatar" style={style} title={user.display_name}>
            <span className="avatar-initials">{initials(user.display_name || user.nickname)}</span>
            {user.avatar_url && !broken ? (
                <img
                    src={user.avatar_url}
                    alt=""
                    loading="lazy"
                    onError={() => setBroken(true)}
                />
            ) : null}
        </span>
    );
}

const REVIEW_LABEL: Record<ReviewState, string> = {
    approved: 'Approved',
    changes_requested: 'Changes requested',
    none: 'Awaiting review',
};

export function ReviewBadge({state}: {state: ReviewState}) {
    return <span className={`badge review-${state}`}>{REVIEW_LABEL[state]}</span>;
}

const BUILD_LABEL: Record<BuildState, string> = {
    SUCCESSFUL: 'Build successful',
    FAILED: 'Build failed',
    INPROGRESS: 'Build in progress',
    STOPPED: 'Build stopped',
    NONE: 'No build status',
};

export function BuildDot({state, label}: {state: BuildState; label?: string}) {
    return (
        <span
            className={`build-dot build-${state}`}
            title={label ?? BUILD_LABEL[state]}
            aria-label={label ?? BUILD_LABEL[state]}
            role="img"
        />
    );
}

export function Skeleton({rows = 3}: {rows?: number}) {
    return (
        <div className="skeleton-list" aria-hidden="true">
            {Array.from({length: rows}, (_, i) => (
                <div className="skeleton-row" key={i}>
                    <span className="skeleton-block w-40" />
                    <span className="skeleton-block w-80" />
                    <span className="skeleton-block w-20" />
                </div>
            ))}
        </div>
    );
}

export function ErrorState({
    message,
    onRetry,
}: {
    message: string;
    onRetry?: () => void;
}) {
    return (
        <div className="state state-error" role="alert">
            <p>{message}</p>
            {onRetry ? (
                <button type="button" className="btn" onClick={onRetry}>
                    Retry
                </button>
            ) : null}
        </div>
    );
}

export function EmptyState({title, hint}: {title: string; hint?: ReactNode}) {
    return (
        <div className="state state-empty">
            <p className="state-title">{title}</p>
            {hint ? <p className="state-hint">{hint}</p> : null}
        </div>
    );
}

export function Spinner({label = 'Loading'}: {label?: string}) {
    return (
        <span className="spinner" role="status" aria-label={label}>
            <span className="spinner-circle" />
        </span>
    );
}
