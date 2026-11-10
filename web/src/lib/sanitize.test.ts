import {describe, expect, it} from 'vitest';
import {sanitizeHtml} from './sanitize';

describe('sanitizeHtml', () => {
    it('keeps ordinary markup', () => {
        expect(sanitizeHtml('<p>Hello <strong>world</strong></p>')).toBe(
            '<p>Hello <strong>world</strong></p>',
        );
    });

    it('removes script tags and their content', () => {
        const safe = sanitizeHtml('<p>hi</p><script>alert(1)</script>');
        expect(safe).not.toContain('script');
        expect(safe).not.toContain('alert');
        expect(safe).toContain('<p>hi</p>');
    });

    it('removes event handler attributes', () => {
        const safe = sanitizeHtml('<p onclick="steal()" onmouseover="x()">text</p>');
        expect(safe).not.toContain('onclick');
        expect(safe).not.toContain('onmouseover');
    });

    it('strips javascript: urls', () => {
        const safe = sanitizeHtml('<a href="javascript:alert(1)">click</a>');
        expect(safe).not.toContain('javascript:');
        expect(safe).toContain('click');
    });

    it('strips obfuscated javascript: urls', () => {
        const safe = sanitizeHtml('<a href="  JaVaScRiPt:alert(1)">click</a>');
        expect(safe.toLowerCase()).not.toContain('javascript:');
    });

    it('hardens external links', () => {
        const safe = sanitizeHtml('<a href="https://bitbucket.org/x">link</a>');
        expect(safe).toContain('target="_blank"');
        expect(safe).toContain('rel="noopener noreferrer nofollow"');
    });

    it('removes iframes and embedded objects', () => {
        const safe = sanitizeHtml('<iframe src="https://evil"></iframe><object data="x"></object><p>ok</p>');
        expect(safe).not.toContain('iframe');
        expect(safe).not.toContain('object');
        expect(safe).toContain('ok');
    });

    it('allows data: images but not data: documents', () => {
        const image = sanitizeHtml('<img src="data:image/png;base64,iVBORw0KGgo=" alt="">');
        expect(image).toContain('data:image/png');
        const doc = sanitizeHtml('<a href="data:text/html,<script>alert(1)</script>">x</a>');
        expect(doc).not.toContain('data:text/html');
    });

    it('returns empty string for empty input', () => {
        expect(sanitizeHtml('')).toBe('');
        expect(sanitizeHtml('   ')).toBe('');
    });

    it('blocks encoded and attribute-based vectors', () => {
        const cases = [
            '<img src=x onerror=alert(1)>',
            '<a href="java&#x09;script:alert(1)">x</a>',
            '<a href="&#106;avascript:alert(1)">x</a>',
            '<svg><a xlink:href="javascript:alert(1)"><text>x</text></a></svg>',
            '<details open ontoggle=alert(1)>x</details>',
            '<p style="background:url(javascript:alert(1))">x</p>',
            '<img src="data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=">',
        ];
        for (const input of cases) {
            const safe = sanitizeHtml(input).toLowerCase();
            expect(safe).not.toMatch(/javascript:|onerror|ontoggle|svg|style=/);
        }
    });
});
