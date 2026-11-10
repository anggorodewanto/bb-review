import {describe, expect, it} from 'vitest';
import {fileIndexAtLine, nextNavTarget, stepFileNav} from './fileNav';

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
