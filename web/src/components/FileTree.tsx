import {useMemo, useState} from 'react';
import type {FileEntry} from '../lib/diff';
import {isNoisePath} from '../lib/diff';
import {viewedStatus} from '../lib/viewed';
import type {ViewedStatus} from '../lib/viewed';
import {plural} from '../lib/format';

type Dir = {
    dirs: Map<string, Dir>;
    files: Array<{name: string; entry: FileEntry; index: number}>;
};

function emptyDir(): Dir {
    return {dirs: new Map(), files: []};
}

function buildTree(files: FileEntry[]): Dir {
    const root = emptyDir();
    files.forEach((entry, index) => {
        const parts = entry.path.split('/');
        const name = parts.pop() ?? entry.path;
        let dir = root;
        for (const part of parts) {
            const next = dir.dirs.get(part) ?? emptyDir();
            dir.dirs.set(part, next);
            dir = next;
        }
        dir.files.push({name, entry, index});
    });
    return root;
}

function dirTotals(dir: Dir): {additions: number; deletions: number; count: number} {
    let additions = 0;
    let deletions = 0;
    let count = 0;
    for (const child of dir.dirs.values()) {
        const totals = dirTotals(child);
        additions += totals.additions;
        deletions += totals.deletions;
        count += totals.count;
    }
    for (const file of dir.files) {
        additions += file.entry.additions;
        deletions += file.entry.deletions;
        count += 1;
    }
    return {additions, deletions, count};
}

function collectNoiseDirs(dir: Dir, prefix: string, output: Set<string>): void {
    for (const [name, child] of dir.dirs) {
        const path = prefix ? `${prefix}/${name}` : name;
        if (isNoisePath(`${path}/x`)) {
            output.add(path);
        }
        collectNoiseDirs(child, path, output);
    }
}

export type FileTreeProps = {
    files: FileEntry[];
    currentIndex: number;
    comments: Record<string, number>;
    viewedFiles: Record<string, string>;
    onSelect: (index: number) => void;
    onToggleViewed: (entry: FileEntry) => void;
    onClose?: () => void;
};

export function FileTree({
    files,
    currentIndex,
    comments,
    viewedFiles,
    onSelect,
    onToggleViewed,
    onClose,
}: FileTreeProps) {
    const tree = useMemo(() => buildTree(files), [files]);
    const [collapsed, setCollapsed] = useState<Set<string>>(() => {
        const noise = new Set<string>();
        collectNoiseDirs(tree, '', noise);
        return noise;
    });

    const viewedCount = files.filter(
        (entry) => viewedStatus(viewedFiles, entry.path, entry.hash) === 'viewed',
    ).length;

    function toggleDir(path: string) {
        setCollapsed((prev) => {
            const next = new Set(prev);
            if (next.has(path)) {
                next.delete(path);
                return next;
            }
            next.add(path);
            return next;
        });
    }

    function renderDir(dir: Dir, prefix: string, depth: number): React.ReactNode {
        const dirs = Array.from(dir.dirs.entries()).sort(([a], [b]) => a.localeCompare(b));
        const dirFiles = [...dir.files].sort((a, b) => a.name.localeCompare(b.name));

        return (
            <>
                {dirs.map(([name, child]) => {
                    const path = prefix ? `${prefix}/${name}` : name;
                    const isCollapsed = collapsed.has(path);
                    const totals = dirTotals(child);
                    return (
                        <div key={path} className="tree-dir">
                            <button
                                type="button"
                                className="tree-dir-row"
                                style={{paddingLeft: `${depth * 12 + 8}px`}}
                                onClick={() => toggleDir(path)}
                                aria-expanded={!isCollapsed}
                            >
                                <span className={`tree-chevron${isCollapsed ? ' is-collapsed' : ''}`} aria-hidden="true">
                                    ▾
                                </span>
                                <span className="tree-name">{name}</span>
                                <span className="tree-counts">
                                    {totals.additions > 0 ? <span className="add">+{totals.additions}</span> : null}
                                    {totals.deletions > 0 ? <span className="del">-{totals.deletions}</span> : null}
                                </span>
                            </button>
                            {isCollapsed ? null : renderDir(child, path, depth + 1)}
                        </div>
                    );
                })}
                {dirFiles.map(({name, entry, index}) => {
                    const status: ViewedStatus = viewedStatus(viewedFiles, entry.path, entry.hash);
                    const commentCount = comments[entry.path] ?? 0;
                    return (
                        <div
                            key={entry.path}
                            className={`tree-file${index === currentIndex ? ' is-current' : ''} viewed-${status}`}
                            style={{paddingLeft: `${depth * 12 + 24}px`}}
                        >
                            <button type="button" className="tree-file-row" onClick={() => onSelect(index)}>
                                <span className="tree-name" title={entry.path}>
                                    {name}
                                </span>
                                {status === 'changed' ? (
                                    <span className="tag tag-warn" title="The diff changed since you marked this viewed">
                                        changed
                                    </span>
                                ) : null}
                                {commentCount > 0 ? (
                                    <span className="tree-comments" title={plural(commentCount, 'comment')}>
                                        {commentCount}
                                    </span>
                                ) : null}
                                <span className="tree-counts">
                                    {entry.additions > 0 ? <span className="add">+{entry.additions}</span> : null}
                                    {entry.deletions > 0 ? <span className="del">-{entry.deletions}</span> : null}
                                </span>
                            </button>
                            <input
                                type="checkbox"
                                className="viewed-check"
                                checked={status === 'viewed'}
                                onChange={() => onToggleViewed(entry)}
                                aria-label={`Mark ${entry.path} viewed`}
                            />
                        </div>
                    );
                })}
            </>
        );
    }

    return (
        <aside className="file-tree" aria-label="Changed files">
            <div className="tree-head">
                <span className="tree-title">
                    {files.length} {files.length === 1 ? 'file' : 'files'}
                </span>
                <span className="tree-progress">
                    {viewedCount}/{files.length} viewed
                </span>
                {onClose ? (
                    <button type="button" className="btn btn-small btn-ghost tree-close" onClick={onClose}>
                        Close
                    </button>
                ) : null}
            </div>
            <div className="tree-scroll">{renderDir(tree, '', 0)}</div>
        </aside>
    );
}
