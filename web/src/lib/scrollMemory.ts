// Per-URL scroll positions in sessionStorage.

const PREFIX = 'bb-review:scroll:';

function storageKey(key: string): string {
    return `${PREFIX}${key}`;
}

export function readScrollPosition(key: string): number | null {
    try {
        const raw = window.sessionStorage.getItem(storageKey(key));
        if (raw === null) {
            return null;
        }
        const value = Number(raw);
        return Number.isFinite(value) && value >= 0 ? value : null;
    } catch {
        return null;
    }
}

export function writeScrollPosition(key: string, y: number): void {
    if (!Number.isFinite(y)) {
        return;
    }
    try {
        window.sessionStorage.setItem(storageKey(key), String(Math.max(0, Math.round(y))));
    } catch {
        // Ignore storage failures.
    }
}
