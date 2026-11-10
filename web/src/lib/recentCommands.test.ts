import {beforeEach, describe, expect, it} from 'vitest';
import {readRecentCommands, rememberCommand} from './recentCommands';

describe('recent commands', () => {
    beforeEach(() => {
        window.localStorage.clear();
    });

    it('starts empty', () => {
        expect(readRecentCommands()).toEqual([]);
    });

    it('keeps most-recent first without duplicates', () => {
        rememberCommand('next-file');
        rememberCommand('approve');
        rememberCommand('next-file');
        expect(readRecentCommands()).toEqual(['next-file', 'approve']);
    });

    it('caps the list at five entries', () => {
        for (const id of ['a', 'b', 'c', 'd', 'e', 'f']) {
            rememberCommand(id);
        }
        expect(readRecentCommands()).toEqual(['f', 'e', 'd', 'c', 'b']);
    });

    it('ignores malformed storage', () => {
        window.localStorage.setItem('bb-review:recent-commands', 'not json');
        expect(readRecentCommands()).toEqual([]);
    });

    it('ignores non-string entries', () => {
        window.localStorage.setItem('bb-review:recent-commands', JSON.stringify(['ok', 3, null]));
        expect(readRecentCommands()).toEqual(['ok']);
    });
});
