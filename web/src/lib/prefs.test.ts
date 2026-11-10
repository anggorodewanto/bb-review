import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest';
import {readPref, writePref} from './prefs';

beforeEach(() => {
    window.localStorage.clear();
});

afterEach(() => {
    vi.restoreAllMocks();
});

describe('prefs', () => {
    it('round-trips a boolean preference such as line wrapping', () => {
        expect(readPref('bb-review:wrap', true)).toBe(true);
        writePref('bb-review:wrap', false);
        expect(readPref('bb-review:wrap', true)).toBe(false);
        expect(window.localStorage.getItem('bb-review:wrap')).toBe('false');
    });

    it('falls back when the stored value is not valid JSON', () => {
        window.localStorage.setItem('bb-review:wrap', 'nonsense{');
        expect(readPref('bb-review:wrap', true)).toBe(true);
    });

    it('falls back when localStorage is unavailable', () => {
        vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
            throw new Error('denied');
        });
        vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
            throw new Error('denied');
        });
        expect(readPref('bb-review:wrap', true)).toBe(true);
        expect(() => writePref('bb-review:wrap', false)).not.toThrow();
    });
});
