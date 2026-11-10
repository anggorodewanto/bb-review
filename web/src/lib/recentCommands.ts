const STORAGE_KEY = 'bb-review:recent-commands';
const MAX_RECENTS = 5;

export function readRecentCommands(limit = MAX_RECENTS): string[] {
    try {
        const raw = window.localStorage.getItem(STORAGE_KEY);
        if (!raw) {
            return [];
        }
        const parsed: unknown = JSON.parse(raw);
        if (!Array.isArray(parsed)) {
            return [];
        }
        return parsed.filter((item): item is string => typeof item === 'string').slice(0, limit);
    } catch {
        return [];
    }
}

/** Push an id to the front of the recents list and return the new list. */
export function rememberCommand(id: string, limit = MAX_RECENTS): string[] {
    const next = [id, ...readRecentCommands(limit).filter((item) => item !== id)].slice(0, limit);
    try {
        window.localStorage.setItem(STORAGE_KEY, JSON.stringify(next));
    } catch {
        // Ignore storage failures.
    }
    return next;
}
