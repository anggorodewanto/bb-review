/**
 * Quick, editor-style smooth scrolling for keyboard navigation.
 *
 * The browser's native `behavior: 'smooth'` is slow for long jumps and its
 * timing can't be tuned. This animates window.scrollY with a short ease-out
 * that scales with distance, and retargets when a new navigation starts
 * mid-flight, so fast repeated keys glide instead of stacking animations.
 */
import {prefersReducedMotion} from './scroll';

const MIN_DURATION_MS = 90;
const MAX_DURATION_MS = 180;
/** If no animation frame arrives in this time (throttled/hidden window), jump. */
const STALL_MS = 100;

/** Animation length for a scroll distance in px: short hops are near-instant. */
export function scrollDuration(distance: number): number {
    const abs = Math.abs(distance);
    if (abs < 1) {
        return 0;
    }
    return Math.round(Math.min(MAX_DURATION_MS, MIN_DURATION_MS + abs / 40));
}

export function easeOutCubic(t: number): number {
    const clamped = Math.min(1, Math.max(0, t));
    return 1 - (1 - clamped) ** 3;
}

type Active = {cancel: () => void};
let active: Active | null = null;

/**
 * Scroll the window to a target, calling `onDone` once it lands. The target
 * may be a function: it is re-read every frame and at the end, so content
 * that renders or resizes mid-scroll (lazy diffs) can't make us land short.
 * The browser clamps out-of-range values. A new call cancels the running
 * animation (its onDone is not called) and starts from the current position.
 */
export function smoothScrollTo(targetY: number | (() => number), onDone?: () => void): void {
    active?.cancel();
    active = null;

    const resolve = typeof targetY === 'function' ? targetY : () => targetY;
    const startY = window.scrollY;
    const duration = scrollDuration(resolve() - startY);

    const jump = () => {
        window.scrollTo({top: Math.max(0, Math.round(resolve())), behavior: 'auto'});
        onDone?.();
    };
    if (duration === 0 || prefersReducedMotion() || typeof window.requestAnimationFrame !== 'function') {
        jump();
        return;
    }

    let frame = 0;
    let frames = 0;
    let done = false;
    const startedAt = performance.now();

    const finish = () => {
        if (done) {
            return;
        }
        done = true;
        window.cancelAnimationFrame(frame);
        window.clearTimeout(stallTimer);
        window.clearTimeout(capTimer);
        if (active === handle) {
            active = null;
        }
        jump();
    };
    const step = (now: number) => {
        frames += 1;
        const t = (now - startedAt) / duration;
        if (t >= 1) {
            finish();
            return;
        }
        const distance = resolve() - startY;
        window.scrollTo({top: startY + distance * easeOutCubic(t), behavior: 'auto'});
        frame = window.requestAnimationFrame(step);
    };

    // Background or occluded windows throttle rAF: land instantly instead of stalling.
    const stallTimer = window.setTimeout(() => {
        if (frames === 0) {
            finish();
        }
    }, STALL_MS);
    const capTimer = window.setTimeout(finish, duration + 200);

    const handle: Active = {
        cancel: () => {
            done = true;
            window.cancelAnimationFrame(frame);
            window.clearTimeout(stallTimer);
            window.clearTimeout(capTimer);
        },
    };
    active = handle;
    frame = window.requestAnimationFrame(step);
}

/** Window scrollY that puts `el` at `block` ('start' = under `offset` px, 'center' = mid-viewport). */
export function targetYFor(el: Element, block: 'start' | 'center', offset = 0): number {
    const rect = el.getBoundingClientRect();
    if (block === 'center') {
        return window.scrollY + rect.top - (window.innerHeight - rect.height) / 2;
    }
    return window.scrollY + rect.top - offset;
}

/** Smoothly bring `el` into view. */
export function smoothScrollToElement(
    el: Element,
    block: 'start' | 'center',
    offset = 0,
    onDone?: () => void,
): void {
    smoothScrollTo(() => targetYFor(el, block, offset), onDone);
}
