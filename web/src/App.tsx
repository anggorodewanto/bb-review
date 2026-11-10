import {Link, Route, Routes} from 'react-router-dom';
import {Inbox} from './components/Inbox';
import {PRView} from './components/PRView';

export function App() {
    return (
        <Routes>
            <Route path="/" element={<Inbox />} />
            <Route path="/pr/:ws/:repo/:id" element={<PRView />} />
            <Route path="*" element={<NotFound />} />
        </Routes>
    );
}

function NotFound() {
    return (
        <div className="page">
            <div className="state state-empty">
                <p className="state-title">Page not found</p>
                <Link className="btn" to="/">
                    Back to inbox
                </Link>
            </div>
        </div>
    );
}
