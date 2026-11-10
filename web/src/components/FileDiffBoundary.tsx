import {Component, type ErrorInfo, type ReactNode} from 'react';

type Props = {
    path: string;
    /** Bitbucket URL for the pull request (fallback link). */
    url: string;
    children: ReactNode;
};

type State = {error: Error | null};

/**
 * Keeps one badly-rendered file from blanking the whole PR view. Renders a
 * compact in-place fallback with a link back to Bitbucket instead.
 */
export class FileDiffBoundary extends Component<Props, State> {
    state: State = {error: null};

    static getDerivedStateFromError(error: Error): State {
        return {error};
    }

    componentDidCatch(error: Error, info: ErrorInfo): void {
        console.error('[bb-review] file render error', this.props.path, error, info.componentStack);
    }

    private reset = (): void => {
        this.setState({error: null});
    };

    render(): ReactNode {
        const {error} = this.state;
        if (!error) {
            return this.props.children;
        }
        return (
            <section className="file-block file-error" role="alert">
                <header className="file-block-head">
                    <div className="file-block-title">
                        <span className="tag tag-warn">Error</span>
                        <span className="file-block-path">{this.props.path}</span>
                    </div>
                </header>
                <p className="file-note">
                    This file failed to render — open on Bitbucket.
                </p>
                <div className="file-error-actions">
                    <a
                        className="btn btn-small"
                        href={this.props.url}
                        target="_blank"
                        rel="noopener noreferrer"
                    >
                        Open on Bitbucket ↗
                    </a>
                    <button type="button" className="btn btn-small btn-ghost" onClick={this.reset}>
                        Try again
                    </button>
                </div>
            </section>
        );
    }
}
