// The inbox remembers the last URL it was on (tab + filter query).

const STORAGE_KEY = 'bb-review:last-inbox';

export function normalizeInboxUrl(search: string): string {
    if (search.length === 0) {
        return '/';
    }
    return search.startsWith('?') ? `/${search}` : `/?${search}`;
}

/** Read the last inbox URL, falling back to a bare `/`. */
export function lastInboxUrl(): string {
    try {
        return window.sessionStorage.getItem(STORAGE_KEY) ?? '/';
    } catch {
        return '/';
    }
}

/** Remember the current inbox URL (path + query string). */
export function rememberInboxUrl(search: string): string {
    const url = normalizeInboxUrl(search);
    try {
        window.sessionStorage.setItem(STORAGE_KEY, url);
    } catch {
        // Ignore storage failures (private mode); `/` remains the fallback.
    }
    return url;
}
