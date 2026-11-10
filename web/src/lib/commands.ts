import {lastInboxUrl} from './inboxUrl';
import {scrollPageToTop} from './scroll';
import {fuzzyMatch} from './fuzzy';
import type {FuzzyRange} from './fuzzy';

export type ViewName = 'inbox' | 'pr' | 'help';

export type CommandGroup = 'Navigation' | 'Review' | 'Comments' | 'View' | 'General';

export type CommandId =
    | 'next-file'
    | 'prev-file'
    | 'next-hunk'
    | 'prev-hunk'
    | 'next-line'
    | 'prev-line'
    | 'next-comment'
    | 'prev-comment'
    | 'scroll-top'
    | 'inbox'
    | 'open-palette'
    | 'comment'
    | 'toggle-viewed'
    | 'approve'
    | 'submit'
    | 'toggle-tree'
    | 'toggle-split'
    | 'toggle-wrap'
    | 'toggle-comments'
    | 'expand-context'
    | 'expand-context-all'
    | 'help'
    | 'help-page'
    | 'escape';

export type CommandContext = {
    view: ViewName;
    /** PR view: a line is focused, so `c` can open the editor. */
    hasFocus: boolean;
    /** PR view: the diff has files. */
    hasFiles: boolean;
    /** Run a command implemented by the current page. */
    dispatch: (id: string) => void;
    navigate: (to: string) => void;
    openPalette: () => void;
};

export type Command = {
    id: CommandId;
    title: string;
    group: CommandGroup;
    /** Every key sequence that triggers the command; chords are multi-element. */
    keys: string[][];
    when: (ctx: CommandContext) => boolean;
    run: (ctx: CommandContext) => void;
};

/** Palette-only entries contributed by the current page (files, PRs). */
export type DynamicCommand = {
    id: string;
    title: string;
    section: 'files' | 'prs';
    search: string[];
    run: () => void;
};

export const COMMAND_GROUP_ORDER: CommandGroup[] = [
    'Navigation',
    'Review',
    'Comments',
    'View',
    'General',
];

const inViews =
    (...views: ViewName[]) =>
    (ctx: CommandContext): boolean =>
        views.includes(ctx.view);

const onPr = inViews('pr');
const onPrWithFiles = (ctx: CommandContext): boolean => ctx.view === 'pr' && ctx.hasFiles;
const onPrWithFocus = (ctx: CommandContext): boolean => ctx.view === 'pr' && ctx.hasFocus;
const anywhere = (): boolean => true;

const dispatch =
    (id: CommandId) =>
    (ctx: CommandContext): void => {
        ctx.dispatch(id);
    };

export const COMMANDS: Command[] = [
    {
        id: 'next-file',
        title: 'Next file',
        group: 'Navigation',
        keys: [['j']],
        when: onPr,
        run: dispatch('next-file'),
    },
    {
        id: 'prev-file',
        title: 'Previous file',
        group: 'Navigation',
        keys: [['k']],
        when: onPr,
        run: dispatch('prev-file'),
    },
    {
        id: 'next-hunk',
        title: 'Next hunk',
        group: 'Navigation',
        keys: [['n']],
        when: onPr,
        run: dispatch('next-hunk'),
    },
    {
        id: 'prev-hunk',
        title: 'Previous hunk',
        group: 'Navigation',
        keys: [['p']],
        when: onPr,
        run: dispatch('prev-hunk'),
    },
    {
        id: 'next-line',
        title: 'Next line',
        group: 'Navigation',
        keys: [['ArrowDown'], ['J']],
        when: onPrWithFiles,
        run: dispatch('next-line'),
    },
    {
        id: 'prev-line',
        title: 'Previous line',
        group: 'Navigation',
        keys: [['ArrowUp'], ['K']],
        when: onPrWithFiles,
        run: dispatch('prev-line'),
    },
    {
        id: 'next-comment',
        title: 'Next comment thread',
        group: 'Comments',
        keys: [[']']],
        when: onPr,
        run: dispatch('next-comment'),
    },
    {
        id: 'prev-comment',
        title: 'Previous comment thread',
        group: 'Comments',
        keys: [['[']],
        when: onPr,
        run: dispatch('prev-comment'),
    },
    {
        id: 'toggle-comments',
        title: 'Toggle comments panel',
        group: 'Comments',
        keys: [['C']],
        when: onPr,
        run: dispatch('toggle-comments'),
    },
    {
        id: 'scroll-top',
        title: 'Go to top of page',
        group: 'Navigation',
        keys: [['g', 'g'], ['Home']],
        when: anywhere,
        run: () => scrollPageToTop(null),
    },
    {
        id: 'inbox',
        title: 'Go to inbox',
        group: 'Navigation',
        keys: [['g', 'i']],
        when: anywhere,
        run: (ctx) => ctx.navigate(lastInboxUrl()),
    },
    {
        id: 'open-palette',
        title: 'Command palette',
        group: 'Navigation',
        keys: [['Ctrl+K'], ['Ctrl+Shift+P']],
        when: anywhere,
        run: (ctx) => ctx.openPalette(),
    },
    {
        id: 'comment',
        title: 'Comment on focused line',
        group: 'Review',
        keys: [['c']],
        when: onPrWithFocus,
        run: dispatch('comment'),
    },
    {
        id: 'toggle-viewed',
        title: 'Toggle viewed, jump to next unviewed',
        group: 'Review',
        keys: [['v']],
        when: onPrWithFiles,
        run: dispatch('toggle-viewed'),
    },
    {
        id: 'approve',
        title: 'Toggle approve',
        group: 'Review',
        keys: [['a']],
        when: onPr,
        run: dispatch('approve'),
    },
    {
        id: 'submit',
        title: 'Submit review',
        group: 'Review',
        keys: [['s']],
        when: onPr,
        run: dispatch('submit'),
    },
    {
        id: 'toggle-tree',
        title: 'Toggle file tree',
        group: 'View',
        keys: [['t']],
        when: onPr,
        run: dispatch('toggle-tree'),
    },
    {
        id: 'toggle-split',
        title: 'Toggle split / unified',
        group: 'View',
        keys: [['u']],
        when: onPr,
        run: dispatch('toggle-split'),
    },
    {
        id: 'toggle-wrap',
        title: 'Toggle line wrapping',
        group: 'View',
        keys: [['w']],
        when: onPr,
        run: dispatch('toggle-wrap'),
    },
    {
        id: 'expand-context',
        title: 'Expand context gap',
        group: 'View',
        keys: [['e']],
        when: onPrWithFocus,
        run: dispatch('expand-context'),
    },
    {
        id: 'expand-context-all',
        title: 'Expand whole context gap',
        group: 'View',
        keys: [['E']],
        when: onPrWithFocus,
        run: dispatch('expand-context-all'),
    },
    {
        id: 'help',
        title: 'Keyboard shortcuts',
        group: 'General',
        keys: [['?']],
        when: (ctx) => ctx.view !== 'help',
        run: (ctx) => ctx.navigate('/help'),
    },
    {
        id: 'help-page',
        title: 'Help',
        group: 'General',
        keys: [],
        when: (ctx) => ctx.view !== 'help',
        run: (ctx) => ctx.navigate('/help'),
    },
    {
        id: 'escape',
        title: 'Close editor / overlay',
        group: 'General',
        keys: [['Escape']],
        when: onPr,
        run: dispatch('escape'),
    },
];

