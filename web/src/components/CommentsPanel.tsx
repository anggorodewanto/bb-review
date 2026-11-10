import {useMemo, useState} from 'react';
import type {NavFilter, NavItem} from '../lib/commentNav';
import {filterNavItems, shortPath} from '../lib/commentNav';
import {plural} from '../lib/format';
import {Avatar} from './ui';

const FILTERS: Array<{value: NavFilter; label: string}> = [
    {value: 'all', label: 'All'},
    {value: 'others', label: "Others'"},
    {value: 'mine', label: 'Mine'},
    {value: 'drafts', label: 'Drafts'},
];

export type CommentsPanelProps = {
    items: NavItem[];
    activeId: string | null;
    onSelect: (item: NavItem) => void;
    onClose: () => void;
};

export function CommentsPanel({items, activeId, onSelect, onClose}: CommentsPanelProps) {
    const [filter, setFilter] = useState<NavFilter>('all');
    const visible = useMemo(() => filterNavItems(items, filter), [items, filter]);

    return (
        <aside className="comments-panel" aria-label="Comment threads">
            <div className="comments-panel-head">
                <span className="comments-panel-title">
                    Comments <span className="muted">{items.length}</span>
                </span>
                <button type="button" className="btn btn-small btn-ghost" onClick={onClose}>
                    Close
                </button>
            </div>

            <div className="comments-filters" role="group" aria-label="Filter comments">
                {FILTERS.map(({value, label}) => (
                    <button
                        key={value}
                        type="button"
                        className={`chip${filter === value ? ' is-active' : ''}`}
                        aria-pressed={filter === value}
                        onClick={() => setFilter(value)}
                    >
                        {label}
                    </button>
                ))}
            </div>

            {visible.length === 0 ? (
                <p className="comments-empty">
                    {items.length === 0 ? 'No comments on this pull request yet.' : 'No comments match this filter.'}
                </p>
            ) : (
                <ul className="comments-list">
                    {visible.map((item) => (
                        <li key={item.id}>
                            <button
                                type="button"
                                className={`comment-nav-item${item.id === activeId ? ' is-active' : ''}`}
                                onClick={() => onSelect(item)}
                            >
                                <span className="comment-nav-loc">
                                    <span className="comment-nav-path" title={item.path || 'General comment'}>
                                        {shortPath(item.path)}
                                    </span>
                                    {item.line != null ? (
                                        <span className="comment-nav-line">:{item.line}</span>
                                    ) : null}
                                </span>
                                <span className="comment-nav-head">
                                    {item.author ? <Avatar user={item.author} size={16} /> : null}
                                    <span className="comment-nav-author">{item.authorName}</span>
                                    {item.replyCount > 0 ? (
                                        <span className="comment-nav-replies">
                                            {plural(item.replyCount, 'reply', 'replies')}
                                        </span>
                                    ) : null}
                                </span>
                                <span className="comment-nav-excerpt">{item.excerpt}</span>
                                <span className="comment-nav-badges">
                                    {item.draft ? <span className="badge badge-draft">Draft</span> : null}
                                    {item.kind === 'outdated' ? (
                                        <span className="badge badge-warn">Outdated</span>
                                    ) : null}
                                    {item.kind === 'general' ? (
                                        <span className="badge">General</span>
                                    ) : null}
                                </span>
                            </button>
                        </li>
                    ))}
                </ul>
            )}
        </aside>
    );
}
