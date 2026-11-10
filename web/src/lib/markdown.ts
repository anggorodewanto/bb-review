// Markdown rendering for PR files. Pure helpers live here so the block
// geometry and link/path resolution can be tested without React.
import {Renderer, marked} from 'marked';
import type {Token, Tokens} from 'marked';
import type {HunkData} from 'react-diff-view';
import {refractor} from 'refractor/lib/common.js';
import {sanitizeHtml} from './sanitize';

export const MAX_MARKDOWN_BYTES = 300 * 1024;

export function isMarkdownPath(path: string): boolean {
    return /\.(?:md|markdown|mdx)$/i.test(path);
}

export type BlockSpan = {startLine: number; endLine: number};

export type MarkdownBlock = BlockSpan & {
    key: string;
    html: string;
    changed: boolean;
};

export type MarkdownRenderOptions = {
    /** Path of the rendered file (used to resolve relative links/images). */
    filePath: string;
    ws: string;
    repo: string;
    commit: string;
    /** New-side (or old-side) line numbers that were added in the diff. */
    changedLines: ReadonlySet<number>;
};

export type ResolvedHref =
    | {kind: 'anchor'; href: string}
    | {kind: 'external'; href: string}
    | {kind: 'repo'; path: string; hash: string}
    | {kind: 'unsafe'; href: string};

function countNewlines(text: string): number {
    let count = 0;
    for (const char of text) {
        if (char === '\n') {
            count += 1;
        }
    }
    return count;
}

/**
 * Normalize a markdown-relative path against the file's directory.
 * Supports `./`, `../`, bare relative paths and root-relative `/` paths.
 */
export function resolveRepoPath(fromPath: string, target: string): string | null {
    const clean = target.split('?')[0]?.split('#')[0] ?? '';
    if (clean.length === 0) {
        return null;
    }
    const absolute = clean.startsWith('/');
    const base = absolute ? [] : fromPath.split('/').slice(0, -1);
    const segments = absolute ? clean.slice(1).split('/') : clean.split('/');
    const output = [...base];
    for (const segment of segments) {
        if (segment === '' || segment === '.') {
            continue;
        }
        if (segment === '..') {
            if (output.length === 0) {
                return null;
            }
            output.pop();
            continue;
        }
        output.push(segment);
    }
    return output.length > 0 ? output.join('/') : null;
}

/** Classify a markdown href for the renderer (anchors, repo files, external). */
export function resolveHref(fromPath: string, rawHref: string | null | undefined): ResolvedHref {
    const href = (rawHref ?? '').trim();
    if (href.length === 0) {
        return {kind: 'unsafe', href};
    }
    if (href.startsWith('#')) {
        return {kind: 'anchor', href};
    }
    if (/^(?:https?:|mailto:)/i.test(href) || href.startsWith('//')) {
        return {kind: 'external', href};
    }
    if (/^[a-z][a-z0-9+.-]*:/i.test(href)) {
        // data:, javascript:, vbscript:, tel: ... not rendered as links.
        return {kind: 'unsafe', href};
    }
    const hashAt = href.indexOf('#');
    const hash = hashAt >= 0 ? href.slice(hashAt + 1) : '';
    const path = resolveRepoPath(fromPath, href);
    if (!path) {
        return {kind: 'unsafe', href};
    }
    return {kind: 'repo', path, hash};
}

export function encodeRepoPath(path: string): string {
    return path
        .split('/')
        .filter((segment) => segment.length > 0)
        .map((segment) => encodeURIComponent(segment))
        .join('/');
}

export function sourceFileUrl(ws: string, repo: string, commit: string, path: string): string {
    return `https://bitbucket.org/${encodeURIComponent(ws)}/${encodeURIComponent(repo)}/src/${encodeURIComponent(commit)}/${encodeRepoPath(path)}`;
}

export function apiFileUrl(ws: string, repo: string, commit: string, path: string): string {
    return `/api/file/${encodeURIComponent(ws)}/${encodeURIComponent(repo)}/${encodeURIComponent(commit)}/${encodeRepoPath(path)}`;
}

