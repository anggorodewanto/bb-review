import {beforeEach, describe, expect, it} from 'vitest';
import {
    readRenderedPaths,
    renderedStorageKey,
    setPathRendered,
    writeRenderedPaths,
} from './rendered';

describe('rendered markdown persistence', () => {
    const key = renderedStorageKey('ws', 'repo', 7);

    beforeEach(() => {
        window.sessionStorage.clear();
    });

    it('builds a per-PR key', () => {
        expect(key).toBe('bb-review:rendered:ws/repo/7');
    });

    it('returns an empty map when nothing is stored', () => {
        expect(readRenderedPaths(key)).toEqual({});
    });

    it('round-trips rendered paths', () => {
        writeRenderedPaths(key, {'README.md': true, 'docs/a.md': true});
        expect(readRenderedPaths(key)).toEqual({'README.md': true, 'docs/a.md': true});
    });

    it('drops false and malformed entries', () => {
        window.sessionStorage.setItem(
            key,
            JSON.stringify({'a.md': true, 'b.md': false, 'c.md': 'yes', 'd.md': 1}),
        );
        expect(readRenderedPaths(key)).toEqual({'a.md': true});
    });

    it('ignores malformed JSON', () => {
        window.sessionStorage.setItem(key, 'not json');
        expect(readRenderedPaths(key)).toEqual({});
    });

    it('sets and removes paths immutably', () => {
        const first = setPathRendered({}, 'README.md', true);
        expect(first).toEqual({'README.md': true});
        const second = setPathRendered(first, 'README.md', false);
        expect(second).toEqual({});
        expect(first).toEqual({'README.md': true});
    });
});
