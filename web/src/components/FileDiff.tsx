import {memo, useCallback, useEffect, useMemo, useRef, useState} from 'react';
import type {ReactElement, ReactNode} from 'react';
import {
    Diff,
    Hunk,
    expandFromRawCode,
    getChangeKey,
    tokenize,
} from 'react-diff-view';
import type {ChangeData, HunkData, ViewType} from 'react-diff-view';
import {refractor} from 'refractor/lib/common.js';
import type {Comment, Draft} from '../types';
import type {FileEntry} from '../lib/diff';
import {lineContainsChangeKey} from '../lib/diff';
import {adjacentGap, gapLayout, gapSpecFor, hunkBounds} from '../lib/expand';
import type {
    AdjacentGap,
    ExpandDirection,
    FocusPosition,
    GapControl,
} from '../lib/expand';
import type {ViewedStatus} from '../lib/viewed';
import {buildLineIndex, groupComments, groupDrafts} from '../lib/comments';
import {languageForPath, plural} from '../lib/format';
import type {ThreadActions} from './CommentThread';
import {CommentThread, DraftCard} from './CommentThread';
import {CommentEditor} from './CommentEditor';
import type {CommentMode} from './CommentEditor';

const LARGE_FILE_THRESHOLD = 1500;

export type FileDiffProps = {
    entry: FileEntry;
    index: number;
    viewType: ViewType;
    /** Wrap long lines; when false the diff scrolls horizontally. */
    wrapLines: boolean;
    focusedKey: string | null;
    comments: Comment[];
    drafts: Draft[];
    draftReplies: Map<number, Draft[]>;
    actions: ThreadActions;
    editorKey: string | null;
    onEditorSubmit: (key: string, raw: string, mode: CommentMode) => Promise<void>;
    onEditorCancel: () => void;
    viewed: ViewedStatus;
    onToggleViewed: (entry: FileEntry) => void;
    onLineClick: (entry: FileEntry, index: number, change: ChangeData, side: 'old' | 'new') => void;
    onLoadSources: (entry: FileEntry) => Promise<{old: string; new: string} | null>;
    registerRef: (index: number, el: HTMLElement | null) => void;
    /** Bumped by a comment jump; forces this file to render and expand. */
    reveal: number | null;
    /** Keyboard request (`e` / `E`) for the focused line in this file. */
    expand: {nonce: number; all: boolean} | null;
};

function changeId(index: number, change: ChangeData): string {
    return `change-${index}-${getChangeKey(change)}`;
}

function oldLineCount(source: string): number {
    if (!source) {
        return 0;
    }
    const lines = source.split('\n');
    return source.endsWith('\n') ? lines.length - 1 : lines.length;
}

type ExpandRowProps = {
    colSpan: number;
    controls: GapControl[];
    busy: boolean;
    onExpand: (direction: ExpandDirection) => void;
};

function expandLabel(control: GapControl): string {
    if (!control.range) {
        return '↓ Expand below';
    }
    if (control.direction === 'all') {
        return `↕ Expand all ${control.range.count} lines`;
    }
    const arrow = control.direction === 'up' ? '↑' : '↓';
    return `${arrow} ${control.range.start}–${control.range.end}`;
}

function ExpandRow({colSpan, controls, busy, onExpand}: ExpandRowProps) {
    return (
        <tbody className="diff-expand">
            <tr>
                <td colSpan={colSpan}>
                    <div className={`expand-controls${controls.length > 1 ? ' is-split' : ''}`}>
                        {controls.map((control, i) => (
                            <button
                                key={`${control.direction}-${i}`}
                                type="button"
                                className="expand-btn"
                                data-command={
                                    control.direction === 'all'
                                        ? 'expand-context-all'
                                        : 'expand-context'
                                }
                                onClick={() => onExpand(control.direction)}
                                disabled={busy}
                                title={
                                    control.range
                                        ? `Lines ${control.range.start}–${control.range.end}`
                                        : 'Expand below'
                                }
                            >
                                {expandLabel(control)}
                            </button>
                        ))}
                    </div>
                </td>
            </tr>
        </tbody>
    );
}

