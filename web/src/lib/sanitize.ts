// Sanitizes Bitbucket-rendered HTML (PR descriptions, comments) before it is
// rendered with dangerouslySetInnerHTML. This content is written by other
// people, so we use DOMPurify's allowlist instead of a hand-rolled blocklist.
import DOMPurify from 'dompurify';

const FORBID_TAGS = ['style', 'form', 'input', 'button', 'textarea', 'select', 'svg', 'math'];

// data: URIs are only allowed for inline raster images.
const SAFE_URI = /^(?:(?:https?|mailto):|[^a-z]|[a-z+.-]+(?:[^a-z+.\-:]|$))/i;
const SAFE_DATA_IMAGE = /^data:image\/(?:png|jpe?g|gif|webp);/i;

let hooked = false;

function installHooks(): void {
    if (hooked) {
        return;
    }
    hooked = true;
    DOMPurify.addHook('afterSanitizeAttributes', (node) => {
        if (node.tagName === 'IMG' && /^\s*data:/i.test(node.getAttribute('src') ?? '') && !SAFE_DATA_IMAGE.test(node.getAttribute('src') ?? '')) {
            node.removeAttribute('src');
        }
        if (node.tagName !== 'A' || !node.getAttribute('href')) {
            return;
        }
        node.setAttribute('target', '_blank');
        node.setAttribute('rel', 'noopener noreferrer nofollow');
    });
}

/** Sanitize Bitbucket HTML before rendering with dangerouslySetInnerHTML. */
export function sanitizeHtml(html: string): string {
    const trimmed = html.trim();
    if (!trimmed) {
        return '';
    }
    if (!DOMPurify.isSupported) {
        // No DOM available: render nothing rather than risk raw markup.
        return '';
    }
    installHooks();
    return DOMPurify.sanitize(trimmed, {
        FORBID_TAGS,
        FORBID_ATTR: ['style'],
        ALLOWED_URI_REGEXP: SAFE_URI,
        ADD_DATA_URI_TAGS: ['img'],
        ADD_ATTR: ['target'],
    });
}
