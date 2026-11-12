// Renders ```mermaid blocks into diagrams after mount. Mermaid is heavy, so it
// is pulled in with a dynamic import (its own chunk) only when a diagram exists.
type MermaidApi = typeof import('mermaid')['default'];

const PROCESSED_ATTR = 'data-mermaid-rendered';

let mermaidPromise: Promise<MermaidApi> | null = null;
let diagramCounter = 0;

/** Cheap check used by the React layer to skip work for plain blocks. */
export function hasMermaid(html: string): boolean {
    return html.includes('data-mermaid');
}

function loadMermaid(): Promise<MermaidApi> {
    if (mermaidPromise) {
        return mermaidPromise;
    }
    mermaidPromise = import('mermaid').then(({default: mermaid}) => {
        const dark = window.matchMedia('(prefers-color-scheme: dark)').matches;
        mermaid.initialize({
            startOnLoad: false,
            securityLevel: 'strict',
            theme: dark ? 'dark' : 'default',
        });
        return mermaid;
    });
    return mermaidPromise;
}

function nextId(): string {
    diagramCounter += 1;
    return `bb-mermaid-${diagramCounter}`;
}

function messageOf(error: unknown): string {
    const message = error instanceof Error ? error.message : String(error ?? '');
    if (message.length === 0) {
        return 'Diagram could not be rendered';
    }
    return message.length > 300 ? `${message.slice(0, 297)}…` : message;
}

/** Mermaid leaves its temporary `#d<id>` wrapper behind when rendering fails. */
function removeTempElement(id: string): void {
    document.getElementById(`d${id}`)?.remove();
}

function replaceWithDiagram(pre: HTMLPreElement, svg: string): void {
    const container = document.createElement('div');
    container.className = 'md-mermaid-diagram';
    container.innerHTML = svg;
    pre.replaceWith(container);
}

function markFailed(pre: HTMLPreElement, error: unknown): void {
    pre.classList.add('md-mermaid-error');
    pre.title = messageOf(error);
}

/**
 * Replace every not-yet-rendered `pre[data-mermaid]` under `root` with the SVG
 * produced by mermaid. Failures leave the plain source visible.
 */
export async function renderMermaidIn(root: HTMLElement): Promise<void> {
    const pending = Array.from(root.querySelectorAll<HTMLPreElement>('pre[data-mermaid]')).filter(
        (pre) => !pre.hasAttribute(PROCESSED_ATTR),
    );
    if (pending.length === 0) {
        return;
    }
    // Mark up front so competing runs do not render the same block twice.
    for (const pre of pending) {
        pre.setAttribute(PROCESSED_ATTR, '1');
    }

    let mermaid: MermaidApi;
    try {
        mermaid = await loadMermaid();
    } catch {
        // Allow a later block to retry (e.g. after a transient chunk load failure).
        mermaidPromise = null;
        for (const pre of pending) {
            pre.removeAttribute(PROCESSED_ATTR);
        }
        return;
    }

    for (const pre of pending) {
        // The block may have been unmounted (or replaced) while mermaid loaded.
        if (!pre.isConnected) {
            continue;
        }
        const id = nextId();
        try {
            const {svg} = await mermaid.render(id, pre.textContent ?? '');
            if (!pre.isConnected) {
                continue;
            }
            replaceWithDiagram(pre, svg);
        } catch (error) {
            removeTempElement(id);
            if (!pre.isConnected) {
                continue;
            }
            markFailed(pre, error);
        }
    }
}
