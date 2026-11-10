import {useEffect, useRef} from 'react';
import {readScrollPosition, writeScrollPosition} from './scrollMemory';

const SAVE_DEBOUNCE_MS = 150;

/**
 * Restore the window scroll position for `key` once the page is ready, and
 * save it while the user scrolls / when the page unmounts.
 */
export function useScrollMemory(key: string, ready = true): void {
    const restored = useRef(false);

    useEffect(() => {
        restored.current = false;
    }, [key]);

    useEffect(() => {
        if (!ready || restored.current) {
            return;
        }
        const saved = readScrollPosition(key);
        if (saved === null || saved <= 0) {
            restored.current = true;
            return;
        }
        restored.current = true;
        const frame = window.requestAnimationFrame(() => {
            window.scrollTo({top: saved, behavior: 'auto'});
        });
        return () => window.cancelAnimationFrame(frame);
    }, [key, ready]);

    useEffect(() => {
        let timer: number | null = null;
        function onScroll() {
            if (timer !== null) {
                return;
            }
            timer = window.setTimeout(() => {
                timer = null;
                writeScrollPosition(key, window.scrollY);
            }, SAVE_DEBOUNCE_MS);
        }

        window.addEventListener('scroll', onScroll, {passive: true});
        return () => {
            window.removeEventListener('scroll', onScroll);
            if (timer !== null) {
                window.clearTimeout(timer);
            }
            writeScrollPosition(key, window.scrollY);
        };
    }, [key]);
}
