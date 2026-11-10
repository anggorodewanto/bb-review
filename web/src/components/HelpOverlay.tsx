import {useEffect} from 'react';
import {Link} from 'react-router-dom';
import {allCommandGroups} from '../lib/commands';
import {formatShortcutList} from '../lib/keymap';

export function HelpOverlay({open, onClose}: {open: boolean; onClose: () => void}) {
    useEffect(() => {
        if (!open) {
            return;
        }
        function onKey(event: KeyboardEvent) {
            if (event.key === 'Escape') {
                event.preventDefault();
                onClose();
            }
        }
        window.addEventListener('keydown', onKey);
        return () => window.removeEventListener('keydown', onKey);
    }, [open, onClose]);

    if (!open) {
        return null;
    }

    return (
        <div className="overlay" onClick={onClose} role="presentation">
            <div
                className="help-dialog"
                role="dialog"
                aria-modal="true"
                aria-label="Keyboard shortcuts"
                onClick={(event) => event.stopPropagation()}
            >
                <div className="help-head">
                    <h2>Keyboard shortcuts</h2>
                    <button type="button" className="btn btn-small" onClick={onClose}>
                        Close
                    </button>
                </div>
                <div className="help-groups">
                    {allCommandGroups().map(({group, commands}) => (
                        <section key={group} className="help-group">
                            <h3>{group}</h3>
                            <dl>
                                {commands.map((command) => (
                                    <div key={command.id} className="help-row">
                                        <dt>
                                            {command.keys.length > 0 ? (
                                                <kbd>{formatShortcutList(command.keys)}</kbd>
                                            ) : (
                                                <span className="muted">—</span>
                                            )}
                                        </dt>
                                        <dd>{command.title}</dd>
                                    </div>
                                ))}
                            </dl>
                        </section>
                    ))}
                </div>
                <p className="help-foot">
                    Shortcuts are ignored while typing in a text field. Ctrl+Enter adds a draft from the
                    comment editor. <Link to="/help" onClick={onClose}>Open the full help page</Link>.
                </p>
            </div>
        </div>
    );
}
