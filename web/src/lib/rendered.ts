// Per-PR "render this Markdown file" choices in sessionStorage.

const PREFIX = 'bb-review:rendered:';

export function renderedStorageKey(ws: string, repo: string, prId: number): string {
    return `${PREFIX}${ws}/${repo}/${prId}`;
}

export function readRenderedPaths(key: string): Record<string, boolean> {
    try {
        const raw = window.sessionStorage.getItem(key);
        if (!raw) {
            return {};
        }
        const parsed: unknown = JSON.parse(raw);
        if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
            return {};
        }
        const output: Record<string, boolean> = {};
        for (const [path, value] of Object.entries(parsed as Record<string, unknown>)) {
            if (value === true) {
                output[path] = true;
            }
        }
        return output;
    } catch {
        return {};
    }
}

export function writeRenderedPaths(key: string, paths: Record<string, boolean>): void {
    try {
        window.sessionStorage.setItem(key, JSON.stringify(paths));
    } catch {
        // Ignore storage failures (private mode, quota).
    }
}

/** Immutable setter: false removes the entry so the map stays small. */
export function setPathRendered(
    paths: Record<string, boolean>,
    path: string,
    rendered: boolean,
): Record<string, boolean> {
    const next = {...paths};
    if (rendered) {
        next[path] = true;
    } else {
        delete next[path];
    }
    return next;
}
