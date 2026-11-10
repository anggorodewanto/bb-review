import {describe, expect, it} from 'vitest';
import {fuzzyMatch, highlightSegments} from './fuzzy';

describe('fuzzyMatch', () => {
    it('matches a contiguous subsequence and reports its range', () => {
        const match = fuzzyMatch('wr', 'Wrap lines');
        expect(match).not.toBeNull();
        expect(match?.ranges).toEqual([{start: 0, end: 2}]);
    });

    it('matches non-contiguous characters', () => {
        const match = fuzzyMatch('tlw', 'Toggle line wrapping');
        expect(match).not.toBeNull();
        expect(match?.ranges).toEqual([
            {start: 0, end: 1},
            {start: 4, end: 5},
            {start: 12, end: 13},
        ]);
    });

    it('returns null when the query is not a subsequence', () => {
        expect(fuzzyMatch('zzz', 'Wrap lines')).toBeNull();
        expect(fuzzyMatch('wrapz', 'Wrap lines')).toBeNull();
    });

    it('treats an empty query as a neutral match', () => {
        expect(fuzzyMatch('   ', 'anything')).toEqual({score: 0, ranges: []});
    });

    it('scores contiguous word starts higher', () => {
        const tight = fuzzyMatch('wrap', 'Toggle wrap');
        const loose = fuzzyMatch('wrap', 'Toggle line wrapping');
        expect(tight).not.toBeNull();
        expect(loose).not.toBeNull();
        expect(tight!.score).toBeGreaterThan(loose!.score);
    });

    it('is case-insensitive', () => {
        expect(fuzzyMatch('APPROVE', 'Toggle approve')).not.toBeNull();
    });
});

describe('highlightSegments', () => {
    it('splits text into plain and matched segments', () => {
        expect(highlightSegments('abcd', [{start: 1, end: 3}])).toEqual([
            {text: 'a', match: false},
            {text: 'bc', match: true},
            {text: 'd', match: false},
        ]);
    });

    it('handles a match at the end', () => {
        expect(highlightSegments('abc', [{start: 1, end: 3}])).toEqual([
            {text: 'a', match: false},
            {text: 'bc', match: true},
        ]);
    });

    it('returns the whole text when there are no ranges', () => {
        expect(highlightSegments('abc', [])).toEqual([{text: 'abc', match: false}]);
    });
});
