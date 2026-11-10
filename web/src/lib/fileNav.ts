/**
 * Pure geometry for the PR view's scroll-spy: the current file is the last one
 * whose top is at or above a reading line just below the sticky toolbar.
 */
export function fileIndexAtLine(tops: ReadonlyArray<number>, line: number): number {
    if (tops.length === 0) {
        return -1;
    }
    let index = 0;
    for (let i = 0; i < tops.length; i += 1) {
        const top = tops[i];
        if (top === undefined || !Number.isFinite(top)) {
            break;
        }
        if (top > line) {
            break;
        }
        index = i;
    }
    return index;
}

/**
 * Next file for j/k. `pending` is the target of a scroll that is still in
 * flight, so fast repeats chain from it instead of from the (lagging) spy.
 */
export function nextNavTarget(
    current: number,
    delta: number,
    pending: number | null,
    count: number,
): number {
    if (count <= 0) {
        return 0;
    }
    const base = pending ?? current;
    return Math.min(count - 1, Math.max(0, base + delta));
}

export type FileNavState = {
    /** Scroll-spy index (updated when the page settles). */
    current: number;
    /** Target of the navigation that is currently scrolling, or null. */
    pending: number | null;
};

/** Reducer form used by tests and by the view for repeated j/k presses. */
export function stepFileNav(state: FileNavState, delta: 1 | -1, count: number): FileNavState {
    return {
        current: state.current,
        pending: nextNavTarget(state.current, delta, state.pending, count),
    };
}
