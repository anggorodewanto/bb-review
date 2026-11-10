const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

/** Compact age: "now", "5m", "3h", "2d", "3w", "5mo", "2y". */
export function relativeAge(iso: string, now: number = Date.now()): string {
    const parsed = Date.parse(iso);
    if (Number.isNaN(parsed)) {
        return '';
    }
    const delta = Math.max(0, now - parsed);
    if (delta < MINUTE) return 'now';
    if (delta < HOUR) return `${Math.floor(delta / MINUTE)}m`;
    if (delta < DAY) return `${Math.floor(delta / HOUR)}h`;
    if (delta < 7 * DAY) return `${Math.floor(delta / DAY)}d`;
    if (delta < 30 * DAY) return `${Math.floor(delta / (7 * DAY))}w`;
    if (delta < 365 * DAY) return `${Math.floor(delta / (30 * DAY))}mo`;
    return `${Math.floor(delta / (365 * DAY))}y`;
}

/** Like relativeAge, but reads as prose and keeps seconds for fresh data. */
export function relativeAgeLong(iso: string, now: number = Date.now()): string {
    const parsed = Date.parse(iso);
    if (Number.isNaN(parsed)) {
        return '';
    }
    const delta = Math.max(0, now - parsed);
    if (delta < 5_000) return 'just now';
    if (delta < MINUTE) return `${Math.floor(delta / 1000)}s ago`;
    if (delta < HOUR) return `${Math.floor(delta / MINUTE)}m ago`;
    if (delta < DAY) return `${Math.floor(delta / HOUR)}h ago`;
    if (delta < 7 * DAY) return `${Math.floor(delta / DAY)}d ago`;
    if (delta < 30 * DAY) return `${Math.floor(delta / (7 * DAY))}w ago`;
    if (delta < 365 * DAY) return `${Math.floor(delta / (30 * DAY))}mo ago`;
    return `${Math.floor(delta / (365 * DAY))}y ago`;
}

export function isoTitle(iso: string): string {
    const parsed = Date.parse(iso);
    if (Number.isNaN(parsed)) {
        return iso;
    }
    return new Date(parsed).toLocaleString();
}

/** Case-insensitive subsequence match across every field (OR-ed fields). */
export function fuzzyMatch(query: string, fields: string[]): boolean {
    const needles = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
    if (needles.length === 0) {
        return true;
    }
    const haystacks = fields.map((field) => field.toLowerCase());
    return needles.every((needle) => haystacks.some((hay) => isSubsequence(needle, hay)));
}

function isSubsequence(needle: string, hay: string): boolean {
    let cursor = 0;
    for (const char of hay) {
        if (char === needle[cursor]) {
            cursor += 1;
            if (cursor === needle.length) {
                return true;
            }
        }
    }
    return cursor === needle.length;
}

const LANGUAGE_BY_EXTENSION: Record<string, string> = {
    bash: 'bash',
    c: 'c',
    cc: 'cpp',
    cfg: 'ini',
    cjs: 'javascript',
    cpp: 'cpp',
    cs: 'csharp',
    css: 'css',
    diff: 'diff',
    ejs: 'markup-templating',
    go: 'go',
    h: 'c',
    hpp: 'cpp',
    htm: 'markup',
    html: 'markup',
    ini: 'ini',
    java: 'java',
    js: 'javascript',
    json: 'json',
    jsx: 'javascript',
    kt: 'kotlin',
    kts: 'kotlin',
    less: 'less',
    lua: 'lua',
    m: 'objectivec',
    md: 'markdown',
    mjs: 'javascript',
    mk: 'makefile',
    php: 'php',
    pl: 'perl',
    py: 'python',
    r: 'r',
    rb: 'ruby',
    rs: 'rust',
    sass: 'sass',
    scss: 'scss',
    sh: 'bash',
    sql: 'sql',
    swift: 'swift',
    toml: 'ini',
    ts: 'typescript',
    tsx: 'typescript',
    vb: 'vbnet',
    xml: 'markup',
    yaml: 'yaml',
    yml: 'yaml',
    zsh: 'bash',
};

export function languageForPath(path: string): string {
    const name = path.split('/').pop() ?? path;
    if (name === 'Makefile' || name === 'makefile' || name === 'GNUmakefile') {
        return 'makefile';
    }
    if (name === 'Dockerfile') {
        return 'bash';
    }
    const ext = name.includes('.') ? name.split('.').pop()!.toLowerCase() : '';
    return LANGUAGE_BY_EXTENSION[ext] ?? '';
}

export function shortSha(sha: string): string {
    return sha.slice(0, 8);
}

export function initials(name: string): string {
    const parts = name.trim().split(/\s+/).filter(Boolean);
    if (parts.length === 0) {
        return '?';
    }
    const first = parts[0]!;
    if (parts.length === 1) {
        return first.slice(0, 2).toUpperCase();
    }
    return `${first[0]}${parts[parts.length - 1]![0]}`.toUpperCase();
}

export function plural(count: number, singular: string, pluralWord = `${singular}s`): string {
    return `${count} ${count === 1 ? singular : pluralWord}`;
}
