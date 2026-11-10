import {describe, expect, it} from 'vitest';
import {parseDiff} from 'react-diff-view';
import {
    apiFileUrl,
    changedLineNumbers,
    encodeRepoPath,
    isMarkdownPath,
    markChangedBlocks,
    markdownBlockSpans,
    renderMarkdown,
    resolveHref,
    resolveRepoPath,
    sourceFileUrl,
} from './markdown';

const OPTIONS = {
    filePath: 'docs/guide/intro.md',
    ws: 'ws',
    repo: 'repo',
    commit: 'abc123',
    changedLines: new Set<number>(),
};

function render(source: string, changedLines: ReadonlySet<number> = new Set()): string {
    return renderMarkdown(source, {...OPTIONS, changedLines})
        .map((block) => block.html)
        .join('');
}

describe('isMarkdownPath', () => {
    it('matches md, markdown and mdx', () => {
        expect(isMarkdownPath('README.md')).toBe(true);
        expect(isMarkdownPath('docs/Guide.MARKDOWN')).toBe(true);
        expect(isMarkdownPath('pages/x.mdx')).toBe(true);
        expect(isMarkdownPath('src/a.ts')).toBe(false);
        expect(isMarkdownPath('notes.txt')).toBe(false);
    });
});

describe('resolveRepoPath', () => {
    it('resolves sibling, parent and root-relative paths', () => {
        expect(resolveRepoPath('docs/guide/intro.md', './img/x.png')).toBe('docs/guide/img/x.png');
        expect(resolveRepoPath('docs/guide/intro.md', 'img/x.png')).toBe('docs/guide/img/x.png');
        expect(resolveRepoPath('docs/guide/intro.md', '../assets/a.png')).toBe('docs/assets/a.png');
        expect(resolveRepoPath('docs/guide/intro.md', '../../x.png')).toBe('x.png');
        expect(resolveRepoPath('docs/guide/intro.md', '/assets/x.png')).toBe('assets/x.png');
    });

    it('strips query and hash', () => {
        expect(resolveRepoPath('a/b.md', './c.md?plain=1#L2')).toBe('a/c.md');
    });

    it('returns null when leaving the repo root', () => {
        expect(resolveRepoPath('a.md', '../outside.png')).toBeNull();
    });
});

describe('resolveHref', () => {
    it('classifies anchors, external and repo links', () => {
        expect(resolveHref('docs/a.md', '#installation')).toEqual({
            kind: 'anchor',
            href: '#installation',
        });
        expect(resolveHref('docs/a.md', 'https://example.com/x')).toEqual({
            kind: 'external',
            href: 'https://example.com/x',
        });
        expect(resolveHref('docs/a.md', 'mailto:me@example.com')).toEqual({
            kind: 'external',
            href: 'mailto:me@example.com',
        });
        expect(resolveHref('docs/a.md', './b.md')).toEqual({
            kind: 'repo',
            path: 'docs/b.md',
            hash: '',
        });
        expect(resolveHref('docs/a.md', './b.md#section')).toEqual({
            kind: 'repo',
            path: 'docs/b.md',
            hash: 'section',
        });
    });

    it('rejects script and data urls', () => {
        expect(resolveHref('a.md', 'javascript:alert(1)').kind).toBe('unsafe');
        expect(resolveHref('a.md', 'data:text/html,<script>x</script>').kind).toBe('unsafe');
        expect(resolveHref('a.md', '').kind).toBe('unsafe');
    });
});

describe('url builders', () => {
    it('encodes path segments but keeps slashes', () => {
        expect(encodeRepoPath('docs/my guide/a b.png')).toBe('docs/my%20guide/a%20b.png');
    });

    it('builds Bitbucket and API file urls', () => {
        expect(sourceFileUrl('ws', 'repo', 'abc', 'docs/a.md')).toBe(
            'https://bitbucket.org/ws/repo/src/abc/docs/a.md',
        );
        expect(apiFileUrl('ws', 'repo', 'abc', 'docs/a b.png')).toBe(
            '/api/file/ws/repo/abc/docs/a%20b.png',
        );
    });
});

