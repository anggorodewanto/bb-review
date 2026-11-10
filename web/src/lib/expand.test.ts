import {describe, expect, it} from 'vitest';
import {expandFromRawCode, parseDiff} from 'react-diff-view';
import {
    adjacentGap,
    gapLayout,
    gapSpecFor,
    hunkBounds,
} from './expand';
import type {HunkBounds} from './expand';

const BOUNDS: HunkBounds[] = [
    {start: 10, end: 20},
    {start: 60, end: 70},
    {start: 200, end: 210},
];

describe('hunkBounds', () => {
    it('converts hunks to inclusive old-side ranges', () => {
        expect(hunkBounds([{oldStart: 1, oldLines: 3}, {oldStart: 10, oldLines: 0}])).toEqual([
            {start: 1, end: 3},
            {start: 10, end: 10},
        ]);
    });
});

describe('gapLayout top-of-file', () => {
    it('returns the last chunk above the first hunk', () => {
        const layout = gapLayout({kind: 'top', firstStart: 31});
        expect(layout?.gap).toEqual({start: 1, end: 30, count: 30});
        expect(layout?.controls).toEqual([
            {direction: 'up', range: {start: 11, end: 30, count: 20}},
        ]);
    });

    it('collapses a small gap into one whole-gap control', () => {
        const layout = gapLayout({kind: 'top', firstStart: 8});
        expect(layout?.gap).toEqual({start: 1, end: 7, count: 7});
        expect(layout?.controls).toEqual([{direction: 'up', range: layout?.gap}]);
    });

    it('returns null when the first hunk starts at line 1', () => {
        expect(gapLayout({kind: 'top', firstStart: 1})).toBeNull();
    });
});

describe('gapLayout middle', () => {
    it('offers non-overlapping down and up chunks plus expand-all', () => {
        const layout = gapLayout({kind: 'middle', prevEnd: 10, nextStart: 50});
        expect(layout?.gap).toEqual({start: 11, end: 49, count: 39});
        expect(layout?.controls).toEqual([
            {direction: 'down', range: {start: 11, end: 30, count: 20}},
            {direction: 'up', range: {start: 31, end: 49, count: 19}},
            {direction: 'all', range: {start: 11, end: 49, count: 39}},
        ]);
    });

    it('splits a 25-line gap without overlap', () => {
        const layout = gapLayout({kind: 'middle', prevEnd: 10, nextStart: 36});
        expect(layout?.controls).toEqual([
            {direction: 'down', range: {start: 11, end: 30, count: 20}},
            {direction: 'up', range: {start: 31, end: 35, count: 5}},
            {direction: 'all', range: {start: 11, end: 35, count: 25}},
        ]);
    });

    it('uses one expand-all control for gaps of 20 lines or less', () => {
        const layout = gapLayout({kind: 'middle', prevEnd: 10, nextStart: 18});
        expect(layout?.gap).toEqual({start: 11, end: 17, count: 7});
        expect(layout?.controls).toEqual([
            {direction: 'all', range: {start: 11, end: 17, count: 7}},
        ]);
    });

    it('returns null when the hunks touch', () => {
        expect(gapLayout({kind: 'middle', prevEnd: 10, nextStart: 11})).toBeNull();
    });
});

describe('gapLayout bottom-of-file', () => {
    it('offers the next chunk and expand-all', () => {
        const layout = gapLayout({kind: 'bottom', lastEnd: 20, total: 100});
        expect(layout?.gap).toEqual({start: 21, end: 100, count: 80});
        expect(layout?.controls).toEqual([
            {direction: 'down', range: {start: 21, end: 40, count: 20}},
            {direction: 'all', range: {start: 21, end: 100, count: 80}},
        ]);
    });

    it('uses one whole-gap control when the remainder is small', () => {
        const layout = gapLayout({kind: 'bottom', lastEnd: 20, total: 25});
        expect(layout?.controls).toEqual([
            {direction: 'down', range: {start: 21, end: 25, count: 5}},
        ]);
    });

    it('returns null when the file ends at the last hunk', () => {
        expect(gapLayout({kind: 'bottom', lastEnd: 20, total: 20})).toBeNull();
    });

    it('offers a fetch-then-expand control when the length is unknown', () => {
        const layout = gapLayout({kind: 'bottom', lastEnd: 20, total: null});
        expect(layout).toEqual({
            gap: null,
            controls: [{direction: 'down', range: null}],
        });
    });
});

