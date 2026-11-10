// localStorage access never throws into the app (Safari private mode, etc.).

export function readPref<T>(key: string, fallback: T): T {
    try {
        const raw = window.localStorage.getItem(key);
        if (raw === null) {
            return fallback;
        }
        return JSON.parse(raw) as T;
    } catch {
        return fallback;
    }
}

export function writePref(key: string, value: unknown): void {
    try {
        window.localStorage.setItem(key, JSON.stringify(value));
    } catch {
        // Preferences are a nicety; ignore quota/security errors.
    }
}