export const COMMAND_BY_ID = new Map<string, Command>(COMMANDS.map((command) => [command.id, command]));

/** Keys that only scroll the page; rapid repeats skip the toast animation. */
export const SCROLL_COMMANDS = new Set<CommandId>([
    'next-file',
    'prev-file',
    'next-hunk',
    'prev-hunk',
    'next-line',
    'prev-line',
    'next-comment',
    'prev-comment',
    'scroll-top',
]);

export function getCommand(id: string): Command | null {
    return COMMAND_BY_ID.get(id) ?? null;
}

export function isCommandEnabled(command: Command, ctx: CommandContext): boolean {
    return command.when(ctx);
}

export function makeCommandContext(
    view: ViewName,
    overrides: Partial<CommandContext> = {},
): CommandContext {
    return {
        view,
        hasFocus: false,
        hasFiles: false,
        dispatch: () => undefined,
        navigate: () => undefined,
        openPalette: () => undefined,
        ...overrides,
    };
}

export function commandsInGroup(group: CommandGroup): Command[] {
    return COMMANDS.filter((command) => command.group === group);
}

export function allCommandGroups(): Array<{group: CommandGroup; commands: Command[]}> {
    return COMMAND_GROUP_ORDER.map((group) => ({group, commands: commandsInGroup(group)}));
}

export function hasShortcut(command: Command): boolean {
    return command.keys.length > 0;
}

export type RankedCommand = {
    command: Command;
    score: number;
    ranges: FuzzyRange[];
};

function scoreCommand(query: string, command: Command): RankedCommand | null {
    const title = fuzzyMatch(query, command.title);
    const group = fuzzyMatch(query, command.group);
    const keys = fuzzyMatch(
        query,
        command.keys.map((sequence) => sequence.join(' ')).join(' '),
    );
    const best = Math.max(
        title ? 1000 + title.score : Number.NEGATIVE_INFINITY,
        group ? 300 + group.score : Number.NEGATIVE_INFINITY,
        keys ? 200 + keys.score : Number.NEGATIVE_INFINITY,
    );
    if (!Number.isFinite(best)) {
        return null;
    }
    return {command, score: best, ranges: title?.ranges ?? []};
}

/** Fuzzy-search the registry, split by availability in the current view. */
export function rankCommands(
    query: string,
    ctx: CommandContext,
): {enabled: RankedCommand[]; disabled: RankedCommand[]} {
    const trimmed = query.trim();
    const enabled: RankedCommand[] = [];
    const disabled: RankedCommand[] = [];
    COMMANDS.forEach((command, index) => {
        const available = isCommandEnabled(command, ctx);
        if (trimmed.length === 0) {
            if (!available) {
                return;
            }
            enabled.push({command, score: -index, ranges: []});
            return;
        }
        const ranked = scoreCommand(trimmed, command);
        if (!ranked) {
            return;
        }
        if (available) {
            enabled.push(ranked);
            return;
        }
        disabled.push(ranked);
    });
    enabled.sort((a, b) => b.score - a.score);
    disabled.sort((a, b) => b.score - a.score);
    return {enabled, disabled};
}
