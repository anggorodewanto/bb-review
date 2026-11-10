// Pure geometry for expanding the context gaps around diff hunks.

export const EXPAND_CHUNK = 20;

export type LineRange = {start: number; end: number; count: number};
export type HunkBounds = {start: number; end: number};
export type FocusPosition = 'first' | 'last' | 'only' | 'middle';
export type ExpandDirection = 'up' | 'down' | 'all';

export type AdjacentGap = {
    kind: 'top' | 'middle' | 'bottom';
    /** Index of the hunk above the gap (used to rebuild the spec). */
    anchor: number;
    /** Side of the gap the focused line is on. */
    direction: 'up' | 'down';
};

export type GapSpec =
    | {kind: 'top'; firstStart: number}
    | {kind: 'middle'; prevEnd: number; nextStart: number}
    | {kind: 'bottom'; lastEnd: number; total: number | null};

export type GapControl = {
    direction: ExpandDirection;
    /** Null when the file length is not known yet (bottom gap). */
    range: LineRange | null;
};

export type GapLayout = {
    /** The whole gap; null when the file length is unknown. */
    gap: LineRange | null;
    /** Controls to render, in order. */
    controls: GapControl[];
};

/** Old-side line bounds for every hunk (inclusive end). */
export function hunkBounds(
    hunks: ReadonlyArray<{oldStart: number; oldLines: number}>,
): HunkBounds[] {
    return hunks.map((hunk) => ({
        start: hunk.oldStart,
        end: hunk.oldLines > 0 ? hunk.oldStart + hunk.oldLines - 1 : hunk.oldStart,
    }));
}

function makeRange(start: number, end: number): LineRange | null {
    if (end < start) {
        return null;
    }
    return {start, end, count: end - start + 1};
}

/**
 * The gap a cursor is next to: `last`/`only` look forward, `first` looks back,
 * `middle` lines have no adjacent gap.
 */
export function adjacentGap(
    bounds: ReadonlyArray<HunkBounds>,
    hunkIndex: number,
    position: FocusPosition,
): AdjacentGap | null {
    if (bounds.length === 0 || hunkIndex < 0 || hunkIndex >= bounds.length) {
        return null;
    }
    if (position === 'middle') {
        return null;
    }
    const forward = position === 'last' || position === 'only';
    if (position === 'only' && hunkIndex === 0 && (bounds[0]?.start ?? 0) > 1) {
        // A single-line first hunk: grow the top gap when there is one.
        return {kind: 'top', anchor: 0, direction: 'up'};
    }
    if (!forward) {
        if (hunkIndex > 0) {
            return {kind: 'middle', anchor: hunkIndex - 1, direction: 'up'};
        }
        return {kind: 'top', anchor: 0, direction: 'up'};
    }
    if (hunkIndex < bounds.length - 1) {
        return {kind: 'middle', anchor: hunkIndex, direction: 'down'};
    }
    return {kind: 'bottom', anchor: hunkIndex, direction: 'down'};
}

export function gapSpecFor(
    gap: AdjacentGap,
    bounds: ReadonlyArray<HunkBounds>,
    total: number | null,
): GapSpec | null {
    if (gap.kind === 'top') {
        const first = bounds[0];
        return first ? {kind: 'top', firstStart: first.start} : null;
    }
    if (gap.kind === 'middle') {
        const previous = bounds[gap.anchor];
        const next = bounds[gap.anchor + 1];
        if (!previous || !next) {
            return null;
        }
        return {kind: 'middle', prevEnd: previous.end, nextStart: next.start};
    }
    const last = bounds[bounds.length - 1];
    return last ? {kind: 'bottom', lastEnd: last.end, total} : null;
}

/**
 * Whole gap plus the controls to show:
 *  - top: only "up" (the last chunk before the first hunk);
 *  - middle: "down" (first chunk after the previous hunk) and "up" (last
 *    chunk before the next hunk); ≤20-line gaps get a single "all" control;
 *  - bottom: only "down", plus "all" when more than one chunk remains.
 */
export function gapLayout(spec: GapSpec, chunk = EXPAND_CHUNK): GapLayout | null {
    if (spec.kind === 'top') {
        const gap = makeRange(1, spec.firstStart - 1);
        if (!gap) {
            return null;
        }
        if (gap.count <= chunk) {
            return {gap, controls: [{direction: 'up', range: gap}]};
        }
        return {
            gap,
            controls: [
                {
                    direction: 'up',
                    range: {start: gap.end - chunk + 1, end: gap.end, count: chunk},
                },
            ],
        };
    }

    if (spec.kind === 'middle') {
        const gap = makeRange(spec.prevEnd + 1, spec.nextStart - 1);
        if (!gap) {
            return null;
        }
        if (gap.count <= chunk) {
            return {gap, controls: [{direction: 'all', range: gap}]};
        }
        const downEnd = Math.min(gap.end, gap.start + chunk - 1);
        const down = makeRange(gap.start, downEnd);
        const upStart = Math.max(downEnd + 1, gap.end - chunk + 1);
        const up = makeRange(upStart, gap.end);
        const controls: GapControl[] = [];
        if (down) {
            controls.push({direction: 'down', range: down});
        }
        if (up) {
            controls.push({direction: 'up', range: up});
        }
        controls.push({direction: 'all', range: gap});
        return {gap, controls};
    }

    if (spec.total === null) {
        return {gap: null, controls: [{direction: 'down', range: null}]};
    }
    const gap = makeRange(spec.lastEnd + 1, spec.total);
    if (!gap) {
        return null;
    }
    if (gap.count <= chunk) {
        return {gap, controls: [{direction: 'down', range: gap}]};
    }
    const downEnd = Math.min(gap.end, gap.start + chunk - 1);
    const down = makeRange(gap.start, downEnd);
    const controls: GapControl[] = [];
    if (down) {
        controls.push({direction: 'down', range: down});
    }
    controls.push({direction: 'all', range: gap});
    return {gap, controls};
}
