import {describe, expect, it} from 'vitest';
import {fireEvent, render, screen} from '@testing-library/react';
import {FileDiff} from './FileDiff';
import {buildFileEntries} from '../lib/diff';
import type {ThreadActions} from './CommentThread';

const DIFF = `diff --git a/notes.txt b/notes.txt
index 1111111..2222222 100644
--- a/notes.txt
+++ b/notes.txt
@@ -2,2 +2,2 @@
-b
+B
 c
@@ -50,2 +50,2 @@
-y
+Y
 z
`;

const SOURCE = Array.from({length: 80}, (_value, index) => `line${index + 1}`).join('\n');

const ACTIONS: ThreadActions = {
    onReply: async () => undefined,
    onUpdateDraft: async () => undefined,
    onDeleteDraft: async () => undefined,
};

function renderDiff(overrides: {focusedKey?: string | null; expand?: {nonce: number; all: boolean} | null} = {}) {
    const entry = buildFileEntries(DIFF)[0]!;
    return render(
        <FileDiff
            entry={entry}
            index={0}
            viewType="unified"
            wrapLines
            focusedKey={overrides.focusedKey ?? null}
            comments={[]}
            drafts={[]}
            draftReplies={new Map()}
            actions={ACTIONS}
            editorKey={null}
            onEditorSubmit={async () => undefined}
            onEditorCancel={() => undefined}
            viewed="unviewed"
            onToggleViewed={() => undefined}
            onLineClick={() => undefined}
            onLoadSources={async () => ({old: SOURCE, new: SOURCE})}
            registerRef={() => undefined}
            reveal={null}
            expand={overrides.expand ?? null}
        />,
    );
}

describe('FileDiff expand controls', () => {
    it('renders top, middle and bottom gap controls with line ranges', async () => {
        renderDiff();

        expect(await screen.findByRole('button', {name: '↑ 1–1'})).toHaveAttribute(
            'data-command',
            'expand-context',
        );
        expect(screen.getByRole('button', {name: '↓ 4–23'})).toHaveAttribute(
            'data-command',
            'expand-context',
        );
        expect(screen.getByRole('button', {name: '↑ 30–49'})).toHaveAttribute(
            'data-command',
            'expand-context',
        );
        expect(screen.getByRole('button', {name: '↕ Expand all 46 lines'})).toHaveAttribute(
            'data-command',
            'expand-context-all',
        );
        // The bottom gap fetches the file length on first click, then shows ranges.
        const bottom = screen.getByRole('button', {name: '↓ Expand below'});
        expect(bottom).toHaveAttribute('data-command', 'expand-context');
        fireEvent.click(bottom);
        expect(await screen.findByRole('button', {name: '↓ 72–80'})).toBeInTheDocument();
    });

    it('expands down from a focused last line via the keyboard request', async () => {
        renderDiff({focusedKey: 'N3', expand: {nonce: 1, all: false}});

        // The 46-line gap shrinks by one 20-line chunk after `e`.
        expect(await screen.findByRole('button', {name: '↓ 24–43'})).toBeInTheDocument();
        expect(screen.getByRole('button', {name: '↑ 44–49'})).toBeInTheDocument();
        expect(screen.queryByRole('button', {name: '↓ 4–23'})).not.toBeInTheDocument();
    });
});
