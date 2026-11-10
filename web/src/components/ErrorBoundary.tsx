import {Component, type ErrorInfo, type ReactNode} from 'react';

type Props = {children: ReactNode};
type State = {error: Error | null};

/** Shows a recoverable error screen instead of a blank page when rendering throws. */
export class ErrorBoundary extends Component<Props, State> {
    state: State = {error: null};

    static getDerivedStateFromError(error: Error): State {
        return {error};
    }

    componentDidCatch(error: Error, info: ErrorInfo): void {
        console.error('[bb-review] render error', error, info.componentStack);
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
            <div className="bbr-crash" role="alert">
                <h1>Something broke while rendering this page.</h1>
                <pre>{error.message}</pre>
                <div className="bbr-crash-actions">
                    <button type="button" onClick={this.reset}>
                        Try again
                    </button>
                    <button type="button" onClick={() => window.location.reload()}>
                        Reload
                    </button>
                    <a href="/">Inbox</a>
                </div>
            </div>
        );
    }
}
