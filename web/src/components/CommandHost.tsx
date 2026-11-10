import {createContext, useCallback, useContext, useEffect, useMemo, useRef, useState} from 'react';
import type {ReactNode} from 'react';
import {useNavigate} from 'react-router-dom';
import {getCommand, makeCommandContext, SCROLL_COMMANDS} from '../lib/commands';
import type {CommandContext, DynamicCommand, ViewName} from '../lib/commands';
import {clearCommandDetail, highlightCommand, nextCommandToast, takeCommandDetail} from '../lib/commandFeedback';
import type {CommandToast} from '../lib/commandFeedback';
import {resolveKey} from '../lib/keymap';
import {formatShortcut} from '../lib/keymap';
import {readRecentCommands, rememberCommand} from '../lib/recentCommands';
import {isCodeScrollTarget} from '../lib/scroll';
import {CommandPalette} from './CommandPalette';

export type PageCommands = {
    view: ViewName;
    handlers: Record<string, (() => void) | undefined>;
    hasFocus?: boolean;
    hasFiles?: boolean;
    hasMarkdown?: boolean;
    currentFileMarkdown?: boolean;
    hasRenderedMarkdown?: boolean;
    dynamic?: DynamicCommand[];
};

export type CommandHost = {
    openPalette: () => void;
    isPaletteOpen: () => boolean;
    setPage: (page: PageCommands) => void;
    run: (id: string) => boolean;
};

const HostContext = createContext<CommandHost | null>(null);

function noop(): void {
    return undefined;
}

function toKeyInput(event: KeyboardEvent): {
    key: string;
    ctrlKey: boolean;
    metaKey: boolean;
    altKey: boolean;
    shiftKey: boolean;
    target: EventTarget | null;
} {
    return {
        key: event.key,
        ctrlKey: event.ctrlKey,
        metaKey: event.metaKey,
        altKey: event.altKey,
        shiftKey: event.shiftKey,
        target: event.target,
    };
}

export function CommandHostProvider({children}: {children: ReactNode}) {
    const navigate = useNavigate();
    const pageRef = useRef<PageCommands | null>(null);
    const [paletteOpen, setPaletteOpen] = useState(false);
    const [toast, setToast] = useState<CommandToast | null>(null);
    const [recents, setRecents] = useState<string[]>(() => readRecentCommands());
    const paletteOpenRef = useRef(false);
    paletteOpenRef.current = paletteOpen;

    const setPage = useCallback((page: PageCommands) => {
        pageRef.current = page;
    }, []);

    const openPalette = useCallback(() => setPaletteOpen(true), []);
    const closePalette = useCallback(() => setPaletteOpen(false), []);

    const buildContext = useCallback((): CommandContext => {
        const page = pageRef.current;
        return {
            view: page?.view ?? 'help',
            hasFocus: page?.hasFocus ?? false,
            hasFiles: page?.hasFiles ?? false,
            hasMarkdown: page?.hasMarkdown ?? false,
            currentFileMarkdown: page?.currentFileMarkdown ?? false,
            hasRenderedMarkdown: page?.hasRenderedMarkdown ?? false,
            dispatch: (id: string) => {
                page?.handlers[id]?.();
            },
            navigate: (to: string) => navigate(to),
            openPalette: () => setPaletteOpen(true),
        };
    }, [navigate]);

    const run = useCallback(
        (id: string): boolean => {
            const command = getCommand(id);
            if (!command) {
                return false;
            }
            const ctx = buildContext();
            if (!command.when(ctx)) {
                return false;
            }
            clearCommandDetail();
            const handler = pageRef.current?.handlers[id];
            if (handler) {
                handler();
            } else {
                command.run(ctx);
            }
            const detail = takeCommandDetail();
            highlightCommand(command.id);
            setRecents(rememberCommand(command.id));
            setToast((previous) =>
                nextCommandToast(previous, {
                    commandId: command.id,
                    title: command.title,
                    keys: command.keys[0] ?? [],
                    detail: detail ?? undefined,
                    isScroll: SCROLL_COMMANDS.has(command.id),
                    now: Date.now(),
                }),
            );
            return true;
        },
        [buildContext],
    );

    useEffect(() => {
        if (!toast) {
            return;
        }
        const timer = window.setTimeout(() => setToast(null), 1200);
        return () => window.clearTimeout(timer);
    }, [toast]);

    useEffect(() => {
        const pending = {current: null as string | null};
        function onKeyDown(event: KeyboardEvent) {
            const result = resolveKey(toKeyInput(event), pending.current);
            pending.current = result.pending;
            if (!result.action) {
                return;
            }
            if (paletteOpenRef.current) {
                if (result.action === 'open-palette') {
                    event.preventDefault();
                    setPaletteOpen(false);
                }
                return;
            }
            if (result.action === 'scroll-top' && event.key === 'Home' && isCodeScrollTarget(event.target)) {
                return;
            }
            const command = getCommand(result.action);
            if (!command) {
                return;
            }
            if (!command.when(buildContext())) {
                return;
            }
            event.preventDefault();
            run(result.action);
        }

        window.addEventListener('keydown', onKeyDown);
        return () => window.removeEventListener('keydown', onKeyDown);
    }, [buildContext, run]);

    const host = useMemo<CommandHost>(
        () => ({
            openPalette,
            isPaletteOpen: () => paletteOpenRef.current,
            setPage,
            run,
        }),
        [openPalette, setPage, run],
    );

    return (
        <HostContext.Provider value={host}>
            {children}
            <CommandPalette
                open={paletteOpen}
                onClose={closePalette}
                ctx={buildContext()}
                recents={recents}
                dynamic={pageRef.current?.dynamic ?? []}
                onRun={(id) => {
                    setPaletteOpen(false);
                    run(id);
                }}
                onOpenHelp={() => navigate('/help')}
            />
            {toast ? (
                <div className={`command-toast${toast.animate ? ' is-new' : ''}`} role="status" aria-live="polite">
                    {toast.keys.length > 0 ? (
                        <>
                            <kbd>{formatShortcut(toast.keys)}</kbd>
                            <span aria-hidden="true">·</span>
                        </>
                    ) : null}
                    <span className="command-toast-title">{toast.title}</span>
                    {toast.count > 1 ? (
                        <span className="command-toast-count">×{toast.count}</span>
                    ) : null}
                    {toast.detail ? <span className="command-toast-detail">{toast.detail}</span> : null}
                </div>
            ) : null}
        </HostContext.Provider>
    );
}

