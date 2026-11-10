import {useEffect, useMemo, useRef, useState} from 'react';
import {COMMAND_BY_ID, rankCommands} from '../lib/commands';
import type {CommandContext, DynamicCommand} from '../lib/commands';
import {fuzzyMatch, highlightSegments} from '../lib/fuzzy';
import type {FuzzyRange} from '../lib/fuzzy';
import {formatShortcutList} from '../lib/keymap';

export type PaletteEntry = {
    key: string;
    title: string;
    subtitle?: string;
    shortcut: string;
    ranges: FuzzyRange[];
    disabled: boolean;
    run: () => void;
};

export type PaletteSection = {
    title: string;
    entries: PaletteEntry[];
};

const DYNAMIC_SECTION: Record<DynamicCommand['section'], string> = {
    files: 'Files',
    prs: 'Pull requests',
};

export function buildPaletteSections(options: {
    query: string;
    ctx: CommandContext;
    recents: string[];
    dynamic: DynamicCommand[];
    runCommand: (id: string) => void;
}): PaletteSection[] {
    const {query, ctx, recents, dynamic, runCommand} = options;
    const sections: PaletteSection[] = [];
    const commandEntry = (commandId: string, ranges: FuzzyRange[], disabled: boolean): PaletteEntry | null => {
        const command = COMMAND_BY_ID.get(commandId);
        if (!command) {
            return null;
        }
        return {
            key: `command:${command.id}`,
            title: command.title,
            subtitle: command.group,
            shortcut: formatShortcutList(command.keys),
            ranges,
            disabled,
            run: () => runCommand(command.id),
        };
    };

    if (query.trim().length === 0) {
        const enabled = rankCommands('', ctx).enabled;
        const byId = new Map<string, (typeof enabled)[number]>(
            enabled.map((ranked) => [ranked.command.id, ranked]),
        );
        const recentEntries = recents
            .map((id) => byId.get(id))
            .filter((ranked): ranked is NonNullable<typeof ranked> => ranked !== undefined)
            .map((ranked) => commandEntry(ranked.command.id, [], false))
            .filter((entry): entry is PaletteEntry => entry !== null);
        if (recentEntries.length > 0) {
            sections.push({title: 'Recently used', entries: recentEntries});
        }
        const recentIds = new Set(recentEntries.map((entry) => entry.key.replace('command:', '')));
        const rest = enabled
            .filter((ranked) => !recentIds.has(ranked.command.id))
            .map((ranked) => commandEntry(ranked.command.id, [], false))
            .filter((entry): entry is PaletteEntry => entry !== null);
        if (rest.length > 0) {
            sections.push({title: 'All commands', entries: rest});
        }
        return sections;
    }

    const {enabled, disabled} = rankCommands(query, ctx);
    const commandEntries = enabled
        .map((ranked) => commandEntry(ranked.command.id, ranked.ranges, false))
        .filter((entry): entry is PaletteEntry => entry !== null);
    if (commandEntries.length > 0) {
        sections.push({title: 'Commands', entries: commandEntries});
    }

    const dynamicEntries: Record<DynamicCommand['section'], PaletteEntry[]> = {files: [], prs: []};
    for (const item of dynamic) {
        const titleMatch = fuzzyMatch(query, item.title);
        const searchMatches = item.search
            .map((text) => fuzzyMatch(query, text))
            .filter((match): match is NonNullable<typeof match> => match !== null);
        if (!titleMatch && searchMatches.length === 0) {
            continue;
        }
        dynamicEntries[item.section].push({
            key: `dynamic:${item.id}`,
            title: item.title,
            subtitle: DYNAMIC_SECTION[item.section],
            shortcut: '',
            ranges: titleMatch?.ranges ?? [],
            disabled: false,
            run: item.run,
        });
    }
    for (const section of ['files', 'prs'] as const) {
        const entries = dynamicEntries[section];
        if (entries.length === 0) {
            continue;
        }
        sections.push({title: DYNAMIC_SECTION[section], entries});
    }

    const disabledEntries = disabled
        .map((ranked) => commandEntry(ranked.command.id, ranked.ranges, true))
        .filter((entry): entry is PaletteEntry => entry !== null);
    if (disabledEntries.length > 0) {
        sections.push({title: 'Not available here', entries: disabledEntries});
    }

    return sections;
}

function Highlighted({text, ranges}: {text: string; ranges: FuzzyRange[]}) {
    return (
        <>
            {highlightSegments(text, ranges).map((segment, index) =>
                segment.match ? (
                    <strong key={`${segment.text}-${index}`} className="command-match">
                        {segment.text}
                    </strong>
                ) : (
                    <span key={`${segment.text}-${index}`}>{segment.text}</span>
                ),
            )}
        </>
    );
}

export type CommandPaletteProps = {
    open: boolean;
    onClose: () => void;
    ctx: CommandContext;
    recents: string[];
    dynamic: DynamicCommand[];
    onRun: (id: string) => void;
    onOpenHelp: () => void;
};