function escapeHtml(text: string): string {
    return text
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;');
}

function escapeAttr(text: string): string {
    return escapeHtml(text).replace(/'/g, '&#39;');
}

type HastNode = {
    type?: string;
    value?: string;
    tagName?: string;
    properties?: Record<string, unknown>;
    children?: HastNode[];
};

/** Minimal hast → HTML serializer for refractor's highlighted output. */
function hastToHtml(node: HastNode | undefined): string {
    if (!node) {
        return '';
    }
    if (node.type === 'text') {
        return escapeHtml(node.value ?? '');
    }
    const children = (node.children ?? []).map(hastToHtml).join('');
    if (node.type !== 'element' || !node.tagName) {
        return children;
    }
    const className = node.properties?.className;
    const classAttr = Array.isArray(className)
        ? className.join(' ')
        : typeof className === 'string'
          ? className
          : '';
    const attrs = classAttr.length > 0 ? ` class="${escapeAttr(classAttr)}"` : '';
    return `<${node.tagName}${attrs}>${children}</${node.tagName}>`;
}

type LocatedToken = {token: Token; startLine: number; endLine: number};

/**
 * Find each top-level lexer token in the source and translate its raw text to
 * 1-based line spans. `raw` is searched sequentially, so repeated text still
 * maps to the right occurrence.
 */
function locateTokens(source: string): LocatedToken[] {
    const tokens = marked.lexer(source, {gfm: true});
    let cursor = 0;
    return tokens.map((token) => {
        const raw = token.raw ?? '';
        let startOffset = cursor;
        if (raw.length > 0) {
            const found = source.indexOf(raw, cursor);
            if (found >= 0) {
                startOffset = found;
                cursor = found + raw.length;
            }
        }
        const startLine = countNewlines(source.slice(0, startOffset)) + 1;
        const trimmed = raw.replace(/[\r\n]+$/, '');
        const endLine = startLine + countNewlines(trimmed);
        return {token, startLine, endLine};
    });
}

/** Line span of every renderable top-level Markdown block. */
export function markdownBlockSpans(source: string): BlockSpan[] {
    return locateTokens(source)
        .filter(({token}) => token.type !== 'space' && token.type !== 'def')
        .map(({startLine, endLine}) => ({startLine, endLine}));
}

/** Which blocks contain at least one added line (1-based new/old side). */
export function markChangedBlocks(
    spans: ReadonlyArray<BlockSpan>,
    changedLines: ReadonlySet<number>,
): boolean[] {
    const sorted = [...changedLines].filter((line) => Number.isFinite(line));
    return spans.map(({startLine, endLine}) =>
        sorted.some((line) => line >= startLine && line <= endLine),
    );
}

/** Changed line numbers for one side of a file's diff. */
export function changedLineNumbers(hunks: HunkData[], side: 'new' | 'old'): Set<number> {
    const lines = new Set<number>();
    for (const hunk of hunks) {
        for (const change of hunk.changes) {
            if (change.type === 'insert') {
                if (side === 'new') {
                    lines.add(change.lineNumber);
                }
                continue;
            }
            if (change.type === 'delete' && side === 'old') {
                lines.add(change.lineNumber);
            }
        }
    }
    return lines;
}

function slugify(html: string): string {
    const text = html
        .replace(/<[^>]*>/g, '')
        .replace(/&[a-z#0-9]+;/gi, ' ')
        .toLowerCase();
    const slug = text
        .replace(/[^\p{L}\p{N}\s-]/gu, '')
        .trim()
        .replace(/\s+/g, '-');
    return slug.length > 0 ? slug : 'section';
}

function replaceTaskCheckboxes(html: string): string {
    return html.replace(/<input\b[^>]*>/gi, (tag) => {
        if (!/type="checkbox"/i.test(tag)) {
            return tag;
        }
        return /\bchecked\b/i.test(tag)
            ? '<span class="md-task md-task-checked" aria-hidden="true">✓</span>'
            : '<span class="md-task" aria-hidden="true">☐</span>';
    });
}

function decorate(html: string): string {
    const safe = sanitizeHtml(replaceTaskCheckboxes(html));
    return safe
        .replace(/<table\b[^>]*>/gi, (tag) => `<div class="md-table-wrap">${tag}`)
        .replace(/<\/table>/gi, '</table></div>');
}

function createRenderer(options: MarkdownRenderOptions): Renderer {
    const renderer = new Renderer();
    const slugs = new Map<string, number>();

    renderer.heading = function (this: Renderer, token: Tokens.Heading): string {
        const body = this.parser.parseInline(token.tokens);
        const base = slugify(body);
        const seen = slugs.get(base) ?? 0;
        slugs.set(base, seen + 1);
        const id = seen === 0 ? base : `${base}-${seen + 1}`;
        return `<h${token.depth} id="${escapeAttr(id)}">${body}</h${token.depth}>\n`;
    };

    renderer.code = function (token: Tokens.Code): string {
        const language = (token.lang ?? '').trim().split(/\s+/)[0] ?? '';
        if (language && refractor.registered(language)) {
            const highlighted = hastToHtml(
                refractor.highlight(token.text, language) as unknown as HastNode,
            );
            return `<pre class="md-pre">${highlighted}</pre>\n`;
        }
        return `<pre class="md-pre"><code>${escapeHtml(token.text)}</code></pre>\n`;
    };

    renderer.link = function (this: Renderer, token: Tokens.Link): string {
        const label = this.parser.parseInline(token.tokens);
        const resolved = resolveHref(options.filePath, token.href);
        const title = token.title ? ` title="${escapeAttr(token.title)}"` : '';
        if (resolved.kind === 'unsafe') {
            return label;
        }
        if (resolved.kind === 'anchor') {
            return `<a href="${escapeAttr(resolved.href)}"${title}>${label}</a>`;
        }
        if (resolved.kind === 'external') {
            return `<a href="${escapeAttr(resolved.href)}"${title} target="_blank" rel="noopener noreferrer nofollow">${label}</a>`;
        }
        const url = `${sourceFileUrl(options.ws, options.repo, options.commit, resolved.path)}${
            resolved.hash ? `#${escapeAttr(resolved.hash)}` : ''
        }`;
        return `<a href="${escapeAttr(url)}"${title} target="_blank" rel="noopener noreferrer nofollow">${label}</a>`;
    };

    renderer.image = function (token: Tokens.Image): string {
        const resolved = resolveHref(options.filePath, token.href);
        const title = token.title ? ` title="${escapeAttr(token.title)}"` : '';
        const alt = escapeAttr(token.text ?? '');
        if (resolved.kind === 'repo') {
            const url = apiFileUrl(options.ws, options.repo, options.commit, resolved.path);
            return `<img src="${escapeAttr(url)}" alt="${alt}"${title} loading="lazy">`;
        }
        if (resolved.kind === 'external' && /^https?:\/\//i.test(resolved.href)) {
            return `<img src="${escapeAttr(resolved.href)}" alt="${alt}"${title} loading="lazy">`;
        }
        return escapeHtml(token.text ?? '');
    };

    return renderer;
}

/** Render Markdown into sanitized, per-block HTML with line spans. */
export function renderMarkdown(
    source: string,
    options: MarkdownRenderOptions,
): MarkdownBlock[] {
    const located = locateTokens(source);
    const renderer = createRenderer(options);
    const blocks: MarkdownBlock[] = [];
    let key = 0;
    for (const {token, startLine, endLine} of located) {
        if (token.type === 'space' || token.type === 'def') {
            continue;
        }
        const parsed = marked.parser([token], {renderer, gfm: true, async: false});
        const html = decorate(parsed);
        if (html.trim().length === 0) {
            continue;
        }
        blocks.push({
            key: `md-${key}`,
            startLine,
            endLine,
            html,
            changed: markChangedBlocks([{startLine, endLine}], options.changedLines)[0] ?? false,
        });
        key += 1;
    }
    return blocks;
}
