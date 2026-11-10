// In-app navigation helpers (back button behaviour).

export type AppLocationState = {fromApp?: boolean};

/** True when the current location was reached by an in-app navigation. */
export function cameFromApp(state: unknown): boolean {
    if (typeof state !== 'object' || state === null) {
        return false;
    }
    return (state as AppLocationState).fromApp === true;
}

/** The destination for a back button: history when possible, else `/`. */
export function backTarget(state: unknown): {kind: 'back'} | {kind: 'path'; path: string} {
    if (cameFromApp(state)) {
        return {kind: 'back'};
    }
    return {kind: 'path', path: '/'};
}
