import {Link, Route, Routes} from 'react-router-dom';
import {Inbox} from './components/Inbox';
import {PRView} from './components/PRView';
import {HelpPage} from './components/HelpPage';
import {CommandButton, CommandHostProvider} from './components/CommandHost';

export function App() {
    return (
        <CommandHostProvider>
            <Routes>
                <Route path="/" element={<Inbox />} />
                <Route path="/pr/:ws/:repo/:id" element={<PRView />} />
                <Route path="/help" element={<HelpPage />} />
                <Route path="*" element={<NotFound />} />
            </Routes>
        </CommandHostProvider>
    );
}

function NotFound() {
    return (
        <div className="page">
            <header className="topbar">
                <Link className="brand" to="/">
                    bb-review
                </Link>
                <span className="topbar-spacer" />
                <CommandButton />
            </header>
            <div className="state state-empty">
                <p className="state-title">Page not found</p>
                <Link className="btn" to="/">
                    Back to inbox
                </Link>
            </div>
        </div>
    );
}