function FileDiffInner({
    entry,
    index,
    viewType,
    wrapLines,
    focusedKey,
    comments,
    drafts,
    draftReplies,
    actions,
    editorKey,
    onEditorSubmit,
    onEditorCancel,
    viewed,
    onToggleViewed,
    onLineClick,
    onLoadSources,
    registerRef,
    reveal,
    expand,
}: FileDiffProps) {
    const sectionRef = useRef<HTMLElement | null>(null);
    const [active, setActive] = useState(false);
    const [loadedLarge, setLoadedLarge] = useState(false);
    const [expandedHunks, setExpandedHunks] = useState<HunkData[] | null>(null);
    const [sources, setSources] = useState<{old: string; new: string} | null>(null);
    const [expanding, setExpanding] = useState(false);
    const [expandError, setExpandError] = useState<string | null>(null);

    const setSectionRef = useCallback(
        (el: HTMLElement | null) => {
            sectionRef.current = el;
            registerRef(index, el);
        },
        [index, registerRef],
    );

    // Render the diff only when the (ever growing) page approaches it.
    useEffect(() => {
        if (active) {
            return;
        }
        const el = sectionRef.current;
        if (!el || typeof IntersectionObserver === 'undefined') {
            setActive(true);
            return;
        }
        const observer = new IntersectionObserver(
            (entries) => {
                if (entries.some((e) => e.isIntersecting)) {
                    setActive(true);
                    observer.disconnect();
                }
            },
            {rootMargin: '800px 0px'},
        );
        observer.observe(el);
        return () => observer.disconnect();
    }, [active]);

    // A comment jump can land in a file that has not been rendered (lazy
    // IntersectionObserver) or is a collapsed large file. Reveal it eagerly.
    useEffect(() => {
        if (reveal == null) {
            return;
        }
        setActive(true);
        setLoadedLarge(true);
    }, [reveal]);

    const hunks = expandedHunks ?? entry.hunks;
    const lineIndex = useMemo(() => buildLineIndex(hunks), [hunks]);
    const groups = useMemo(
        () => groupComments(comments, lineIndex, entry.path),
        [comments, lineIndex, entry.path],
    );
    const draftGroups = useMemo(
        () => groupDrafts(drafts, lineIndex, entry.path),
        [drafts, lineIndex, entry.path],
    );

    const language = languageForPath(entry.path);
    const large = entry.changedLines > LARGE_FILE_THRESHOLD;
    const showDiff =
        active && hunks.length > 0 && !entry.isBinary && (!large || loadedLarge);

    const tokens = useMemo(() => {
        if (!showDiff || !language) {
            return null;
        }
        try {
            return tokenize(hunks, {highlight: true, refractor, language});
        } catch {
            return null;
        }
    }, [showDiff, hunks, language]);

    const widgets = useMemo(() => {
        const byKey = new Map<string, ReactNode[]>();
        const add = (key: string, node: ReactNode) => {
            const list = byKey.get(key);
            if (list) {
                list.push(node);
                return;
            }
            byKey.set(key, [node]);
        };

        for (const [key, threads] of groups.byKey) {
            for (const thread of threads) {
                add(
                    key,
                    <CommentThread
                        key={`comment-${thread.root.id}`}
                        id={`comment-${thread.root.id}`}
                        thread={thread}
                        replyDrafts={draftReplies.get(thread.root.id) ?? []}
                        actions={actions}
                    />,
                );
            }
        }
        for (const [key, keyDrafts] of draftGroups.byKey) {
            for (const draft of keyDrafts) {
                add(
                    key,
                    <DraftCard
                        key={`draft-${draft.id}`}
                        id={`comment-draft-${draft.id}`}
                        draft={draft}
                        actions={actions}
                    />,
                );
            }
        }
        if (editorKey) {
            add(
                editorKey,
                <CommentEditor
                    key="inline-editor"
                    onSubmit={(raw, mode) => onEditorSubmit(editorKey, raw, mode)}
                    onCancel={onEditorCancel}
                />,
            );
        }

        const result: Record<string, ReactNode> = {};
        for (const [key, nodes] of byKey) {
            result[key] = <div className="comment-widget">{nodes}</div>;
        }
        return result;
    }, [groups, draftGroups, draftReplies, actions, editorKey, onEditorSubmit, onEditorCancel]);

    async function expandGap(gap: AdjacentGap, direction: ExpandDirection) {
        if (expanding) {
            return;
        }
        setExpanding(true);
        setExpandError(null);
        try {
            let loaded = sources;
            if (!loaded) {
                loaded = await onLoadSources(entry);
                if (!loaded) {
                    setExpandError('Could not load the file contents');
                    return;
                }
                setSources(loaded);
            }
            const total = oldLineCount(loaded.old);
            const spec = gapSpecFor(gap, hunkBounds(hunks), total);
            if (!spec) {
                return;
            }
            const layout = gapLayout(spec);
            if (!layout) {
                return;
            }
            const range =
                direction === 'all'
                    ? layout.gap
                    : (layout.controls.find((control) => control.direction === direction)?.range ??
                      layout.gap);
            if (!range) {
                return;
            }
            // expandFromRawCode takes a half-open [start, end) range.
            setExpandedHunks(
                expandFromRawCode(hunks, loaded.old.split('\n'), range.start, range.end + 1),
            );
        } catch (err) {
            setExpandError(err instanceof Error ? err.message : 'Could not expand the context');
        } finally {
            setExpanding(false);
        }
    }

    function findFocusedChange(
        list: HunkData[],
        key: string | null,
    ): {hunkIndex: number; position: FocusPosition} | null {
        if (!key) {
            return null;
        }
        for (let i = 0; i < list.length; i += 1) {
            const changes = list[i]?.changes ?? [];
            const at = changes.findIndex((change) =>
                change ? getChangeKey(change) === key : false,
            );
            if (at < 0) {
                continue;
            }
            if (changes.length === 1) {
                return {hunkIndex: i, position: 'only'};
            }
            if (at === 0) {
                return {hunkIndex: i, position: 'first'};
            }
            if (at === changes.length - 1) {
                return {hunkIndex: i, position: 'last'};
            }
            return {hunkIndex: i, position: 'middle'};
        }
        return null;
    }

    function expandAtFocus(all: boolean) {
        const found = findFocusedChange(hunks, focusedKey);
        if (!found) {
            return;
        }
        const bounds = hunkBounds(hunks);
        const gap = adjacentGap(bounds, found.hunkIndex, found.position);
        if (!gap) {
            return;
        }
        if (all) {
            void expandGap(gap, 'all');
            return;
        }
        const total = sources ? oldLineCount(sources.old) : null;
        const spec = gapSpecFor(gap, bounds, total);
        const layout = spec ? gapLayout(spec) : null;
        const control =
            layout?.controls.find((item) => item.direction === gap.direction) ??
            layout?.controls[0];
        void expandGap(gap, control?.direction ?? 'all');
    }

    // `e` / `E` from the command registry (only the focused file gets a nonce).
    useEffect(() => {
        if (!expand) {
            return;
        }
        expandAtFocus(expand.all);
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [expand?.nonce]);

    const gutterClick = ({change, side}: {change: ChangeData | null; side?: 'old' | 'new'}) => {
        if (!change) {
            return;
        }
        onLineClick(entry, index, change, side ?? 'new');
    };

    const generateAnchorID = useCallback(
        (change: ChangeData) => changeId(index, change),
        [index],
    );

    const generateLineClassName = useCallback(
        ({changes, defaultGenerate}: {changes: ChangeData[]; defaultGenerate: () => string}) => {
            // Split rows pair old/new changes; one side is null for pure inserts/deletes.
            if (!lineContainsChangeKey(changes, focusedKey)) {
                return defaultGenerate();
            }
            return `${defaultGenerate()} bbr-line-focus`;
        },
        [focusedKey],
    );

    function renderHunks(rendered: HunkData[]): ReactElement[] {
        const rows: ReactElement[] = [];
        const colSpan = viewType === 'split' ? 4 : 3;
        const bounds = hunkBounds(rendered);

        function pushGap(key: string, gap: AdjacentGap, controls: GapControl[]): void {
            const primary = controls.filter((control) => control.direction !== 'all');
            const all = controls.filter((control) => control.direction === 'all');
            if (primary.length > 0) {
                rows.push(
                    <ExpandRow
                        key={key}
                        colSpan={colSpan}
                        controls={primary}
                        busy={expanding}
                        onExpand={(direction) => void expandGap(gap, direction)}
                    />,
                );
            }
            for (const control of all) {
                rows.push(
                    <ExpandRow
                        key={`${key}-all`}
                        colSpan={colSpan}
                        controls={[control]}
                        busy={expanding}
                        onExpand={() => void expandGap(gap, 'all')}
                    />,
                );
            }
        }

        const firstBounds = bounds[0];
        if (firstBounds && firstBounds.start > 1) {
            const layout = gapLayout({kind: 'top', firstStart: firstBounds.start});
            if (layout) {
                pushGap('expand-top', {kind: 'top', anchor: 0, direction: 'up'}, layout.controls);
            }
        }

        rendered.forEach((hunk, i) => {
            rows.push(<Hunk key={`hunk-${hunk.oldStart}-${hunk.newStart}-${i}`} hunk={hunk} />);

            const previous = bounds[i];
            const nextBounds = bounds[i + 1];
            if (nextBounds) {
                const previousEnd = previous?.end ?? 0;
                if (nextBounds.start > previousEnd + 1) {
                    const layout = gapLayout({
                        kind: 'middle',
                        prevEnd: previousEnd,
                        nextStart: nextBounds.start,
                    });
                    if (layout) {
                        pushGap(
                            `expand-${i}`,
                            {kind: 'middle', anchor: i, direction: 'down'},
                            layout.controls,
                        );
                    }
                }
                return;
            }

            if (!previous) {
                return;
            }
            const layout = gapLayout({
                kind: 'bottom',
                lastEnd: previous.end,
                total: sources ? oldLineCount(sources.old) : null,
            });
            if (layout) {
                pushGap(
                    'expand-bottom',
                    {kind: 'bottom', anchor: bounds.length - 1, direction: 'down'},
                    layout.controls,
                );
            }
        });

        return rows;
    }

    // Both views follow the "Wrap lines" preference. With wrapping off, the
    // table grows to the longest line and scrolls inside .diff-scroll.
    const wrap = wrapLines;

    const statusLabel =
        entry.type === 'add'
            ? 'Added'
            : entry.type === 'delete'
              ? 'Deleted'
              : entry.type === 'rename'
                ? 'Renamed'
                : entry.type === 'copy'
                  ? 'Copied'
                  : 'Modified';

    return (
        <section
            id={`file-${index}`}
            ref={setSectionRef}
            className={`file-block viewed-${viewed}`}
            data-file-index={index}
        >
            <header className="file-block-head">
                <div className="file-block-title">
                    <span className={`tag file-type-${entry.type}`}>{statusLabel}</span>
                    <span className="file-block-path">{entry.path}</span>
                    {entry.type === 'rename' && entry.oldPath ? (
                        <span className="file-old-path">from {entry.oldPath}</span>
                    ) : null}
                </div>
                <div className="file-block-meta">
                    <span className="diffstat">
                        {entry.additions > 0 ? <span className="add">+{entry.additions}</span> : null}
                        {entry.deletions > 0 ? <span className="del">-{entry.deletions}</span> : null}
                    </span>
                    {viewed === 'changed' ? (
                        <span className="tag tag-warn" title="The diff changed since you marked this file viewed">
                            changed since viewed
                        </span>
                    ) : null}
                    <label className="viewed-toggle">
                        <input
                            type="checkbox"
                            data-command="toggle-viewed"
                            checked={viewed === 'viewed'}
                            onChange={() => onToggleViewed(entry)}
                        />
                        Viewed
                    </label>
                </div>
            </header>

            {entry.isBinary ? (
                <p className="file-note">Binary file — diff not shown.</p>
            ) : null}

            {!entry.isBinary && entry.hunks.length === 0 ? (
                <p className="file-note">No textual changes in this file.</p>
            ) : null}

            {!entry.isBinary && large && !loadedLarge ? (
                <div className="file-note file-large">
                    <p>
                        Large file: {plural(entry.changedLines, 'changed line')}. Rendering is deferred to
                        keep scrolling smooth.
                    </p>
                    <button type="button" className="btn" onClick={() => setLoadedLarge(true)}>
                        Load diff
                    </button>
                </div>
            ) : null}

            {expandError ? <p className="form-error file-note">{expandError}</p> : null}

            {entry.isBinary || entry.hunks.length === 0 ? null : !active ? (
                <p className="file-note file-placeholder">Preparing diff…</p>
            ) : !showDiff ? null : (
                <div className="diff-scroll">
                    <Diff
                        viewType={viewType}
                        className={wrap ? 'diff-wrap' : 'diff-nowrap'}
                        diffType={entry.type}
                        hunks={hunks}
                        tokens={tokens}
                        widgets={widgets}
                        generateAnchorID={generateAnchorID}
                        generateLineClassName={generateLineClassName}
                        codeEvents={{onClick: gutterClick}}
                        gutterEvents={{onClick: gutterClick}}
                    >
                        {(rendered) => renderHunks(rendered)}
                    </Diff>
                </div>
            )}

            {groups.outdated.length > 0 || draftGroups.unmapped.length > 0 ? (
                <details className="outdated" open={false}>
                    <summary>
                        Outdated comments (
                        {groups.outdated.length + draftGroups.unmapped.length})
                    </summary>
                    <div className="outdated-body">
                        {groups.outdated.map((thread) => (
                            <CommentThread
                                key={`outdated-${thread.root.id}`}
                                id={`comment-${thread.root.id}`}
                                thread={thread}
                                replyDrafts={draftReplies.get(thread.root.id) ?? []}
                                actions={actions}
                            />
                        ))}
                        {draftGroups.unmapped.map((draft) => (
                            <DraftCard
                                key={`outdated-draft-${draft.id}`}
                                id={`comment-draft-${draft.id}`}
                                draft={draft}
                                actions={actions}
                            />
                        ))}
                    </div>
                </details>
            ) : null}
        </section>
    );
}

export const FileDiff = memo(FileDiffInner);
