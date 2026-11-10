import {describe, expect, it, vi} from 'vitest';
import {render, screen} from '@testing-library/react';
import {FileDiffBoundary} from './FileDiffBoundary';

const URL = 'https://bitbucket.org/ws/repo/pull-requests/1';

function Boom(): JSX.Element {
    throw new Error('bad diff');
}

describe('FileDiffBoundary', () => {
    it('renders its children when the diff is fine', () => {
        render(
            <FileDiffBoundary path="src/good.ts" url={URL}>
                <p>diff content</p>
            </FileDiffBoundary>,
        );
        expect(screen.getByText('diff content')).toBeInTheDocument();
    });

    it('contains a file render error and links to Bitbucket', () => {
        const spy = vi.spyOn(console, 'error').mockImplementation(() => undefined);
        try {
            render(
                <FileDiffBoundary path="src/bad.ts" url={URL}>
                    <Boom />
                </FileDiffBoundary>,
            );

            const alert = screen.getByRole('alert');
            expect(alert).toHaveTextContent('This file failed to render');
            expect(alert).toHaveTextContent('src/bad.ts');

            const link = screen.getByRole('link', {name: /Open on Bitbucket/});
            expect(link).toHaveAttribute('href', URL);
            expect(link).toHaveAttribute('target', '_blank');
            expect(screen.getByRole('button', {name: 'Try again'})).toBeInTheDocument();
        } finally {
            spy.mockRestore();
        }
    });
});