describe('markdownBlockSpans', () => {
    const source = `# Title

Intro paragraph
with two lines.

- item one
- item two

| a | b |
|---|---|
| 1 | 2 |
`;

    it('computes 1-based spans for top-level blocks', () => {
        expect(markdownBlockSpans(source)).toEqual([
            {startLine: 1, endLine: 1},
            {startLine: 3, endLine: 4},
            {startLine: 6, endLine: 7},
            {startLine: 9, endLine: 11},
        ]);
    });

    it('marks blocks containing changed lines', () => {
        const spans = markdownBlockSpans(source);
        expect(markChangedBlocks(spans, new Set([4]))).toEqual([false, true, false, false]);
        expect(markChangedBlocks(spans, new Set([10]))).toEqual([false, false, false, true]);
        expect(markChangedBlocks(spans, new Set([99]))).toEqual([false, false, false, false]);
    });
});

describe('changedLineNumbers', () => {
    const DIFF = `diff --git a/README.md b/README.md
index 1111111..2222222 100644
--- a/README.md
+++ b/README.md
@@ -1,2 +1,3 @@
 keep
+added
-old
`;
    const file = parseDiff(DIFF)[0]!;

    it('collects inserted new-side lines', () => {
        expect([...changedLineNumbers(file.hunks, 'new')]).toEqual([2]);
    });

    it('collects deleted old-side lines', () => {
        expect([...changedLineNumbers(file.hunks, 'old')]).toEqual([2]);
    });
});

describe('renderMarkdown', () => {
    it('renders GFM headings, tables, task lists and code', () => {
        const html = render(`# Hello

| a | b |
|---|---|
| 1 | 2 |

- [x] done
- [ ] todo

\`\`\`js
const x = 1;
\`\`\`
`);
        expect(html).toContain('<h1 id="hello">Hello</h1>');
        expect(html).toContain('<div class="md-table-wrap"><table>');
        expect(html).toContain('class="md-task md-task-checked"');
        expect(html).toContain('class="md-task"');
        expect(html).toContain('<pre class="md-pre">');
        expect(html).toContain('<span class="token');
    });

    it('marks only blocks that contain changed lines', () => {
        const blocks = renderMarkdown('# A\n\ntext\n\n## B\n', {
            ...OPTIONS,
            changedLines: new Set([3]),
        });
        expect(blocks.map((block) => block.changed)).toEqual([false, true, false]);
    });

    it('resolves relative images and links against the file directory', () => {
        const html = render('![alt](../assets/a.png)\n\n[doc](./other.md#top)\n');
        expect(html).toContain('/api/file/ws/repo/abc123/docs/assets/a.png');
        expect(html).toContain('https://bitbucket.org/ws/repo/src/abc123/docs/guide/other.md#top');
    });

    it('keeps absolute https images and anchors working', () => {
        const html = render('![x](https://example.com/x.png)\n\n[jump](#hello)\n');
        expect(html).toContain('src="https://example.com/x.png"');
        expect(html).toContain('href="#hello"');
        expect(html).not.toContain('target="_blank"');
    });

    it('sanitizes raw HTML and javascript: links', () => {
        const html = render(
            '<script>alert(1)</script>\n\n<img src="x" onerror="alert(1)">\n\n[x](javascript:alert(1))\n',
        ).toLowerCase();
        expect(html).not.toContain('<script');
        expect(html).not.toContain('onerror');
        expect(html).not.toContain('javascript:');
        expect(html).toContain('x');
    });

    it('renders mermaid and math as plain code/text', () => {
        const html = render('\`\`\`mermaid\ngraph TD; A-->B;\n\`\`\`\n\nInline $x^2$ math.\n');
        expect(html).toContain('graph TD; A--&gt;B;');
        expect(html).toContain('$x^2$');
    });

    it('gives duplicate headings unique ids', () => {
        const html = render('# Same\n\n## Same\n');
        expect(html).toContain('id="same"');
        expect(html).toContain('id="same-2"');
    });
});
