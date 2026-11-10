// One keymap definition powers both the global handler and the help overlay.

export type KeyAction =
    | 'next-file'
    | 'prev-file'
    | 'next-hunk'
    | 'prev-hunk'
    | 'next-line'
    | 'prev-line'
    | 'comment'
    | 'toggle-viewed'
    | 'approve'
    | 'submit'
    | 'toggle-tree'
    | 'toggle-split'
    | 'toggle-wrap'
    | 'next-comment'
    | 'prev-comment'
    | 'toggle-comments'
    | 'scroll-top'
    | 'inbox'
    | 'help'
    | 'escape';

export type KeyBinding = {
    action: KeyAction;
    /** e.g. ['j'] or ['g', 'i'] for a chord. */
    keys: string[];
    label: string;
    group: 'Navigate' | 'Review' | 'View' | 'Help';
};

export const KEYMAP: KeyBinding[] = [
    {action: 'next-file', keys: ['j'], label: 'Next file', group: 'Navigate'},
    {action: 'prev-file', keys: ['k'], label: 'Previous file', group: 'Navigate'},
    {action: 'next-hunk', keys: ['n'], label: 'Next hunk', group: 'Navigate'},
    {action: 'prev-hunk', keys: ['p'], label: 'Previous hunk', group: 'Navigate'},
    {action: 'next-line', keys: ['ArrowDown'], label: 'Next line', group: 'Navigate'},
    {action: 'prev-line', keys: ['ArrowUp'], label: 'Previous line', group: 'Navigate'},
    {action: 'next-line', keys: ['J'], label: 'Next line', group: 'Navigate'},
    {action: 'prev-line', keys: ['K'], label: 'Previous line', group: 'Navigate'},
    {action: 'next-comment', keys: [']'], label: 'Next comment thread', group: 'Navigate'},
    {action: 'prev-comment', keys: ['['], label: 'Previous comment thread', group: 'Navigate'},
    {action: 'scroll-top', keys: ['g', 'g'], label: 'Go to top of page', group: 'Navigate'},
    {action: 'scroll-top', keys: ['Home'], label: 'Go to top of page', group: 'Navigate'},
    {action: 'comment', keys: ['c'], label: 'Comment on focused line', group: 'Review'},
    {action: 'toggle-viewed', keys: ['v'], label: 'Toggle viewed, jump to next unviewed', group: 'Review'},
    {action: 'approve', keys: ['a'], label: 'Toggle approve', group: 'Review'},
    {action: 'submit', keys: ['s'], label: 'Submit review', group: 'Review'},
    {action: 'toggle-tree', keys: ['t'], label: 'Toggle file tree', group: 'View'},
    {action: 'toggle-split', keys: ['u'], label: 'Toggle split / unified', group: 'View'},
    {action: 'toggle-wrap', keys: ['w'], label: 'Toggle line wrapping', group: 'View'},
    {action: 'toggle-comments', keys: ['C'], label: 'Toggle comments panel', group: 'View'},
    {action: 'inbox', keys: ['g', 'i'], label: 'Go to inbox', group: 'Navigate'},
    {action: 'help', keys: ['?'], label: 'Show keyboard help', group: 'Help'},
    {action: 'escape', keys: ['Escape'], label: 'Close editor / overlay', group: 'Help'},
];

const SINGLE = new Map<string, KeyAction>();
const CHORDS = new Map<string, Map<string, KeyAction>>();

for (const binding of KEYMAP) {
    const [first, second, ...rest] = binding.keys;
    if (!first || rest.length > 0) {
        continue;
    }
    if (second === undefined) {
        if (!SINGLE.has(first)) {
            SINGLE.set(first, binding.action);
        }
        continue;
    }
    const next = CHORDS.get(first) ?? new Map<string, KeyAction>();
    next.set(second, binding.action);
    CHORDS.set(first, next);
}

export type KeyInput = {
    key: string;
    ctrlKey: boolean;
    metaKey: boolean;
    altKey: boolean;
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

export function resolveKey(input: KeyInput, pending: string | null): KeyResult {
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

export function formatKeys(keys: string[]): string {
    return keys
        .map((key) => {
            if (key === 'ArrowDown') return '↓';
            if (key === 'ArrowUp') return '↑';
            if (key === 'Escape') return 'Esc';
            return key;
        })
        .join(' then ');
}
