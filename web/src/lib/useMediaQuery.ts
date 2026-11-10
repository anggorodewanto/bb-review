import {useCallback, useSyncExternalStore} from 'react';

/** Reactive `window.matchMedia`, with a safe fallback when matchMedia is missing. */
export function useMediaQuery(query: string): boolean {
    const subscribe = useCallback(
        (onChange: () => void) => {
            if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
                return () => undefined;
            }
            const mql = window.matchMedia(query);
            mql.addEventListener('change', onChange);
            return () => mql.removeEventListener('change', onChange);
        },
        [query],
    );

    const getSnapshot = useCallback(() => {
        if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
            return false;
        }
        return window.matchMedia(query).matches;
    }, [query]);

    return useSyncExternalStore(subscribe, getSnapshot, () => false);
}