export function CommandPalette({
    open,
    onClose,
    ctx,
    recents,
    dynamic,
    onRun,
    onOpenHelp,
}: CommandPaletteProps) {
    const [query, setQuery] = useState('');
    const [active, setActive] = useState(0);
    const inputRef = useRef<HTMLInputElement>(null);
    const dialogRef = useRef<HTMLDivElement>(null);

    const sections = useMemo(
        () => buildPaletteSections({query, ctx, recents, dynamic, runCommand: onRun}),
        [query, ctx, recents, dynamic, onRun],
    );
    const entries = useMemo(() => sections.flatMap((section) => section.entries), [sections]);

    useEffect(() => {
        if (!open) {
            return;
        }
        setQuery('');
        setActive(0);
        const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
        const frame = window.requestAnimationFrame(() => inputRef.current?.focus());

        function onKeyDown(event: KeyboardEvent) {
            if (event.key === 'Escape') {
                event.preventDefault();
                event.stopPropagation();
                onClose();
                return;
            }
            if (event.key !== 'Tab') {
                return;
            }
            const focusables = dialogRef.current?.querySelectorAll<HTMLElement>(
                'input, button, a[href]',
            );
            if (!focusables || focusables.length === 0) {
                return;
            }
            const first = focusables[0]!;
            const last = focusables[focusables.length - 1]!;
            if (!event.shiftKey && document.activeElement === last) {
                event.preventDefault();
                first.focus();
                return;
            }
            if (event.shiftKey && document.activeElement === first) {
                event.preventDefault();
                last.focus();
            }
        }

        document.addEventListener('keydown', onKeyDown, true);
        return () => {
            document.removeEventListener('keydown', onKeyDown, true);
            window.cancelAnimationFrame(frame);
            previous?.focus();
        };
    }, [open, onClose]);

    useEffect(() => {
        setActive((current) => Math.min(current, Math.max(0, entries.length - 1)));
    }, [entries.length]);

    useEffect(() => {
        if (!open) {
            return;
        }
        const option = document.getElementById(`palette-option-${active}`);
        if (!option || typeof option.scrollIntoView !== 'function') {
            return;
        }
        option.scrollIntoView({block: 'nearest'});
    }, [active, open]);

    if (!open) {
        return null;
    }

    function move(delta: number) {
        if (entries.length === 0) {
            return;
        }
        setActive((current) => {
            const next = current + delta;
            return (next + entries.length) % entries.length;
        });
    }

    function runEntry(entry: PaletteEntry | undefined) {
        if (!entry || entry.disabled) {
            return;
        }
        onClose();
        entry.run();
    }

    let optionIndex = 0;

    return (
        <div className="overlay command-overlay" role="presentation" onMouseDown={onClose}>
            <div
                ref={dialogRef}
                className="command-palette"
                role="dialog"
                aria-modal="true"
                aria-label="Command palette"
                onMouseDown={(event) => event.stopPropagation()}
            >
                <input
                    ref={inputRef}
                    className="command-input"
                    type="text"
                    role="combobox"
                    aria-expanded="true"
                    aria-controls="palette-results"
                    aria-activedescendant={entries.length > 0 ? `palette-option-${active}` : undefined}
                    aria-autocomplete="list"
                    aria-label="Search commands"
                    autoComplete="off"
                    spellCheck={false}
                    placeholder="Type a command, file or pull request…"
                    value={query}
                    onChange={(event) => {
                        setQuery(event.target.value);
                        setActive(0);
                    }}
                    onKeyDown={(event) => {
                        if (event.key === 'ArrowDown') {
                            event.preventDefault();
                            move(1);
                            return;
                        }
                        if (event.key === 'ArrowUp') {
                            event.preventDefault();
                            move(-1);
                            return;
                        }
                        if (event.key === 'Enter') {
                            event.preventDefault();
                            runEntry(entries[active]);
                        }
                    }}
                />

                <div className="command-results" id="palette-results" role="listbox" aria-label="Commands">
                    {entries.length === 0 ? (
                        <p className="command-empty">No matching commands.</p>
                    ) : null}
                    {sections.map((section) => (
                        <div className="command-section" key={section.title}>
                            <div className="command-section-title" role="presentation">
                                {section.title}
                            </div>
                            {section.entries.map((entry) => {
                                const index = optionIndex;
                                optionIndex += 1;
                                return (
                                    <div
                                        key={entry.key}
                                        id={`palette-option-${index}`}
                                        role="option"
                                        aria-selected={index === active}
                                        aria-disabled={entry.disabled}
                                        className={`command-option${index === active ? ' is-active' : ''}${
                                            entry.disabled ? ' is-disabled' : ''
                                        }`}
                                        onMouseDown={(event) => event.preventDefault()}
                                        onMouseEnter={() => setActive(index)}
                                        onClick={() => runEntry(entry)}
                                    >
                                        <span className="command-option-main">
                                            <span className="command-option-title">
                                                <Highlighted text={entry.title} ranges={entry.ranges} />
                                            </span>
                                            {entry.subtitle ? (
                                                <span className="command-option-group">{entry.subtitle}</span>
                                            ) : null}
                                        </span>
                                        {entry.shortcut ? <kbd>{entry.shortcut}</kbd> : null}
                                    </div>
                                );
                            })}
                        </div>
                    ))}
                </div>

                <div className="command-foot">
                    <button
                        type="button"
                        className="btn btn-small btn-ghost"
                        onClick={() => {
                            onOpenHelp();
                            onClose();
                        }}
                    >
                        Help
                    </button>
                    <span className="command-foot-hint">
                        ↑↓ move · Enter run · Esc close · <kbd>Ctrl+K</kbd>
                    </span>
                </div>
            </div>
        </div>
    );
}
