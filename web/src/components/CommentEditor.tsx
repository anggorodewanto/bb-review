import {useState} from 'react';
import {Spinner} from './ui';

export type CommentMode = 'draft' | 'now';

type Props = {
    initialValue?: string;
    primaryLabel?: string;
    secondaryLabel?: string | null;
    placeholder?: string;
    autoFocus?: boolean;
    onSubmit: (raw: string, mode: CommentMode) => Promise<void>;
    onCancel: () => void;
};

export function CommentEditor({
    initialValue = '',
    primaryLabel = 'Add to review',
    secondaryLabel = 'Comment now',
    placeholder = 'Leave a comment…',
    autoFocus = true,
    onSubmit,
    onCancel,
}: Props) {
    const [value, setValue] = useState(initialValue);
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState<string | null>(null);

    async function submit(mode: CommentMode) {
        const raw = value.trim();
        if (!raw || busy) {
            return;
        }
        setBusy(true);
        setError(null);
        try {
            await onSubmit(raw, mode);
        } catch (err) {
            setError(err instanceof Error ? err.message : 'Could not save the comment');
            setBusy(false);
        }
    }

    function onKeyDown(event: React.KeyboardEvent<HTMLTextAreaElement>) {
        if (event.key === 'Escape') {
            event.stopPropagation();
            onCancel();
            return;
        }
        if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) {
            event.preventDefault();
            void submit('draft');
        }
    }

    return (
        <div className="comment-editor">
            <textarea
                value={value}
                onChange={(event) => setValue(event.target.value)}
                onKeyDown={onKeyDown}
                placeholder={placeholder}
                autoFocus={autoFocus}
                rows={4}
                disabled={busy}
            />
            {error ? <p className="form-error">{error}</p> : null}
            <div className="editor-actions">
                <button
                    type="button"
                    className="btn btn-primary"
                    onClick={() => void submit('draft')}
                    disabled={busy || !value.trim()}
                >
                    {busy ? <Spinner label="Saving" /> : null}
                    {primaryLabel}
                </button>
                {secondaryLabel ? (
                    <button
                        type="button"
                        className="btn"
                        onClick={() => void submit('now')}
                        disabled={busy || !value.trim()}
                    >
                        {secondaryLabel}
                    </button>
                ) : null}
                <span className="editor-hint">Ctrl+Enter to {primaryLabel.toLowerCase()}</span>
                <button type="button" className="btn btn-ghost" onClick={onCancel} disabled={busy}>
                    Cancel
                </button>
            </div>
        </div>
    );
}
