import {parseDiff, getChangeKey} from 'react-diff-view';
import type {ChangeData, FileData, HunkData} from 'react-diff-view';
import type {DiffType} from 'react-diff-view';
import {hashText} from './viewed';

export type FileEntry = {
    /** Display path; unique within a PR (used as the viewed-state key). */
    path: string;
    oldPath: string | null;
    newPath: string | null;
    type: DiffType;
    isBinary: boolean;
    hunks: HunkData[];
    /** This file's own slice of the unified diff (hashed for "viewed"). */
    rawText: string;
    hash: string;
    additions: number;
    deletions: number;
    changedLines: number;
};

export function splitDiffChunks(raw: string): string[] {
    const chunks: string[] = [];
    let current: string[] = [];

    for (const line of raw.split('\n')) {
        if (line.startsWith('diff --git ')) {
            if (current.length > 0) {
                chunks.push(current.join('\n'));
            }
            current = [line];
            continue;
        }
        if (current.length > 0) {
            current.push(line);
        }
    }

    if (current.length > 0) {
        chunks.push(current.join('\n'));
    }
    return chunks;
}

function countChanges(hunks: HunkData[]): {additions: number; deletions: number} {
    let additions = 0;
    let deletions = 0;
    for (const hunk of hunks) {
        for (const change of hunk.changes) {
            if (change.type === 'insert') {
                additions += 1;
                continue;
            }
            if (change.type === 'delete') {
                deletions += 1;
            }
        }
    }
    return {additions, deletions};
}

function chunkFor(file: FileData, chunks: string[]): string | null {
    const newPath = file.newPath ? ` b/${file.newPath}` : '';
    const oldPath = file.oldPath ? ` a/${file.oldPath}` : '';
    for (const chunk of chunks) {
        if (newPath && chunk.includes(newPath) && chunk.startsWith('diff --git ')) {
            return chunk;
        }
        if (oldPath && chunk.includes(oldPath) && chunk.startsWith('diff --git ')) {
            return chunk;
        }
    }
    return null;
}

function fallbackText(file: FileData): string {
    const header = `diff --git a/${file.oldPath} b/${file.newPath}\n`;
    const hunks = file.hunks.map((hunk) => hunk.content).join('\n');
    return `${header}${hunks}`;
}

/**
 * True when a rendered line's change list contains the focused change key.
 * Split rows pair old/new changes and hold `null` on the missing side for
 * pure inserts/deletes, so the list must be treated as nullable at runtime.
 */
export function lineContainsChangeKey(
    changes: ReadonlyArray<ChangeData | null | undefined> | null | undefined,
    focusedKey: string | null,
): boolean {
    if (!focusedKey || !changes) {
        return false;
    }
    return changes.some((change) => (change ? getChangeKey(change) === focusedKey : false));
}

export function buildFileEntries(rawDiff: string): FileEntry[] {
    const trimmed = rawDiff.trim();
    if (!trimmed) {
        return [];
    }

    let files: FileData[];
    try {
        files = parseDiff(rawDiff, {nearbySequences: 'zip'});
    } catch {
        return [];
    }

    const chunks = splitDiffChunks(rawDiff);
    const aligned = chunks.length === files.length;

    return files.map((file, index) => {
        const rawText = (aligned ? chunks[index] : chunkFor(file, chunks)) ?? fallbackText(file);
        const oldPath = file.oldPath && file.oldPath !== '/dev/null' ? file.oldPath : null;
        const newPath = file.newPath && file.newPath !== '/dev/null' ? file.newPath : null;
        const path = file.type === 'delete' ? (oldPath ?? newPath ?? '') : (newPath ?? oldPath ?? '');
        const {additions, deletions} = countChanges(file.hunks);
        const binary =
            file.isBinary === true ||
            /^Binary files .* differ$/m.test(rawText) ||
            /^GIT binary patch$/m.test(rawText);

        return {
            path,
            oldPath,
            newPath,
            type: file.type,
            isBinary: binary,
            hunks: file.hunks,
            rawText,
            hash: hashText(rawText),
            additions,
            deletions,
            changedLines: additions + deletions,
        };
    });
}

/** Directories collapsed in the tree by default: lockfiles and vendored trees. */
export function isNoisePath(path: string): boolean {
    return /(^|\/)(node_modules|vendor|dist|build)\//.test(path);
}
