import {useEffect} from 'react';
import {KEYMAP_GROUPS, formatKeys} from '../lib/keymap';

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
                    {KEYMAP_GROUPS.map(({group, bindings}) => (
                        <section key={group} className="help-group">
                            <h3>{group}</h3>
                            <dl>
                                {bindings.map((binding) => (
                                    <div key={`${binding.action}-${binding.keys.join('')}`} className="help-row">
                                        <dt>
                                            <kbd>{formatKeys(binding.keys)}</kbd>
                                        </dt>
                                        <dd>{binding.label}</dd>
                                    </div>
                                ))}
                            </dl>
                        </section>
                    ))}
                </div>
                <p className="help-foot">
                    Shortcuts are ignored while typing in a text field. Ctrl+Enter adds a draft from the
                    comment editor.
                </p>
            </div>
        </div>
    );
}
