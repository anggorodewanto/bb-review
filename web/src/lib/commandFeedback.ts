// Command-run feedback: toast coalescing, detail text and DOM highlighting.

export const TOAST_WINDOW_MS = 1200;
export const RAPID_SCROLL_MS = 150;
export const FLASH_MS = 700;

export type CommandToast = {
    commandId: string;
    title: string;
    keys: string[];
    count: number;
    detail?: string;
    /** False for rapid repeated scrolling, so the toast does not re-animate. */
    animate: boolean;
    lastRunAt: number;
};

export type ToastInput = {
    commandId: string;
    title: string;
    keys: string[];
    detail?: string;
    isScroll: boolean;
    now: number;
};

let pendingDetail: string | null = null;

/** A page handler can attach extra text (e.g. "2/12") to the run's toast. */
export function setCommandDetail(text: string): void {
    pendingDetail = text;
}

export function takeCommandDetail(): string | null {
    const detail = pendingDetail;
    pendingDetail = null;
    return detail;
}

export function clearCommandDetail(): void {
    pendingDetail = null;
}

export function nextCommandToast(previous: CommandToast | null, input: ToastInput): CommandToast {
    const gap = previous ? input.now - previous.lastRunAt : Number.POSITIVE_INFINITY;
    const sameRun = previous !== null && previous.commandId === input.commandId && gap <= TOAST_WINDOW_MS;
    if (!sameRun) {
        return {
            commandId: input.commandId,
            title: input.title,
            keys: input.keys,
            count: 1,
            detail: input.detail,
            animate: true,
            lastRunAt: input.now,
        };
    }
    const rapidScroll = input.isScroll && gap < RAPID_SCROLL_MS;
    return {
        ...previous,
        title: input.title,
        keys: input.keys,
        count: previous.count + 1,
        detail: input.detail,
        animate: !rapidScroll,
        lastRunAt: input.now,
    };
}

/**
 * Pulse every control currently in the DOM that maps to this command.
 * Uses a data attribute instead of a class so React re-renders (which own
 * `className`) cannot wipe the highlight.
 * Returns the number of highlighted elements.
 */
export function highlightCommand(id: string, root: ParentNode = document): number {
    const nodes = Array.from(root.querySelectorAll<HTMLElement>(`[data-command="${id}"]`));
    for (const node of nodes) {
        node.removeAttribute('data-command-flash');
        // Force a reflow so a repeated run restarts the animation.
        void node.offsetWidth;
        node.setAttribute('data-command-flash', '');
    }
    if (nodes.length > 0) {
        window.setTimeout(() => {
            for (const node of nodes) {
                node.removeAttribute('data-command-flash');
            }
        }, FLASH_MS);
    }
    return nodes.length;
}
