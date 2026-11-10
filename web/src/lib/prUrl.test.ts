import {describe, expect, it} from 'vitest';
import {extractPRUrl} from './prUrl';

describe('extractPRUrl', () => {
    it('normalizes a plain PR url', () => {
        expect(extractPRUrl('https://bitbucket.org/ws/repo/pull-requests/12')).toBe(
            'https://bitbucket.org/ws/repo/pull-requests/12',
        );
    });

    it('drops www, trailing path and query', () => {
        expect(
            extractPRUrl('https://www.bitbucket.org/ws/repo/pull-requests/12/overview?x=1'),
        ).toBe('https://bitbucket.org/ws/repo/pull-requests/12');
    });

    it('accepts http and surrounding text', () => {
        expect(extractPRUrl('see http://bitbucket.org/a/b/pull-requests/7 for details')).toBe(
            'https://bitbucket.org/a/b/pull-requests/7',
        );
    });

    it('rejects non-PR urls', () => {
        expect(extractPRUrl('https://bitbucket.org/ws/repo')).toBeNull();
        expect(extractPRUrl('https://example.com/a/b/pull-requests/1')).toBeNull();
        expect(extractPRUrl('not a url')).toBeNull();
        expect(extractPRUrl('')).toBeNull();
    });
});
