import {useMemo, useRef, useState} from 'react';
import {Link} from 'react-router-dom';
import {allCommandGroups} from '../lib/commands';
import type {Command} from '../lib/commands';
import {fuzzyMatch} from '../lib/fuzzy';
import {formatShortcutList} from '../lib/keymap';
import {lastInboxUrl} from '../lib/inboxUrl';
import {scrollPageToTop} from '../lib/scroll';
import {usePageCommands} from './CommandHost';
import {CommandButton} from './CommandHost';

function commandMatches(command: Command, query: string): boolean {
    if (query.length === 0) {
        return true;
    }
    if (fuzzyMatch(query, command.title)) {
        return true;
    }
    if (fuzzyMatch(query, command.group)) {
        return true;
    }
    return command.keys.some((sequence) => fuzzyMatch(query, sequence.join(' ')) !== null);
}

export function HelpPage() {
    const pageRef = useRef<HTMLDivElement>(null);
    const [query, setQuery] = useState('');

    const handlers = useMemo(
        () => ({
            'scroll-top': () => scrollPageToTop(pageRef.current),
        }),
        [],
    );
    usePageCommands({view: 'help', handlers});

    const groups = useMemo(() => {
        const trimmed = query.trim();
        return allCommandGroups()
            .map((group) => ({
                ...group,
                commands: group.commands.filter((command) => commandMatches(command, trimmed)),
            }))
            .filter((group) => group.commands.length > 0);
    }, [query]);

    return (
        <div className="page help-page" ref={pageRef}>
            <header className="topbar">
                <Link className="brand" to={lastInboxUrl()}>
                    bb-review
                </Link>
                <Link className="btn btn-small btn-ghost" to={lastInboxUrl()}>
                    ← Inbox
                </Link>
                <span className="topbar-spacer" />
                <CommandButton />
            </header>

            <section className="help-intro">
                <h1>Help</h1>
                <p className="muted">
                    Keyboard shortcuts, comment navigation and how the review list is built.
                </p>
                <input
                    className="help-search"
                    type="search"
                    value={query}
                    onChange={(event) => setQuery(event.target.value)}
                    placeholder="Search shortcuts…"
                    aria-label="Search shortcuts"
                />
            </section>

            <section className="help-reference" aria-label="Keyboard shortcuts">
                {groups.length === 0 ? <p className="muted">No shortcuts match.</p> : null}
                {groups.map(({group, commands}) => (
                    <div className="help-group" key={group}>
                        <h2>{group}</h2>
                        <dl>
                            {commands.map((command) => (
                                <div className="help-row" key={command.id}>
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
                    </div>
                ))}
            </section>

            <section className="help-section">
                <h2>Reviewing a PR</h2>
                <p>
                    Open a pull request from the inbox and read the diff. Click a line, or press{' '}
                    <kbd>c</kbd>, to comment. Comments are saved as drafts; press <kbd>s</kbd> to open{' '}
                    <strong>Submit review</strong> and post them with Comment, Approve or Request changes.
                </p>
            </section>

            <section className="help-section">
                <h2>Comments navigation</h2>
                <p>
                    Press <kbd>]</kbd> and <kbd>[</kbd> to jump between threads, or <kbd>C</kbd> for the
                    comments panel. Clicking a thread in the panel jumps to it. Drafts, outdated and
                    general comments are all included.
                </p>
            </section>

            <section className="help-section">
                <h2>Phone tips</h2>
                <p>
                    Add bb-review to your Home Screen from the browser menu for a full-screen app. The
                    floating <strong>💬</strong> button jumps to the next comment and <strong>↑</strong>{' '}
                    returns to the top. The review bar stays pinned at the bottom of the screen.
                </p>
            </section>

            <section className="help-section">
                <h2>How the To-review list is found</h2>
                <p>
                    The app scans the workspace repositories that had activity during the active window
                    (14 days by default). A background scanner re-checks changed and hot repositories every
                    minute, with a full sweep every six hours. Repositories listed in <code>repos</code> in
                    the config are always checked. Results are served from that cached scan state, and an
                    hourly request budget keeps Bitbucket rate limits in check.
                </p>
            </section>

            <p className="help-foot">
                <Link to={lastInboxUrl()}>← Back to inbox</Link>
            </p>
        </div>
    );
}
