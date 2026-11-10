import {useEffect, useState} from 'react';
import type {RefObject} from 'react';
import {scrollContainerFor, shouldShowBackToTop} from '../lib/scroll';

export type BackToTopProps = {
    onClick: () => void;
    /** Page root; when it lives in a scroll container, that container is watched. */
    scrollRoot?: RefObject<HTMLElement>;
};

export function BackToTop({onClick, scrollRoot}: BackToTopProps) {
    const [visible, setVisible] = useState(false);

    useEffect(() => {
        const container = scrollContainerFor(scrollRoot?.current ?? null);
        const read = () => {
            if (container) {
                setVisible(shouldShowBackToTop(container.scrollTop, container.clientHeight));
                return;
            }
            const scrollTop = window.scrollY || document.documentElement.scrollTop || 0;
            const height = window.innerHeight || document.documentElement.clientHeight || 0;
            setVisible(shouldShowBackToTop(scrollTop, height));
        };

        read();
        const target: HTMLElement | Window = container ?? window;
        target.addEventListener('scroll', read, {passive: true});
        window.addEventListener('resize', read);
        return () => {
            target.removeEventListener('scroll', read);
            window.removeEventListener('resize', read);
        };
    }, [scrollRoot]);

    if (!visible) {
        return null;
    }

    return (
        <button
            type="button"
            className="float-btn back-to-top"
            aria-label="Back to top"
            title="Back to top"
            onClick={onClick}
        >
            ↑
        </button>
    );
}
