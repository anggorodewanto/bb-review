import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest';
import {easeOutCubic, scrollDuration, smoothScrollTo} from './smoothScroll';

describe('scrollDuration', () => {
    it('is zero for no movement', () => {
        expect(scrollDuration(0)).toBe(0);
        expect(scrollDuration(0.5)).toBe(0);
    });

    it('is short for small hops and capped for long jumps', () => {
        expect(scrollDuration(40)).toBe(91);
        expect(scrollDuration(-2000)).toBe(140);
        expect(scrollDuration(50_000)).toBe(180);
    });
});

describe('easeOutCubic', () => {
    it('clamps and eases out', () => {
        expect(easeOutCubic(-1)).toBe(0);
        expect(easeOutCubic(0)).toBe(0);
        expect(easeOutCubic(1)).toBe(1);
        expect(easeOutCubic(2)).toBe(1);
        expect(easeOutCubic(0.5)).toBeGreaterThan(0.5);
    });
});

describe('smoothScrollTo', () => {
    let y = 0;

    beforeEach(() => {
        vi.useFakeTimers();
        y = 0;
        Object.defineProperty(window, 'scrollY', {configurable: true, get: () => y});
        Object.defineProperty(window, 'innerHeight', {configurable: true, value: 800});
        Object.defineProperty(document.documentElement, 'scrollHeight', {configurable: true, value: 10_000});
        vi.spyOn(window, 'scrollTo').mockImplementation(((arg: ScrollToOptions) => {
            y = arg.top ?? y;
        }) as typeof window.scrollTo);
    });

    afterEach(() => {
        vi.useRealTimers();
        vi.restoreAllMocks();
    });

    it('lands instantly when animation frames never arrive (throttled window)', () => {
        vi.spyOn(window, 'requestAnimationFrame').mockImplementation(() => 1);
        const done = vi.fn();
        smoothScrollTo(3000, done);
        expect(done).not.toHaveBeenCalled();
        vi.advanceTimersByTime(150);
        expect(y).toBe(3000);
        expect(done).toHaveBeenCalledTimes(1);
    });

    it('re-reads a moving target so late layout changes do not land short', () => {
        vi.spyOn(window, 'requestAnimationFrame').mockImplementation(() => 1);
        let target = 2000;
        smoothScrollTo(() => target);
        target = 3400; // content above rendered while scrolling
        vi.advanceTimersByTime(150);
        expect(y).toBe(3400);
    });

    it('cancels the previous animation without calling its onDone', () => {
        vi.spyOn(window, 'requestAnimationFrame').mockImplementation(() => 1);
        const first = vi.fn();
        const second = vi.fn();
        smoothScrollTo(3000, first);
        smoothScrollTo(500, second);
        vi.advanceTimersByTime(500);
        expect(first).not.toHaveBeenCalled();
        expect(second).toHaveBeenCalledTimes(1);
        expect(y).toBe(500);
    });
});
