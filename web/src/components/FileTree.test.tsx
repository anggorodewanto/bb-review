import {describe, expect, it, vi} from 'vitest';
import {render, screen} from '@testing-library/react';
import {FileTree} from './FileTree';
import type {FileTreeProps} from './FileTree';
import {buildFileEntries} from '../lib/diff';

const DIFF = [
    'diff --git a/src/app.ts b/src/app.ts',
    'index 1111111..2222222 100644',
    '--- a/src/app.ts',
    '+++ b/src/app.ts',
    '@@ -1,1 +1,2 @@',
    ' a',
    '+b',
    'diff --git a/node_modules/pkg/index.js b/node_modules/pkg/index.js',
    'index 3333333..4444444 100644',
    '--- a/node_modules/pkg/index.js',
    '+++ b/node_modules/pkg/index.js',
    '@@ -1,1 +1,2 @@',
    ' x',
    '+y',
    'diff --git a/README.md b/README.md',
    'index 5555555..6666666 100644',
    '--- a/README.md',
    '+++ b/README.md',
    '@@ -1,1 +1,2 @@',
    ' hi',
    '+there',
    '',
].join('\n');

/** `src/app.ts` (0), `node_modules/pkg/index.js` (1), `README.md` (2). */
const NOISE_INDEX = 1;

const files = buildFileEntries(DIFF);

function treeProps(overrides: Partial<FileTreeProps> = {}): FileTreeProps {
    return {
        files,
        currentIndex: 0,
        comments: {},
        viewedFiles: {},
        visible: true,
        onSelect: () => undefined,
        onToggleViewed: () => undefined,
        ...overrides,
    };
}

function renderTree(overrides: Partial<FileTreeProps> = {}) {
    const props = treeProps(overrides);
    const view = render(<FileTree {...props} />);
    return {
        view,
        rerender: (next: Partial<FileTreeProps>) => view.rerender(<FileTree {...props} {...next} />),
    };
}

function rect(top: number, height: number): DOMRect {
    return {
        top,
        bottom: top + height,
        left: 0,
        right: 0,
        width: 0,
        height,
        x: 0,
        y: top,
        toJSON: () => ({}),
    };
}

/** jsdom has no layout: fake the list's box and the current row's box. */
function stubLayout(containerRect: DOMRect, rowRect: DOMRect): void {
    vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function (
        this: Element,
    ): DOMRect {
        if (this.classList.contains('tree-scroll')) {
            return containerRect;
        }
        if (this.classList.contains('is-current')) {
            return rowRect;
        }
        return rect(0, 0);
    });
    vi.spyOn(Element.prototype, 'clientHeight', 'get').mockReturnValue(300);
}

function scrollContainer(view: {container: HTMLElement}): HTMLElement {
    const element = view.container.querySelector('.tree-scroll');
    expect(element).not.toBeNull();
    return element as HTMLElement;
}

describe('FileTree current file', () => {
    it('marks only the current row as current for assistive tech', () => {
        renderTree({currentIndex: 0});

        expect(screen.getByRole('button', {name: /app\.ts/})).toHaveAttribute(
            'aria-current',
            'true',
        );
        expect(screen.getByRole('button', {name: /README\.md/})).not.toHaveAttribute(
            'aria-current',
        );
    });

    it('reveals the current file inside a collapsed noise folder', () => {
        renderTree({currentIndex: NOISE_INDEX});

        expect(screen.getByRole('button', {name: /index\.js/})).toBeInTheDocument();
    });

    it('leaves the noise folder collapsed while the sidebar is hidden', () => {
        renderTree({currentIndex: NOISE_INDEX, visible: false});

        expect(screen.queryByRole('button', {name: /index\.js/})).toBeNull();
    });
});

describe('FileTree scroll to the current file', () => {
    it('centres a row that sits below the visible part of the list', () => {
        // Container 0..300; row 400..430 => 400 - (300 - 30) / 2 = 265.
        stubLayout(rect(0, 300), rect(400, 30));

        const {view} = renderTree({currentIndex: 2});

        expect(scrollContainer(view).scrollTop).toBe(265);
    });

    it('does not move the list when the row is already inside it', () => {
        stubLayout(rect(0, 300), rect(100, 30));

        const {view} = renderTree({currentIndex: 2});

        expect(scrollContainer(view).scrollTop).toBe(0);
    });

    it('only scrolls once the sidebar becomes visible', () => {
        stubLayout(rect(0, 300), rect(400, 30));

        const {view, rerender} = renderTree({currentIndex: 2, visible: false});
        const container = scrollContainer(view);
        expect(container.scrollTop).toBe(0);

        rerender({visible: true});
        expect(container.scrollTop).toBe(265);
    });
});
