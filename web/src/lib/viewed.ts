// Viewed-file bookkeeping. The server stores one opaque hash per file path, so
// we hash the file's own slice of the unified diff text.

/** Stable FNV-1a (32-bit) over UTF-16 code units, plus the byte length. */
export function hashText(text: string): string {
    let hash = 0x811c9dc5;
    for (let i = 0; i < text.length; i += 1) {
        hash ^= text.charCodeAt(i);
        hash = Math.imul(hash, 0x01000193);
    }
    return `${(hash >>> 0).toString(16).padStart(8, '0')}-${text.length.toString(36)}`;
}

export type ViewedStatus = 'viewed' | 'changed' | 'unviewed';

export function viewedStatus(
    files: Record<string, string>,
    path: string,
    currentHash: string,
): ViewedStatus {
    const stored = files[path];
    if (stored === undefined) {
        return 'unviewed';
    }
    return stored === currentHash ? 'viewed' : 'changed';
}

/** Immutably set/remove the viewed mark for a path. */
export function toggleViewed(
    files: Record<string, string>,
    path: string,
    currentHash: string,
): Record<string, string> {
    if (viewedStatus(files, path, currentHash) === 'viewed') {
        const next = {...files};
        delete next[path];
        return next;
    }
    return {...files, [path]: currentHash};
}

/** Paths that are not yet viewed (or changed since viewed), in diff order. */
export function unviewedPaths(
    order: Array<{path: string; hash: string}>,
    files: Record<string, string>,
): string[] {
    return order.filter(({path, hash}) => viewedStatus(files, path, hash) !== 'viewed').map((f) => f.path);
}
