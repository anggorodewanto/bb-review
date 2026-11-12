import {beforeEach, describe, expect, it, vi} from 'vitest';

const {initializeMock, renderMock} = vi.hoisted(() => ({
    initializeMock: vi.fn(),
    renderMock: vi.fn(),
}));

vi.mock('mermaid', () => ({
    default: {initialize: initializeMock, render: renderMock},
}));

type MermaidModule = typeof import('./mermaid');

const PRE =
    '<pre class="md-pre md-mermaid" data-mermaid="1"><code>graph TD; A--&gt;B;</code></pre>';

function setup(html: string): HTMLElement {
    const root = document.createElement('div');
    root.innerHTML = html;
    document.body.appendChild(root);
    return root;
}

async function load(): Promise<MermaidModule> {
    return import('./mermaid');
}

beforeEach(() => {
    vi.resetModules();
    initializeMock.mockReset();
    renderMock.mockReset();
    document.body.innerHTML = '';
});

describe('hasMermaid', () => {
    it('detects the marker and ignores plain HTML', async () => {
        const {hasMermaid} = await load();
        expect(hasMermaid(PRE)).toBe(true);
        expect(hasMermaid('<p>hello</p>')).toBe(false);
    });
});

describe('renderMermaidIn', () => {
    it('replaces the source block with the rendered svg', async () => {
        renderMock.mockResolvedValue({svg: '<svg id="diagram">x</svg>'});
        const {renderMermaidIn} = await load();
        const root = setup(`<div class="markdown-body">${PRE}</div>`);

        await renderMermaidIn(root);

        const diagram = root.querySelector('.md-mermaid-diagram');
        expect(diagram).not.toBeNull();
        expect(diagram?.innerHTML).toContain('<svg id="diagram">');
        expect(root.querySelector('pre[data-mermaid]')).toBeNull();
        expect(renderMock).toHaveBeenCalledWith(expect.any(String), 'graph TD; A-->B;');
        expect(initializeMock).toHaveBeenCalledWith({
            startOnLoad: false,
            securityLevel: 'strict',
            theme: 'default',
        });
    });

    it('keeps the source and marks it when rendering fails', async () => {
        renderMock.mockImplementation((id: string) => {
            const temp = document.createElement('div');
            temp.id = `d${id}`;
            document.body.appendChild(temp);
            return Promise.reject(new Error('Parse error on line 1'));
        });
        const {renderMermaidIn} = await load();
        const root = setup(PRE);

        await renderMermaidIn(root);

        const pre = root.querySelector<HTMLPreElement>('pre[data-mermaid]');
        expect(pre).not.toBeNull();
        expect(pre?.classList.contains('md-mermaid-error')).toBe(true);
        expect(pre?.title).toBe('Parse error on line 1');
        expect(root.querySelector('.md-mermaid-diagram')).toBeNull();
        expect(document.querySelector('[id^="dbb-mermaid-"]')).toBeNull();
    });

    it('does not render an already processed block twice', async () => {
        renderMock.mockRejectedValue(new Error('bad diagram'));
        const {renderMermaidIn} = await load();
        const root = setup(PRE);

        await renderMermaidIn(root);
        await renderMermaidIn(root);

        expect(renderMock).toHaveBeenCalledTimes(1);
    });

    it('does nothing without mermaid blocks', async () => {
        const {renderMermaidIn} = await load();
        const root = setup('<pre class="md-pre"><code>x</code></pre>');

        await renderMermaidIn(root);

        expect(renderMock).not.toHaveBeenCalled();
        expect(initializeMock).not.toHaveBeenCalled();
    });
});
