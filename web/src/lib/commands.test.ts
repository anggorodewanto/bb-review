import {describe, expect, it} from 'vitest';
import {
    COMMANDS,
    COMMAND_BY_ID,
    SCROLL_COMMANDS,
    allCommandGroups,
    isCommandEnabled,
    makeCommandContext,
    rankCommands,
} from './commands';

describe('command registry', () => {
    it('has unique ids', () => {
        const ids = COMMANDS.map((command) => command.id);
        expect(new Set(ids).size).toBe(ids.length);
        expect(COMMAND_BY_ID.size).toBe(COMMANDS.length);
    });

    it('has unique key sequences per view scope', () => {
        for (const view of ['inbox', 'pr', 'help'] as const) {
            const ctx = makeCommandContext(view, {hasFocus: true, hasFiles: true});
            const seen = new Map<string, string>();
            for (const command of COMMANDS) {
                if (!command.when(ctx)) {
                    continue;
                }
                for (const sequence of command.keys) {
                    const key = sequence.join(' ');
                    const previous = seen.get(key);
                    expect(previous, `${command.id} reuses "${key}" (${previous}) in ${view}`).toBeUndefined();
                    seen.set(key, command.id);
                }
            }
        }
    });

    it('keeps the established shortcuts', () => {
        const keysFor = (id: string) =>
            COMMAND_BY_ID.get(id)?.keys.map((sequence) => sequence.join(' '));
        expect(keysFor('next-file')).toEqual(['j']);
        expect(keysFor('toggle-wrap')).toEqual(['w']);
        expect(keysFor('expand-context')).toEqual(['e']);
        expect(keysFor('expand-context-all')).toEqual(['E']);
        expect(keysFor('toggle-rendered')).toEqual(['r']);
        expect(keysFor('next-comment')).toEqual([']']);
        expect(keysFor('toggle-comments')).toEqual(['C']);
        expect(keysFor('scroll-top')).toEqual(['g g', 'Home']);
        expect(keysFor('inbox')).toEqual(['g i']);
        expect(keysFor('open-palette')).toEqual(['Ctrl+K', 'Ctrl+Shift+P']);
    });

    it('marks scrolling commands for rapid-repeat coalescing', () => {
        expect(SCROLL_COMMANDS.has('next-file')).toBe(true);
        expect(SCROLL_COMMANDS.has('next-line')).toBe(true);
        expect(SCROLL_COMMANDS.has('approve')).toBe(false);
    });
});

describe('command availability', () => {
    it('hides PR-only commands on the inbox', () => {
        const inbox = makeCommandContext('inbox');
        expect(isCommandEnabled(COMMAND_BY_ID.get('next-file')!, inbox)).toBe(false);
        expect(isCommandEnabled(COMMAND_BY_ID.get('approve')!, inbox)).toBe(false);
        expect(isCommandEnabled(COMMAND_BY_ID.get('scroll-top')!, inbox)).toBe(true);
        expect(isCommandEnabled(COMMAND_BY_ID.get('inbox')!, inbox)).toBe(true);
        expect(isCommandEnabled(COMMAND_BY_ID.get('open-palette')!, inbox)).toBe(true);
    });

    it('requires a focused line to comment', () => {
        const noFocus = makeCommandContext('pr', {hasFiles: true});
        const focus = makeCommandContext('pr', {hasFocus: true, hasFiles: true});
        expect(isCommandEnabled(COMMAND_BY_ID.get('comment')!, noFocus)).toBe(false);
        expect(isCommandEnabled(COMMAND_BY_ID.get('comment')!, focus)).toBe(true);
    });

    it('requires files for line and viewed navigation', () => {
        const empty = makeCommandContext('pr');
        expect(isCommandEnabled(COMMAND_BY_ID.get('next-line')!, empty)).toBe(false);
        expect(isCommandEnabled(COMMAND_BY_ID.get('toggle-viewed')!, empty)).toBe(false);
        expect(isCommandEnabled(COMMAND_BY_ID.get('next-file')!, empty)).toBe(true);
    });

    it('hides help commands on the help page', () => {
        const help = makeCommandContext('help');
        expect(isCommandEnabled(COMMAND_BY_ID.get('help')!, help)).toBe(false);
        expect(isCommandEnabled(COMMAND_BY_ID.get('help-page')!, help)).toBe(false);
        expect(isCommandEnabled(COMMAND_BY_ID.get('inbox')!, help)).toBe(true);
    });

    it('enables Markdown commands only when they apply', () => {
        const plain = makeCommandContext('pr', {hasFiles: true});
        expect(isCommandEnabled(COMMAND_BY_ID.get('toggle-rendered')!, plain)).toBe(false);
        expect(isCommandEnabled(COMMAND_BY_ID.get('render-all-markdown')!, plain)).toBe(false);

        const markdown = makeCommandContext('pr', {
            hasFiles: true,
            hasMarkdown: true,
            currentFileMarkdown: true,
        });
        expect(isCommandEnabled(COMMAND_BY_ID.get('toggle-rendered')!, markdown)).toBe(true);
        expect(isCommandEnabled(COMMAND_BY_ID.get('render-all-markdown')!, markdown)).toBe(true);
        expect(isCommandEnabled(COMMAND_BY_ID.get('show-markdown-diffs')!, markdown)).toBe(false);

        const rendered = makeCommandContext('pr', {
            hasFiles: true,
            hasMarkdown: true,
            hasRenderedMarkdown: true,
        });
        expect(isCommandEnabled(COMMAND_BY_ID.get('show-markdown-diffs')!, rendered)).toBe(true);
    });

    it('groups every command exactly once', () => {
        const grouped = allCommandGroups().flatMap((group) => group.commands);
        expect(grouped).toHaveLength(COMMANDS.length);
    });
});

describe('rankCommands', () => {
    it('orders title matches by score', () => {
        const ctx = makeCommandContext('pr', {hasFocus: true, hasFiles: true});
        const ids = rankCommands('wrap', ctx).enabled.map((ranked) => ranked.command.id);
        expect(ids[0]).toBe('toggle-wrap');
    });

    it('separates commands unavailable in this view', () => {
        const inbox = makeCommandContext('inbox');
        const {enabled, disabled} = rankCommands('next file', inbox);
        expect(enabled.map((ranked) => ranked.command.id)).not.toContain('next-file');
        expect(disabled.map((ranked) => ranked.command.id)).toContain('next-file');
    });

    it('returns every enabled command for an empty query in registry order', () => {
        const inbox = makeCommandContext('inbox');
        const ranked = rankCommands('', inbox);
        expect(ranked.disabled).toHaveLength(0);
        expect(ranked.enabled.map((entry) => entry.command.id)).toContain('inbox');
        expect(ranked.enabled.map((entry) => entry.command.id)).toContain('open-palette');
    });

    it('matches on the group and shortcut text', () => {
        const ctx = makeCommandContext('pr', {hasFiles: true});
        const byGroup = rankCommands('navigation', ctx).enabled.map((entry) => entry.command.id);
        expect(byGroup).toContain('next-file');
        const byKey = rankCommands('g i', ctx).enabled.map((entry) => entry.command.id);
        expect(byKey).toContain('inbox');
    });
});
