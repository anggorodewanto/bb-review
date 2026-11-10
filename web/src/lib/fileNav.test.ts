import {describe, expect, it} from 'vitest';
import {fileIndexAtLine, nextNavTarget, prevFileTarget, stepFileNav} from './fileNav';

describe('fileIndexAtLine', () => {
    const tops = [100, 500, 900, 1400];

    it('returns -1 when there are no files', () => {
        expect(fileIndexAtLine([], 64)).toBe(-1);
    });

    it('returns the first file before the first top', () => {
        expect(fileIndexAtLine(tops, 0)).toBe(0);
        expect(fileIndexAtLine(tops, 99)).toBe(0);
    });

    it('treats the exact boundary as that file', () => {
        expect(fileIndexAtLine(tops, 100)).toBe(0);
        expect(fileIndexAtLine(tops, 500)).toBe(1);
        expect(fileIndexAtLine(tops, 900)).toBe(2);
    });

    it('returns the last file at or above the line', () => {
        expect(fileIndexAtLine(tops, 499)).toBe(0);
        expect(fileIndexAtLine(tops, 650)).toBe(1);
        expect(fileIndexAtLine(tops, 1200)).toBe(2);
    });

    it('returns the last file after all tops', () => {
        expect(fileIndexAtLine(tops, 5000)).toBe(3);
    });

    it('stops at unmeasured (infinite) sections', () => {
        expect(fileIndexAtLine([100, Number.POSITIVE_INFINITY, 900], 2000)).toBe(0);
    });
});

describe('nextNavTarget', () => {
    it('moves one file at a time', () => {
        expect(nextNavTarget(5, 1, null, 10)).toBe(6);
        expect(nextNavTarget(5, -1, null, 10)).toBe(4);
    });

    it('chains fast repeats from the pending target', () => {
        const first = nextNavTarget(5, -1, null, 10);
        const second = nextNavTarget(5, -1, first, 10);
        const third = nextNavTarget(5, -1, second, 10);
        expect([first, second, third]).toEqual([4, 3, 2]);
    });

    it('clamps at the first and last file', () => {
        expect(nextNavTarget(0, -1, null, 10)).toBe(0);
        expect(nextNavTarget(9, 1, null, 10)).toBe(9);
        expect(nextNavTarget(0, 1, null, 0)).toBe(0);
    });
});

describe('stepFileNav', () => {
    it('reduces repeated k presses while a scroll is pending', () => {
        let state = {current: 5, pending: null as number | null};
        state = stepFileNav(state, -1, 10);
        expect(state.pending).toBe(4);
        state = stepFileNav(state, -1, 10);
        expect(state.pending).toBe(3);
        state = stepFileNav(state, -1, 10);
        expect(state.pending).toBe(2);
        expect(state.current).toBe(5);
    });

    it('reduces repeated j presses from the pending target', () => {
        let state = {current: 1, pending: null as number | null};
        state = stepFileNav(state, 1, 10);
        state = stepFileNav(state, 1, 10);
        expect(state.pending).toBe(3);
    });

    it('starts from current when nothing is pending', () => {
        expect(stepFileNav({current: 2, pending: null}, 1, 10).pending).toBe(3);
    });
});

describe('prevFileTarget', () => {
    const VIEWPORT = 800;
    const LINE = 100;

    it('returns 0 when there are no files', () => {
        expect(prevFileTarget([], LINE, VIEWPORT, null)).toBe(0);
    });

    it('goes to the start of the current file when deep inside it', () => {
        // File 0 top is 2100px above the reading line (> one viewport).
        expect(prevFileTarget([-2000, 300], LINE, VIEWPORT, null)).toBe(0);
        // Deep inside file 1.
        expect(prevFileTarget([0, -2000, 300], LINE, VIEWPORT, null)).toBe(1);
    });

    it('goes to the previous file when near the top of the current one', () => {
        expect(prevFileTarget([0, 50, 300], LINE, VIEWPORT, null)).toBe(0);
        expect(prevFileTarget([0, 500, 3000], LINE, VIEWPORT, null)).toBe(0);
    });

    it('stays on the first file when near its top', () => {
        expect(prevFileTarget([0, 300], LINE, VIEWPORT, null)).toBe(0);
        expect(prevFileTarget([50, 300], LINE, VIEWPORT, null)).toBe(0);
    });

    it('respects a pending target', () => {
        // Pending file 2 is near the top -> previous file.
        expect(prevFileTarget([0, -2000, 300, 3000], LINE, VIEWPORT, 2)).toBe(1);
        // Pending file 2 is deep -> start of file 2.
        expect(prevFileTarget([0, 0, -2000, 3000], LINE, VIEWPORT, 2)).toBe(2);
        // Pending beyond the list clamps to the last file.
        expect(prevFileTarget([0, 50, 300], LINE, VIEWPORT, 99)).toBe(1);
    });

    it('chains: start of a deep file first, then the previous file', () => {
        const deepTops = [0, -2000, 5000];
        const first = prevFileTarget(deepTops, LINE, VIEWPORT, null);
        expect(first).toBe(1);
        // After scrolling file 1 to the reading line.
        const atStart = [0, LINE - 4, 5000];
        expect(prevFileTarget(atStart, LINE, VIEWPORT, first)).toBe(0);
    });

    it('ignores the deep rule when the viewport height is unknown', () => {
        expect(prevFileTarget([0, -2000, 300], LINE, 0, null)).toBe(0);
    });
});
