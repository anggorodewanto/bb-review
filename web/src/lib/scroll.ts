/** Scroll helpers shared by the inbox and the PR view. */
import {smoothScrollTo} from './smoothScroll';

export function prefersReducedMotion(): boolean {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
        return false;
    }
    return window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

/**
 * Nearest ancestor that actually scrolls. The PR view scrolls the window
 * today, but this keeps scrolling correct if the layout ever moves into a
 * container (e.g. a fixed-height diff pane).
 */
export function scrollContainerFor(el: Element | null): HTMLElement | null {
    let node = el?.parentElement ?? null;
    while (node) {
        const style = window.getComputedStyle(node);
        const overflowY = style.overflowY;
        const scrollable = overflowY === 'auto' || overflowY === 'scroll' || overflowY === 'overlay';
        if (scrollable && node.scrollHeight > node.clientHeight) {
            return node;
        }
        node = node.parentElement;
    }
    return null;
}

/** Scroll the page (or its scroll container) back to the top. */
export function scrollPageToTop(root?: Element | null): void {
    const behavior: ScrollBehavior = prefersReducedMotion() ? 'auto' : 'smooth';
    const container = scrollContainerFor(root ?? null);
    if (container) {
        container.scrollTo({top: 0, behavior});
        return;
    }
    smoothScrollTo(0);
}

/** Show the back-to-top button after scrolling more than one viewport. */
export function shouldShowBackToTop(scrollTop: number, viewportHeight: number): boolean {
    if (!(viewportHeight > 0)) {
        return false;
    }
    return scrollTop > viewportHeight;
}

/** Home should keep working inside a scrollable code block. */
export function isCodeScrollTarget(target: EventTarget | null): boolean {
    if (!(target instanceof HTMLElement)) {
        return false;
    }
    return target.closest('.diff-scroll') !== null;
}