/**
 * Register the page's commands and (when there is no provider, e.g. isolated
 * component tests) install a minimal keyboard handler.
 */
export function usePageCommands(page: PageCommands): CommandHost {
    const host = useContext(HostContext);
    const navigate = useNavigate();
    const pageRef = useRef(page);
    pageRef.current = page;

    if (host) {
        host.setPage(page);
    }

    const runLocal = useCallback(
        (id: string): boolean => {
            const command = getCommand(id);
            if (!command) {
                return false;
            }
            const current = pageRef.current;
            const ctx = makeCommandContext(current.view, {
                hasFocus: current.hasFocus ?? false,
                hasFiles: current.hasFiles ?? false,
                hasMarkdown: current.hasMarkdown ?? false,
                currentFileMarkdown: current.currentFileMarkdown ?? false,
                hasRenderedMarkdown: current.hasRenderedMarkdown ?? false,
                navigate: (to) => navigate(to),
            });
            if (!command.when(ctx)) {
                return false;
            }
            const handler = current.handlers[command.id];
            if (handler) {
                handler();
                return true;
            }
            command.run(ctx);
            return true;
        },
        [navigate],
    );

    useEffect(() => {
        if (host) {
            return;
        }
        const pending = {current: null as string | null};
        function onKeyDown(event: KeyboardEvent) {
            const result = resolveKey(toKeyInput(event), pending.current);
            pending.current = result.pending;
            if (!result.action) {
                return;
            }
            if (result.action === 'scroll-top' && event.key === 'Home' && isCodeScrollTarget(event.target)) {
                return;
            }
            const command = getCommand(result.action);
            if (!command) {
                return;
            }
            const ctx = makeCommandContext(pageRef.current.view, {
                hasFocus: pageRef.current.hasFocus ?? false,
                hasFiles: pageRef.current.hasFiles ?? false,
                hasMarkdown: pageRef.current.hasMarkdown ?? false,
                currentFileMarkdown: pageRef.current.currentFileMarkdown ?? false,
                hasRenderedMarkdown: pageRef.current.hasRenderedMarkdown ?? false,
                navigate: (to) => navigate(to),
            });
            if (!command.when(ctx)) {
                return;
            }
            event.preventDefault();
            runLocal(result.action);
        }
        window.addEventListener('keydown', onKeyDown);
        return () => window.removeEventListener('keydown', onKeyDown);
    }, [host, navigate, runLocal]);

    return useMemo(
        () =>
            host ?? {
                openPalette: noop,
                isPaletteOpen: () => false,
                setPage: noop,
                run: runLocal,
            },
        [host, runLocal],
    );
}

/** Header button that opens the palette. Hidden when no host is mounted. */
export function CommandButton() {
    const host = useContext(HostContext);
    if (!host) {
        return null;
    }
    return (
        <button
            type="button"
            className="btn btn-small command-btn"
            data-command="open-palette"
            onClick={host.openPalette}
            aria-label="Command palette"
            title="Command palette (Ctrl+K)"
        >
            <span className="command-btn-icon" aria-hidden="true">
                ⌘
            </span>
            <span className="command-btn-label">Command</span>
        </button>
    );
}
