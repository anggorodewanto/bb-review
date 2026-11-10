import {describe, expect, it} from 'vitest';
import {resolveKey} from './keymap';
import type {KeyInput} from './keymap';

function input(key: string, overrides: Partial<KeyInput> = {}): KeyInput {
    return {
        key,
        ctrlKey: false,
        metaKey: false,
        altKey: false,
        target: document.body,
        ...overrides,
    };
}

describe('resolveKey', () => {
    it('maps navigation keys', () => {
        expect(resolveKey(input('j'), null).action).toBe('next-file');
        expect(resolveKey(input('k'), null).action).toBe('prev-file');
        expect(resolveKey(input('n'), null).action).toBe('next-hunk');
        expect(resolveKey(input('p'), null).action).toBe('prev-hunk');
        expect(resolveKey(input('ArrowDown'), null).action).toBe('next-line');
        expect(resolveKey(input('ArrowUp'), null).action).toBe('prev-line');
        expect(resolveKey(input('J'), null).action).toBe('next-line');
        expect(resolveKey(input('K'), null).action).toBe('prev-line');
    });

    it('maps review and view keys', () => {
        expect(resolveKey(input('c'), null).action).toBe('comment');
        expect(resolveKey(input('v'), null).action).toBe('toggle-viewed');
        expect(resolveKey(input('a'), null).action).toBe('approve');
        expect(resolveKey(input('s'), null).action).toBe('submit');
        expect(resolveKey(input('t'), null).action).toBe('toggle-tree');
        expect(resolveKey(input('u'), null).action).toBe('toggle-split');
        expect(resolveKey(input('?'), null).action).toBe('help');
        expect(resolveKey(input('Escape'), null).action).toBe('escape');
    });

    it('ignores keys typed into inputs and textareas', () => {
        for (const tag of ['input', 'textarea', 'select']) {
            const element = document.createElement(tag);
            document.body.appendChild(element);
            expect(resolveKey(input('j', {target: element}), null).action).toBeNull();
            element.remove();
        }
    });

    it('ignores keys typed into contenteditable elements', () => {
        const element = document.createElement('div');
        element.setAttribute('contenteditable', 'true');
        document.body.appendChild(element);
        expect(resolveKey(input('j', {target: element}), null).action).toBeNull();
        element.remove();
    });

    it('ignores modifier shortcuts', () => {
        expect(resolveKey(input('j', {ctrlKey: true}), null).action).toBeNull();
        expect(resolveKey(input('j', {metaKey: true}), null).action).toBeNull();
        expect(resolveKey(input('j', {altKey: true}), null).action).toBeNull();
    });

    it('resolves the g i chord', () => {
        const first = resolveKey(input('g'), null);
        expect(first.action).toBeNull();
        expect(first.pending).toBe('g');
        expect(resolveKey(input('i'), first.pending).action).toBe('inbox');
    });

    it('clears an unknown chord instead of firing the second key', () => {
        const first = resolveKey(input('g'), null);
        const second = resolveKey(input('x'), first.pending);
        expect(second.action).toBeNull();
        expect(second.pending).toBeNull();
    });

    it('treats j after an unrelated key normally', () => {
        expect(resolveKey(input('j'), null).action).toBe('next-file');
    });
});
