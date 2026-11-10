// One command registry (lib/commands.ts) powers the global handler, the help
// overlay, the palette and the help page. This module turns it into key
// bindings and resolves keyboard events.

import {COMMANDS} from './commands';
import type {CommandId} from './commands';

export type KeyAction = CommandId;

export type KeyBinding = {
    action: KeyAction;
    /** e.g. ['j'] or ['g', 'i'] for a chord. */
    keys: string[];
    label: string;
    group: 'Navigate' | 'Review' | 'View' | 'Help';
};

type LegacyGroup = KeyBinding['group'];

function legacyGroup(group: string): LegacyGroup {
    if (group === 'Navigation' || group === 'Comments') {
        return 'Navigate';
    }
    if (group === 'Review' || group === 'View') {
        return group;
    }
    return 'Help';
}

/**
 * Compatibility view of the registry: one entry per key sequence. New code
 * should read COMMANDS from lib/commands.ts directly.
 */
export const KEYMAP: KeyBinding[] = COMMANDS.flatMap((command) =>
    command.keys.map((keys) => ({
        action: command.id,
        keys,
        label: command.title,
        group: legacyGroup(command.group),
    })),
);

type ParsedToken = {
    key: string;
    ctrl: boolean;
    meta: boolean;
    shift: boolean;
    modified: boolean;
};

function parseToken(raw: string): ParsedToken {
    const parts = raw.split('+');
    const key = parts.pop() ?? raw;
    const modifiers = new Set(parts.map((part) => part.toLowerCase()));
    return {
        key,
        ctrl: modifiers.has('ctrl'),
        meta: modifiers.has('meta'),
        shift: modifiers.has('shift'),
        modified: modifiers.size > 0,
    };
}

function modifiedId(token: ParsedToken): string {
    const mods: string[] = [];
    if (token.ctrl) {
        mods.push('ctrl');
    }
    if (token.meta) {
        mods.push('meta');
    }
    if (token.shift) {
        mods.push('shift');
    }
    return `${mods.sort().join('+')}|${token.key.toLowerCase()}`;
}

const SINGLE = new Map<string, KeyAction>();
const MODIFIED = new Map<string, KeyAction>();
const CHORDS = new Map<string, Map<string, KeyAction>>();

for (const command of COMMANDS) {
    for (const sequence of command.keys) {
        const [first, second, ...rest] = sequence;
        if (!first || rest.length > 0) {
            continue;
        }
        if (second === undefined) {
            const token = parseToken(first);
            if (token.modified) {
                if (!MODIFIED.has(modifiedId(token))) {
                    MODIFIED.set(modifiedId(token), command.id);
                }
                continue;
            }
            if (!SINGLE.has(first)) {
                SINGLE.set(first, command.id);
            }
            continue;
        }
        const next = CHORDS.get(first) ?? new Map<string, KeyAction>();
        if (!next.has(second)) {
            next.set(second, command.id);
        }
        CHORDS.set(first, next);
    }
}

export type KeyInput = {
    key: string;
    ctrlKey: boolean;
    metaKey: boolean;
    altKey: boolean;
    shiftKey?: boolean;
    target: EventTarget | null;
};

export type KeyResult = {
    action: KeyAction | null;
    /** pending first key of a chord, or null */
    pending: string | null;
};

const EMPTY: KeyResult = {action: null, pending: null};

export function isTypingTarget(target: EventTarget | null): boolean {
    if (!(target instanceof HTMLElement)) {
        return false;
    }
    const tag = target.tagName.toLowerCase();
    if (tag === 'input' || tag === 'textarea' || tag === 'select') {
        return true;
    }
    if (target.isContentEditable) {
        return true;
    }
    const editable = target.getAttribute('contenteditable');
    return editable !== null && editable !== 'false';
}

function inputModifiedId(input: KeyInput): string {
    const mods: string[] = [];
    // Ctrl and Cmd are interchangeable (Cmd on macOS).
    if (input.ctrlKey || input.metaKey) {
        mods.push('ctrl');
    }
    if (input.shiftKey) {
        mods.push('shift');
    }
    return `${mods.sort().join('+')}|${input.key.toLowerCase()}`;
}

export function resolveKey(input: KeyInput, pending: string | null): KeyResult {
    // Modifier shortcuts (e.g. Ctrl+K) work even while typing in a field.
    if (input.ctrlKey || input.metaKey) {
        const modified = MODIFIED.get(inputModifiedId(input));
        if (modified) {
            return {action: modified, pending: null};
        }
    }
    if (isTypingTarget(input.target)) {
        return EMPTY;
    }
    // Leave browser/OS shortcuts alone.
    if (input.ctrlKey || input.metaKey || input.altKey) {
        return EMPTY;
    }
    if (input.key === 'Escape') {
        return {action: 'escape', pending: null};
    }

    if (pending) {
        const action = CHORDS.get(pending)?.get(input.key) ?? null;
        return {action, pending: null};
    }

    const single = SINGLE.get(input.key);
    if (single) {
        return {action: single, pending: null};
    }
    if (CHORDS.has(input.key)) {
        return {action: null, pending: input.key};
    }
    return EMPTY;
}

export const KEYMAP_GROUPS: Array<{group: string; bindings: KeyBinding[]}> = [
    'Navigate',
    'Review',
    'View',
    'Help',
].map((group) => ({
    group,
    bindings: KEYMAP.filter((binding) => binding.group === group),
}));

function keyLabel(key: string): string {
    if (key === 'ArrowDown') {
        return '↓';
    }
    if (key === 'ArrowUp') {
        return '↑';
    }
    if (key === 'Escape') {
        return 'Esc';
    }
    return key;
}

/** Compact display for a single key sequence, e.g. "g i" or "Ctrl+K". */
export function formatShortcut(keys: string[]): string {
    return keys.map(keyLabel).join(' ');
}

/** Human-readable list of every sequence for a command. */
export function formatShortcutList(sequences: string[][]): string {
    return sequences.map(formatShortcut).join(' / ');
}

export function formatKeys(keys: string[]): string {
    return keys.map(keyLabel).join(' then ');
}
