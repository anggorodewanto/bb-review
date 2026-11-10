// Detection/normalization of bitbucket.org pull request URLs.

const PR_URL =
    /\bhttps?:\/\/(?:www\.)?bitbucket\.org\/([^/\s?#]+)\/([^/\s?#]+)\/pull-requests\/(\d+)/i;

/**
 * Pull the first bitbucket.org pull request URL out of pasted text and
 * normalize it to the canonical form (no leading `www.`, no trailing path or
 * query string). Returns null when the text does not contain such a URL.
 */
export function extractPRUrl(text: string): string | null {
    const match = text.match(PR_URL);
    if (!match) {
        return null;
    }
    const [, workspace, repo, id] = match;
    if (!workspace || !repo || !id) {
        return null;
    }
    return `https://bitbucket.org/${workspace}/${repo}/pull-requests/${id}`;
}
