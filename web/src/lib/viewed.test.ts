import {describe, expect, it} from 'vitest';
import {hashText, toggleViewed, unviewedPaths, viewedStatus} from './viewed';

describe('hashText', () => {
    it('is deterministic', () => {
        const text = 'diff --git a/x b/x\n+hello\n';
        expect(hashText(text)).toBe(hashText(text));
    });

    it('changes when the diff changes', () => {
        expect(hashText('+one')).not.toBe(hashText('+two'));
    });

    it('does not collide for a shifted line', () => {
        expect(hashText('ab')).not.toBe(hashText('ba'));
    });
});

describe('viewedStatus', () => {
    const hash = hashText('+line');

    it('is unviewed when there is no hash', () => {
        expect(viewedStatus({}, 'src/a.ts', hash)).toBe('unviewed');
    });

    it('is viewed when the stored hash matches', () => {
        expect(viewedStatus({'src/a.ts': hash}, 'src/a.ts', hash)).toBe('viewed');
    });

    it('is changed when the stored hash differs', () => {
        expect(viewedStatus({'src/a.ts': 'stale'}, 'src/a.ts', hash)).toBe('changed');
    });
});

describe('toggleViewed', () => {
    const hash = hashText('+line');

    it('adds a hash when marking viewed', () => {
        expect(toggleViewed({}, 'src/a.ts', hash)).toEqual({'src/a.ts': hash});
    });

    it('removes the hash when unmarking', () => {
        expect(toggleViewed({'src/a.ts': hash}, 'src/a.ts', hash)).toEqual({});
    });

    it('re-marks a changed file with the new hash', () => {
        expect(toggleViewed({'src/a.ts': 'old'}, 'src/a.ts', hash)).toEqual({'src/a.ts': hash});
    });

    it('does not mutate the input', () => {
        const input = {'src/a.ts': 'old'};
        toggleViewed(input, 'src/a.ts', hash);
        expect(input).toEqual({'src/a.ts': 'old'});
    });
});

describe('unviewedPaths', () => {
    it('returns paths that are not fully viewed, in order', () => {
        const order = [
            {path: 'a', hash: 'h1'},
            {path: 'b', hash: 'h2'},
            {path: 'c', hash: 'h3'},
        ];
        expect(unviewedPaths(order, {a: 'h1', b: 'stale'})).toEqual(['b', 'c']);
    });
});
