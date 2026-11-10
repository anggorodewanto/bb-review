import {beforeEach, describe, expect, it} from 'vitest';
import {normalizeInboxUrl, lastInboxUrl, rememberInboxUrl} from './inboxUrl';
import {backTarget, cameFromApp} from './navigation';
import {readScrollPosition, writeScrollPosition} from './scrollMemory';

describe('inbox URL memory', () => {
    beforeEach(() => {
        window.sessionStorage.clear();
    });

    it('normalizes query strings into an inbox path', () => {
        expect(normalizeInboxUrl('')).toBe('/');
        expect(normalizeInboxUrl('?tab=mine')).toBe('/?tab=mine');
        expect(normalizeInboxUrl('tab=mine&q=abc')).toBe('/?tab=mine&q=abc');
    });

    it('falls back to / when nothing was remembered', () => {
        expect(lastInboxUrl()).toBe('/');
    });

    it('remembers tab and filter', () => {
        rememberInboxUrl('?tab=mine&q=diff');
        expect(lastInboxUrl()).toBe('/?tab=mine&q=diff');
    });

    it('survives a fresh module read through sessionStorage', () => {
        window.sessionStorage.setItem('bb-review:last-inbox', '/?tab=mine&q=abc');
        expect(lastInboxUrl()).toBe('/?tab=mine&q=abc');
    });
});

describe('back navigation', () => {
    it('detects in-app navigation from location state', () => {
        expect(cameFromApp({fromApp: true})).toBe(true);
        expect(cameFromApp({fromApp: false})).toBe(false);
        expect(cameFromApp(null)).toBe(false);
        expect(cameFromApp(undefined)).toBe(false);
        expect(cameFromApp('fromApp')).toBe(false);
    });

    it('uses history when the PR was opened in-app', () => {
        expect(backTarget({fromApp: true})).toEqual({kind: 'back'});
    });

    it('falls back to / when there is no in-app history', () => {
        expect(backTarget(null)).toEqual({kind: 'path', path: '/'});
        expect(backTarget({fromApp: false})).toEqual({kind: 'path', path: '/'});
    });
});

describe('scroll memory', () => {
    beforeEach(() => {
        window.sessionStorage.clear();
    });

    it('round-trips a position', () => {
        writeScrollPosition('inbox:?tab=mine', 640);
        expect(readScrollPosition('inbox:?tab=mine')).toBe(640);
    });

    it('returns null for missing or invalid values', () => {
        expect(readScrollPosition('nope')).toBeNull();
        window.sessionStorage.setItem('bb-review:scroll:bad', 'NaN');
        expect(readScrollPosition('bad')).toBeNull();
        window.sessionStorage.setItem('bb-review:scroll:negative', '-5');
        expect(readScrollPosition('negative')).toBeNull();
    });
});
