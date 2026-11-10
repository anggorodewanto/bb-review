// Tiny subsequence matcher for the command palette and help search.

export type FuzzyRange = {start: number; end: number};

export type FuzzyMatch = {
    score: number;
    /** Matched character ranges (end exclusive), merged when contiguous. */
    ranges: FuzzyRange[];
};

function isBoundary(text: string, index: number): boolean {
    if (index === 0) {
        return true;
    }
    const previous = text[index - 1] ?? '';
    return /[\s\-_/.:]/.test(previous);
}

function toRanges(positions: number[]): FuzzyRange[] {
    const ranges: FuzzyRange[] = [];
    for (const position of positions) {
        const last = ranges[ranges.length - 1];
        if (last && last.end === position) {
            last.end = position + 1;
            continue;
        }
        ranges.push({start: position, end: position + 1});
    }
    return ranges;
}

/**
 * Case-insensitive subsequence match with small word-boundary and
 * contiguity bonuses. Returns null when the query does not match.
 */
export function fuzzyMatch(query: string, text: string): FuzzyMatch | null {
    const needle = query.trim().toLowerCase();
    if (needle.length === 0) {
        return {score: 0, ranges: []};
    }
    const haystack = text.toLowerCase();
    const positions: number[] = [];
    let from = 0;
    for (const char of needle) {
        const index = haystack.indexOf(char, from);
        if (index < 0) {
            return null;
        }
        positions.push(index);
        from = index + 1;
    }

    let score = 0;
    positions.forEach((position, i) => {
        score += 1;
        if (isBoundary(haystack, position)) {
            score += 4;
        }
        const previous = positions[i - 1];
        if (previous === undefined) {
            score += Math.max(0, 8 - position);
            return;
        }
        if (position === previous + 1) {
            score += 4;
            return;
        }
        score -= Math.min(4, position - previous - 1);
    });

    return {score, ranges: toRanges(positions)};
}

export type Segment = {text: string; match: boolean};

/** Split text into plain and highlighted segments for rendering. */
export function highlightSegments(text: string, ranges: FuzzyRange[]): Segment[] {
    const segments: Segment[] = [];
    let index = 0;
    for (const range of ranges) {
        if (range.start > index) {
            segments.push({text: text.slice(index, range.start), match: false});
        }
        segments.push({text: text.slice(range.start, range.end), match: true});
        index = range.end;
    }
    if (index < text.length) {
        segments.push({text: text.slice(index), match: false});
    }
    return segments;
}
