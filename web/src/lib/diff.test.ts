import {describe, expect, it} from 'vitest';
import {buildFileEntries, splitDiffChunks} from './diff';

const DIFF = `diff --git a/src/a.ts b/src/a.ts
index 1111111..2222222 100644
--- a/src/a.ts
+++ b/src/a.ts
@@ -1,2 +1,3 @@
 keep
+added
-removed
diff --git a/old.txt b/old.txt
deleted file mode 100644
index 3333333..0000000
--- a/old.txt
+++ /dev/null
@@ -1,2 +0,0 @@
-a
-b
diff --git a/a.go b/b.go
similarity index 100%
rename from a.go
rename to b.go
diff --git a/logo.png b/logo.png
new file mode 100644
Binary files /dev/null and b/logo.png differ
`;

describe('splitDiffChunks', () => {
    it('splits on diff --git boundaries', () => {
        const chunks = splitDiffChunks(DIFF);
        expect(chunks).toHaveLength(4);
        expect(chunks[0]?.startsWith('diff --git a/src/a.ts')).toBe(true);
    });

    it('returns nothing for an empty diff', () => {
        expect(splitDiffChunks('')).toEqual([]);
    });
});

describe('buildFileEntries', () => {
    const entries = buildFileEntries(DIFF);

    it('parses every file', () => {
        expect(entries).toHaveLength(4);
    });

    it('uses the new path, or the old path for deleted files', () => {
        expect(entries.map((entry) => entry.path)).toEqual([
            'src/a.ts',
            'old.txt',
            'b.go',
            'logo.png',
        ]);
        expect(entries[0]?.type).toBe('modify');
        expect(entries[1]?.type).toBe('delete');
        expect(entries[2]?.type).toBe('rename');
        expect(entries[2]?.oldPath).toBe('a.go');
    });

    it('counts added and removed lines', () => {
        expect(entries[0]?.additions).toBe(1);
        expect(entries[0]?.deletions).toBe(1);
        expect(entries[0]?.changedLines).toBe(2);
        expect(entries[1]?.additions).toBe(0);
        expect(entries[1]?.deletions).toBe(2);
    });

    it('hashes each file separately', () => {
        expect(entries[0]?.hash).not.toBe(entries[1]?.hash);
        expect(entries[0]?.hash).toBe(buildFileEntries(DIFF)[0]?.hash);
    });

    it('labels binary files', () => {
        expect(entries[3]?.isBinary).toBe(true);
        expect(entries[3]?.hunks).toHaveLength(0);
    });

    it('returns an empty list for an empty diff', () => {
        expect(buildFileEntries('')).toEqual([]);
    });
});