describe('adjacentGap', () => {
    it('looks down from the last line of a hunk', () => {
        expect(adjacentGap(BOUNDS, 0, 'last')).toEqual({
            kind: 'middle',
            anchor: 0,
            direction: 'down',
        });
    });

    it('looks up from the first line of a hunk', () => {
        expect(adjacentGap(BOUNDS, 1, 'first')).toEqual({
            kind: 'middle',
            anchor: 0,
            direction: 'up',
        });
    });

    it('uses the top and bottom gaps at the edges', () => {
        expect(adjacentGap(BOUNDS, 0, 'first')).toEqual({
            kind: 'top',
            anchor: 0,
            direction: 'up',
        });
        expect(adjacentGap(BOUNDS, 2, 'last')).toEqual({
            kind: 'bottom',
            anchor: 2,
            direction: 'down',
        });
    });

    it('does nothing from the middle of a hunk', () => {
        expect(adjacentGap(BOUNDS, 1, 'middle')).toBeNull();
    });

    it('grows the top gap from a single-line first hunk, bottom otherwise', () => {
        expect(adjacentGap([{start: 5, end: 5}], 0, 'only')).toEqual({
            kind: 'top',
            anchor: 0,
            direction: 'up',
        });
        expect(adjacentGap([{start: 1, end: 1}], 0, 'only')).toEqual({
            kind: 'bottom',
            anchor: 0,
            direction: 'down',
        });
    });
});

describe('gapSpecFor', () => {
    it('maps each gap kind back to a spec', () => {
        expect(gapSpecFor({kind: 'top', anchor: 0, direction: 'up'}, BOUNDS, 100)).toEqual({
            kind: 'top',
            firstStart: 10,
        });
        expect(gapSpecFor({kind: 'middle', anchor: 0, direction: 'down'}, BOUNDS, 100)).toEqual({
            kind: 'middle',
            prevEnd: 20,
            nextStart: 60,
        });
        expect(gapSpecFor({kind: 'bottom', anchor: 2, direction: 'down'}, BOUNDS, 100)).toEqual({
            kind: 'bottom',
            lastEnd: 210,
            total: 100,
        });
    });
});

describe('expandFromRawCode fills gaps', () => {
    const DIFF = `diff --git a/src/a.ts b/src/a.ts
index 1111111..2222222 100644
--- a/src/a.ts
+++ b/src/a.ts
@@ -2,2 +2,2 @@
-b
+B
 c
@@ -8,2 +8,2 @@
-g
+G
 h
`;
    const source = ['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j'].join('\n');

    it('expands the whole gap so the hunks become adjacent', () => {
        const file = parseDiff(DIFF)[0]!;
        expect(file.hunks).toHaveLength(2);
        const spec = gapSpecFor(
            {kind: 'middle', anchor: 0, direction: 'down'},
            hunkBounds(file.hunks),
            10,
        )!;
        const gap = gapLayout(spec)!.gap!;
        expect(gap).toEqual({start: 4, end: 7, count: 4});
        // expandFromRawCode takes a half-open [start, end) range.
        const expanded = expandFromRawCode(file.hunks, source.split('\n'), gap.start, gap.end + 1);
        expect(expanded).toHaveLength(1);
        expect(hunkBounds(expanded)[0]).toEqual({start: 2, end: 9});
    });

    it('leaves a remaining gap when only one side is expanded', () => {
        const file = parseDiff(DIFF)[0]!;
        const spec = gapSpecFor(
            {kind: 'middle', anchor: 0, direction: 'down'},
            hunkBounds(file.hunks),
            10,
        )!;
        const down = gapLayout(spec, 1)!.controls.find((control) => control.direction === 'down')!;
        const range = down.range!;
        const expanded = expandFromRawCode(file.hunks, source.split('\n'), range.start, range.end + 1);
        const bounds = hunkBounds(expanded);
        expect(bounds[0]!.end + 1).toBeLessThan(bounds[1]!.start);
    });
});
