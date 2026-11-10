import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest';
import {
    FLASH_MS,
    RAPID_SCROLL_MS,
    TOAST_WINDOW_MS,
    highlightCommand,
    nextCommandToast,
    setCommandDetail,
    takeCommandDetail,
} from './commandFeedback';

const BASE = {
    commandId: 'next-file',
    title: 'Next file',
    keys: ['j'],
    isScroll: true,
};

describe('nextCommandToast', () => {
    it('starts a new toast for the first run', () => {
        const toast = nextCommandToast(null, {...BASE, now: 1000});
        expect(toast).toMatchObject({count: 1, animate: true, commandId: 'next-file'});
    });

    it('coalesces rapid repeats of the same command', () => {
        const first = nextCommandToast(null, {...BASE, now: 1000});
        const second = nextCommandToast(first, {...BASE, now: 1080});
        const third = nextCommandToast(second, {...BASE, now: 1160});
        expect(third.count).toBe(3);
        expect(third.commandId).toBe('next-file');
        expect(third.lastRunAt).toBe(1160);
    });

    it('does not re-animate rapid repeated scrolling', () => {
        const first = nextCommandToast(null, {...BASE, now: 1000});
        const rapid = nextCommandToast(first, {...BASE, now: 1000 + RAPID_SCROLL_MS - 10});
        expect(rapid.count).toBe(2);
        expect(rapid.animate).toBe(false);

        const slower = nextCommandToast(first, {...BASE, now: 1000 + RAPID_SCROLL_MS + 10});
        expect(slower.animate).toBe(true);
    });

    it('animates non-scrolling repeats', () => {
        const first = nextCommandToast(null, {commandId: 'approve', title: 'Toggle approve', keys: ['a'], isScroll: false, now: 1000});
        const second = nextCommandToast(first, {
            commandId: 'approve',
            title: 'Toggle approve',
            keys: ['a'],
            isScroll: false,
            now: 1050,
        });
        expect(second.count).toBe(2);
        expect(second.animate).toBe(true);
    });

    it('starts over after the coalesce window', () => {
        const first = nextCommandToast(null, {...BASE, now: 1000});
        const later = nextCommandToast(first, {...BASE, now: 1000 + TOAST_WINDOW_MS + 1});
        expect(later.count).toBe(1);
    });

    it('starts over for a different command', () => {
        const first = nextCommandToast(null, {...BASE, now: 1000});
        const other = nextCommandToast(first, {...BASE, commandId: 'prev-file', title: 'Previous file', now: 1050});
        expect(other.count).toBe(1);
        expect(other.commandId).toBe('prev-file');
    });

    it('carries the optional detail text', () => {
        const toast = nextCommandToast(null, {...BASE, detail: '2/12', now: 1000});
        expect(toast.detail).toBe('2/12');
    });
});

describe('command detail handoff', () => {
    afterEach(() => {
        takeCommandDetail();
    });

    it('returns and clears the pending detail', () => {
        setCommandDetail('3/7');
        expect(takeCommandDetail()).toBe('3/7');
        expect(takeCommandDetail()).toBeNull();
    });
});

describe('highlightCommand', () => {
    beforeEach(() => {
        vi.useFakeTimers();
        document.body.innerHTML = '';
    });

    afterEach(() => {
        vi.useRealTimers();
    });

    it('flags every matching control and clears after the flash window', () => {
        const button = document.createElement('button');
        button.setAttribute('data-command', 'approve');
        const other = document.createElement('button');
        other.setAttribute('data-command', 'submit');
        document.body.append(button, other);

        const count = highlightCommand('approve');
        expect(count).toBe(1);
        expect(button.hasAttribute('data-command-flash')).toBe(true);
        expect(other.hasAttribute('data-command-flash')).toBe(false);

        vi.advanceTimersByTime(FLASH_MS + 10);
        expect(button.hasAttribute('data-command-flash')).toBe(false);
    });

    it('does nothing when no control is mounted', () => {
        expect(highlightCommand('approve')).toBe(0);
    });
});
