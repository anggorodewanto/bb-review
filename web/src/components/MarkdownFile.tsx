import {useEffect, useMemo, useState} from 'react';
import type {ReactNode} from 'react';
import {useQuery} from '@tanstack/react-query';
import * as api from '../api';
import type {FileEntry} from '../lib/diff';
import type {NavItem} from '../lib/commentNav';
import {
    MAX_MARKDOWN_BYTES,
    changedLineNumbers,
    renderMarkdown,
    sourceFileUrl,
} from '../lib/markdown';
import type {MarkdownBlock} from '../lib/markdown';

export type MarkdownContext = {
    ws: string;
    repo: string;
    prUrl: string;
    sourceCommit: string;
    mergeBase: string;
    comments: NavItem[];
    onJumpToComment: (item: NavItem) => void;
};

type Side = 'new' | 'old';

function sideLine(item: NavItem, side: Side): number | null {
    if (!item.anchor) {
        return null;
    }
    return side === 'old' ? item.anchor.from : item.anchor.to;
}

function messageOf(error: unknown): string {
    return error instanceof Error ? error.message : 'Could not load the file';
}

export function MarkdownFile({entry, context}: {entry: FileEntry; context: MarkdownContext}) {
    const deleted = entry.type === 'delete' || !entry.newPath;
    const canSwitch = Boolean(entry.oldPath && entry.newPath);
    const [side, setSide] = useState<Side>(deleted ? 'old' : 'new');
    const [changedOnly, setChangedOnly] = useState(false);
    const [expandedGroups, setExpandedGroups] = useState<Set<number>>(new Set());

    const path = side === 'old' ? (entry.oldPath ?? entry.path) : (entry.newPath ?? entry.path);
    const commit = side === 'old' ? context.mergeBase : context.sourceCommit;

    const sourceQuery = useQuery({
        queryKey: ['file', context.ws, context.repo, commit, path],
        queryFn: () => api.getFile(context.ws, context.repo, commit, path),
        enabled: path.length > 0 && commit.length > 0,
        staleTime: Infinity,
    });
    const source = sourceQuery.data ?? '';
    const tooLarge = source.length > MAX_MARKDOWN_BYTES;

    useEffect(() => {
        setExpandedGroups(new Set());
    }, [side, source]);

    const changedLines = useMemo(
        () => changedLineNumbers(entry.hunks, side),
        [entry.hunks, side],
    );

    const blocks = useMemo(() => {
        if (source.length === 0 || tooLarge) {
            return [];
        }
        return renderMarkdown(source, {
            filePath: path,
            ws: context.ws,
            repo: context.repo,
            commit,
            changedLines,
        });
    }, [source, tooLarge, path, commit, context.ws, context.repo, changedLines]);

    const blockComments = useMemo(() => {
        const located = context.comments
            .filter((item) => item.path === entry.path)
            .map((item) => ({item, line: sideLine(item, side)}))
            .filter((entry): entry is {item: NavItem; line: number} => entry.line !== null);
        return blocks.map((block) =>
            located
                .filter(({line}) => line >= block.startLine && line <= block.endLine)
                .map(({item}) => item),
        );
    }, [blocks, context.comments, entry.path, side]);

    function renderBlock(block: MarkdownBlock, index: number): ReactNode {
        const comments = blockComments[index] ?? [];
        return (
            <section
                key={block.key}
                className={`md-block${block.changed ? ' is-changed' : ''}`}
                data-start-line={block.startLine}
                data-end-line={block.endLine}
            >
                {block.changed || comments.length > 0 ? (
                    <div className="md-block-meta">
                        {block.changed ? <span className="md-changed-badge">changed</span> : null}
                        {comments.length > 0 ? (
                            <button
                                type="button"
                                className="md-comment-marker"
                                onClick={() => context.onJumpToComment(comments[0]!)}
                                title="Show this comment in Diff view"
                                aria-label={`${comments.length} comment${
                                    comments.length === 1 ? '' : 's'
                                } on this block — show in Diff view`}
                            >
                                💬 {comments.length}
                            </button>
                        ) : null}
                    </div>
                ) : null}
                <div
                    className="markdown-body"
                    dangerouslySetInnerHTML={{__html: block.html}}
                />
            </section>
        );
    }

    function renderBlocks(): ReactNode[] {
        if (!changedOnly) {
            return blocks.map((block, index) => renderBlock(block, index));
        }
        const output: ReactNode[] = [];
        let index = 0;
        while (index < blocks.length) {
            const block = blocks[index]!;
            if (block.changed) {
                output.push(renderBlock(block, index));
                index += 1;
                continue;
            }
            const start = index;
            while (index < blocks.length && !blocks[index]!.changed) {
                index += 1;
            }
            const count = index - start;
            if (expandedGroups.has(start)) {
                for (let i = start; i < index; i += 1) {
                    output.push(renderBlock(blocks[i]!, i));
                }
                continue;
            }
            output.push(
                <button
                    key={`skip-${start}`}
                    type="button"
                    className="md-unchanged"
                    onClick={() =>
                        setExpandedGroups((previous) => {
                            const next = new Set(previous);
                            next.add(start);
                            return next;
                        })
                    }
                >
                    … {count} unchanged block{count === 1 ? '' : 's'}
                </button>,
            );
        }
        return output;
    }

    return (
        <div className="md-file">
            {deleted ? (
                <p className="md-banner">Deleted file — showing the last version before deletion.</p>
            ) : null}

            <div className="md-file-toolbar">
                {canSwitch ? (
                    <div className="md-version" role="group" aria-label="Markdown version">
                        <button
                            type="button"
                            aria-pressed={side === 'new'}
                            onClick={() => setSide('new')}
                        >
                            New
                        </button>
                        <button
                            type="button"
                            aria-pressed={side === 'old'}
                            onClick={() => setSide('old')}
                        >
                            Old
                        </button>
                    </div>
                ) : null}
                <label className="md-changed-only">
                    <input
                        type="checkbox"
                        checked={changedOnly}
                        onChange={(event) => setChangedOnly(event.target.checked)}
                    />
                    Changed blocks only
                </label>
                <span className="md-hint">Comments are added in Diff view.</span>
            </div>

            {sourceQuery.isPending ? (
                <p className="file-note">Loading file…</p>
            ) : sourceQuery.isError ? (
                <p className="form-error file-note">
                    {messageOf(sourceQuery.error)} —{' '}
                    <a
                        href={sourceFileUrl(context.ws, context.repo, commit, path)}
                        target="_blank"
                        rel="noopener noreferrer"
                    >
                        open on Bitbucket ↗
                    </a>
                </p>
            ) : tooLarge ? (
                <p className="file-note">
                    Too large to render ({Math.round(source.length / 1024)} KB).{' '}
                    <a
                        href={sourceFileUrl(context.ws, context.repo, commit, path)}
                        target="_blank"
                        rel="noopener noreferrer"
                    >
                        Open on Bitbucket ↗
                    </a>
                </p>
            ) : blocks.length === 0 ? (
                <p className="file-note">This file is empty.</p>
            ) : (
                <div className="md-blocks">{renderBlocks()}</div>
            )}
        </div>
    );
}
