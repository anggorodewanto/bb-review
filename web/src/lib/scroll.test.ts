import {describe, expect, it} from 'vitest';
import {shouldShowBackToTop} from './scroll';

describe('shouldShowBackToTop', () => {
    it('hides near the top of the page', () => {
        expect(shouldShowBackToTop(0, 800)).toBe(false);
        expect(shouldShowBackToTop(800, 800)).toBe(false);
    });

    it('appears after more than one viewport', () => {
        expect(shouldShowBackToTop(801, 800)).toBe(true);
        expect(shouldShowBackToTop(4000, 800)).toBe(true);
    });

    it('stays hidden when the viewport height is unknown', () => {
        expect(shouldShowBackToTop(1200, 0)).toBe(false);
    });
});
