import {useState} from 'react';
import {describe, expect, it, vi} from 'vitest';
import {fireEvent, render, screen} from '@testing-library/react';
import {makeCommandContext} from '../lib/commands';
import type {CommandContext, DynamicCommand} from '../lib/commands';
import {CommandPalette} from './CommandPalette';

type HarnessProps = {
    ctx?: CommandContext;
    recents?: string[];
    dynamic?: DynamicCommand[];
    onRun?: (id: string) => void;
};

function Harness({
    ctx = makeCommandContext('pr', {hasFocus: true, hasFiles: true}),
    recents = [],
    dynamic = [],
    onRun = () => undefined,
}: HarnessProps) {
    const [open, setOpen] = useState(false);
    return (
        <div>
            <button type="button" onClick={() => setOpen(true)}>
                Open palette
            </button>
            <CommandPalette
                open={open}
                onClose={() => setOpen(false)}
                ctx={ctx}
                recents={recents}
                dynamic={dynamic}
                onRun={onRun}
                onOpenHelp={() => undefined}
            />
        </div>
    );
}

function openPalette() {
    const button = screen.getByRole('button', {name: 'Open palette'});
    button.focus();
    fireEvent.click(button);
    return screen.getByRole('combobox', {name: 'Search commands'});
}

describe('CommandPalette', () => {
    it('filters commands and runs the selected one with Enter', () => {
        const onRun = vi.fn();
        render(<Harness onRun={onRun} />);
        const input = openPalette();

        fireEvent.change(input, {target: {value: 'wrap'}});
        expect(screen.getByRole('option', {name: /Toggle line/})).toBeInTheDocument();

        fireEvent.keyDown(input, {key: 'Enter'});
        expect(onRun).toHaveBeenCalledWith('toggle-wrap');
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });

    it('moves the selection with the arrow keys', () => {
        const onRun = vi.fn();
        render(<Harness onRun={onRun} />);
        const input = openPalette();

        fireEvent.change(input, {target: {value: 'go to'}});
        fireEvent.keyDown(input, {key: 'ArrowDown'});
        fireEvent.keyDown(input, {key: 'Enter'});
        expect(onRun).toHaveBeenCalledWith('inbox');
    });

    it('closes on Escape and restores focus', () => {
        render(<Harness />);
        openPalette();

        fireEvent.keyDown(screen.getByRole('combobox', {name: 'Search commands'}), {key: 'Escape'});

        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'Open palette'})).toHaveFocus();
    });

    it('shows unavailable commands greyed in a final section', () => {
        const onRun = vi.fn();
        render(<Harness ctx={makeCommandContext('inbox')} onRun={onRun} />);
        const input = openPalette();

        fireEvent.change(input, {target: {value: 'next file'}});

        expect(screen.getByText('Not available here')).toBeInTheDocument();
        const option = screen.getByRole('option', {name: /Next file/});
        expect(option).toHaveAttribute('aria-disabled', 'true');

        fireEvent.keyDown(input, {key: 'Enter'});
        expect(onRun).not.toHaveBeenCalled();
    });

    it('lists recently used commands first when the query is empty', () => {
        render(<Harness recents={['toggle-wrap']} />);
        openPalette();
        expect(screen.getByText('Recently used')).toBeInTheDocument();
        expect(screen.getAllByText('Toggle line wrapping')[0]).toBeInTheDocument();
    });

    it('shows dynamic page entries in their own section and runs them', () => {
        const run = vi.fn();
        const dynamic: DynamicCommand[] = [
            {
                id: 'file-0',
                title: 'Go to file: src/a.ts',
                section: 'files',
                search: ['src/a.ts'],
                run,
            },
        ];
        render(<Harness dynamic={dynamic} />);
        const input = openPalette();

        fireEvent.change(input, {target: {value: 'a.ts'}});
        expect(screen.getAllByText('Files').length).toBeGreaterThan(0);
        fireEvent.click(screen.getByRole('option', {name: /Go to file/}));

        expect(run).toHaveBeenCalledTimes(1);
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });

    it('highlights the matched characters in the title', () => {
        render(<Harness />);
        const input = openPalette();
        fireEvent.change(input, {target: {value: 'wrap'}});
        const option = screen.getByRole('option', {name: /Toggle line/});
        expect(option.querySelector('.command-match')?.textContent).toBe('wrap');
    });
});
