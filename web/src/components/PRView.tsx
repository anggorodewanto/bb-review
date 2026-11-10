import {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {Link, useNavigate, useParams} from 'react-router-dom';
import {useMutation, useQuery, useQueryClient} from '@tanstack/react-query';
import {getChangeKey, isDelete} from 'react-diff-view';
import type {ChangeData, HunkData, ViewType} from 'react-diff-view';
import * as api from '../api';
import type {Comment, Draft, InlineAnchor, PRDetail, SubmitResult, ViewedState} from '../types';
import {buildFileEntries} from '../lib/diff';
import type {FileEntry} from '../lib/diff';
import {anchorForChange, commentCounts, groupComments} from '../lib/comments';
import {resolveKey} from '../lib/keymap';
import type {KeyAction} from '../lib/keymap';
import {readPref, writePref} from '../lib/prefs';
import {useMediaQuery} from '../lib/useMediaQuery';
import type {CommentMode} from '../components/CommentEditor';
import {toggleViewed as toggleViewedMap, viewedStatus} from '../lib/viewed';
import {ErrorState, Skeleton} from '../components/ui';
import {FileTree} from '../components/FileTree';
import {FileDiff} from '../components/FileDiff';
import {PRHeader} from '../components/PRHeader';
import {ReviewBar} from '../components/ReviewBar';
import type {SubmitAction} from '../components/ReviewBar';
import {HelpOverlay} from '../components/HelpOverlay';
import {GeneralComments} from '../components/GeneralComments';
import type {ThreadActions} from '../components/CommentThread';

const EMPTY_INDEX = {newLines: new Map<number, string>(), oldLines: new Map<number, string>()};

type CommentInput = {raw: string; inline?: InlineAnchor; parent_id?: number};
type FocusState = {fileIndex: number; key: string; anchor: InlineAnchor};
type EditorState = {fileIndex: number; key: string; anchor: InlineAnchor};

function messageOf(error: unknown): string {
    return error instanceof Error ? error.message : 'Something went wrong';
}

export function PRView() {
    const params = useParams<{ws: string; repo: string; id: string}>();
    const ws = params.ws ?? '';
    const repo = params.repo ?? '';
    const prId = Number(params.id);
    const valid = ws.length > 0 && repo.length > 0 && Number.isInteger(prId) && prId > 0;

    const navigate = useNavigate();
    const queryClient = useQueryClient();

    const prKey = useMemo(() => ['pr', ws, repo, prId] as const, [ws, repo, prId]);
    const diffKey = useMemo(() => ['pr', ws, repo, prId, 'diff'] as const, [ws, repo, prId]);
    const commentsKey = useMemo(() => ['pr', ws, repo, prId, 'comments'] as const, [ws, repo, prId]);
    const draftsKey = useMemo(() => ['pr', ws, repo, prId, 'drafts'] as const, [ws, repo, prId]);
    const viewedKey = useMemo(() => ['pr', ws, repo, prId, 'viewed'] as const, [ws, repo, prId]);

    const prQuery = useQuery({
        queryKey: prKey,
        queryFn: () => api.getPR(ws, repo, prId),
        enabled: valid,
    });
    const diffQuery = useQuery({
        queryKey: diffKey,
        queryFn: () => api.getDiff(ws, repo, prId),
        enabled: valid,
    });
    const commentsQuery = useQuery({
        queryKey: commentsKey,
        queryFn: () => api.getComments(ws, repo, prId),
        enabled: valid,
    });
    const draftsQuery = useQuery({
        queryKey: draftsKey,
        queryFn: () => api.getDrafts(ws, repo, prId),
        enabled: valid,
    });
    const viewedQuery = useQuery({
        queryKey: viewedKey,
        queryFn: () => api.getViewed(ws, repo, prId),
        enabled: valid,
    });

    const pr = prQuery.data;
    const comments = useMemo(() => commentsQuery.data ?? [], [commentsQuery.data]);
    const drafts = useMemo(() => draftsQuery.data ?? [], [draftsQuery.data]);
    const viewedFiles = viewedQuery.data?.files ?? {};

    const files = useMemo(() => buildFileEntries(diffQuery.data ?? ''), [diffQuery.data]);
    const commentCountByPath = useMemo(() => commentCounts(comments), [comments]);
    const generalThreads = useMemo(
        () => groupComments(comments.filter((comment) => !comment.inline), EMPTY_INDEX, '').general,
        [comments],
    );
    const generalDrafts = useMemo(
        () => drafts.filter((draft) => !draft.inline && draft.parent_id == null),
        [drafts],
    );
    const draftReplies = useMemo(() => {
        const map = new Map<number, Draft[]>();
        for (const draft of drafts) {
            if (draft.parent_id == null) {
                continue;
            }
            const list = map.get(draft.parent_id);
            if (list) {
                list.push(draft);
                continue;
            }
            map.set(draft.parent_id, [draft]);
        }
        return map;
    }, [drafts]);

    // --- layout state -------------------------------------------------------
    const isMobile = useMediaQuery('(max-width: 1099px)');
    const forceUnified = useMediaQuery('(max-width: 699px)');

    const [viewPref, setViewPref] = useState<ViewType>(() => {
        const stored = readPref<string | null>('bb-review:view', null);
        if (stored === 'split' || stored === 'unified') {
            return stored;
        }
        if (typeof window.matchMedia !== 'function') {
            return 'unified';
        }
        return window.matchMedia('(min-width: 1100px)').matches ? 'split' : 'unified';
    });
    const [currentIndex, setCurrentIndex] = useState(0);
    const [focus, setFocus] = useState<FocusState | null>(null);
    const [editor, setEditor] = useState<EditorState | null>(null);
    const [treeVisible, setTreeVisible] = useState(true);
    const [treeOpen, setTreeOpen] = useState(false);
    const [helpOpen, setHelpOpen] = useState(false);
    const [reviewOpen, setReviewOpen] = useState(false);
    const [failures, setFailures] = useState<SubmitResult['failed']>([]);

    const viewType: ViewType = forceUnified ? 'unified' : viewPref;

    // --- refs for stable keyboard handlers ---------------------------------
    const filesRef = useRef<FileEntry[]>(files);
    filesRef.current = files;
    const currentIndexRef = useRef(currentIndex);
    currentIndexRef.current = currentIndex;
    const focusRef = useRef<FocusState | null>(focus);
    focusRef.current = focus;
    const prRef = useRef<PRDetail | undefined>(pr);
    prRef.current = pr;
    const editorRef = useRef<EditorState | null>(editor);
    editorRef.current = editor;
    const fileRefs = useRef<Array<HTMLElement | null>>([]);
    const hunkCursor = useRef<{fileIndex: number; hunk: number}>({fileIndex: -1, hunk: -1});
    const pendingChord = useRef<string | null>(null);

    // Reset per-PR view state only when the PR itself changes (a background
    // refetch of the same PR must not yank the reader back to the first file).
    useEffect(() => {
        setCurrentIndex(0);
        setFocus(null);
        setEditor(null);
        hunkCursor.current = {fileIndex: -1, hunk: -1};
        window.scrollTo(0, 0);
    }, [prId]);

    useEffect(() => {
        if (currentIndex <= files.length - 1) {
            return;
        }
        setCurrentIndex(Math.max(0, files.length - 1));
    }, [files.length, currentIndex]);

    const registerRef = useCallback((index: number, el: HTMLElement | null) => {
        fileRefs.current[index] = el;
    }, []);

    const scrollToFile = useCallback((index: number) => {
        setCurrentIndex(index);
        const el = fileRefs.current[index];
        if (!el) {
            return;
        }
        el.scrollIntoView({behavior: 'smooth', block: 'start'});
    }, []);

    // --- mutations ----------------------------------------------------------
    const invalidatePr = useCallback(() => {
        void queryClient.invalidateQueries({queryKey: prKey});
        void queryClient.invalidateQueries({queryKey: ['prs']});
    }, [queryClient, prKey]);

    const approveMutation = useMutation({
        mutationFn: (on: boolean) => api.setApprove(ws, repo, prId, on),
        onSuccess: (state) => {
            queryClient.setQueryData<PRDetail>(prKey, (old) => (old ? {...old, my_state: state} : old));
            invalidatePr();
        },
    });
    const changesMutation = useMutation({
        mutationFn: (on: boolean) => api.setRequestChanges(ws, repo, prId, on),
        onSuccess: (state) => {
            queryClient.setQueryData<PRDetail>(prKey, (old) => (old ? {...old, my_state: state} : old));
            invalidatePr();
        },
    });

    const createDraftMutation = useMutation({
        mutationFn: (input: CommentInput) => api.createDraft(ws, repo, prId, input),
        onSuccess: (draft) => {
            queryClient.setQueryData<Draft[]>(draftsKey, (old) => [...(old ?? []), draft]);
        },
    });
    const postCommentMutation = useMutation({
        mutationFn: (input: CommentInput) => api.postComment(ws, repo, prId, input),
        onSuccess: (comment) => {
            queryClient.setQueryData<Comment[]>(commentsKey, (old) => [...(old ?? []), comment]);
            invalidatePr();
        },
    });
    const updateDraftMutation = useMutation({
        mutationFn: (input: {id: string; raw: string}) =>
            api.updateDraft(ws, repo, prId, input.id, input.raw),
        onSuccess: (draft) => {
            queryClient.setQueryData<Draft[]>(draftsKey, (old) =>
                (old ?? []).map((item) => (item.id === draft.id ? draft : item)),
            );
        },
    });
    const deleteDraftMutation = useMutation({
        mutationFn: (id: string) => api.deleteDraft(ws, repo, prId, id),
        onSuccess: (_data, id) => {
            queryClient.setQueryData<Draft[]>(draftsKey, (old) => (old ?? []).filter((item) => item.id !== id));
        },
    });
    const submitMutation = useMutation({
        mutationFn: (action: 'approve' | 'request_changes' | null) =>
            api.submitDrafts(ws, repo, prId, action),
        onSuccess: (result) => {
            setFailures(result.failed);
            setReviewOpen(false);
            void queryClient.invalidateQueries({queryKey: draftsKey});
            void queryClient.invalidateQueries({queryKey: commentsKey});
            invalidatePr();
        },
    });
    const putViewedMutation = useMutation({
        mutationFn: (filesNext: Record<string, string>) =>
            api.putViewed(ws, repo, prId, {files: filesNext}),
        onSuccess: (state) => {
            queryClient.setQueryData<ViewedState>(viewedKey, state);
        },
        onError: () => {
            void queryClient.invalidateQueries({queryKey: viewedKey});
        },
    });

    // --- callbacks ----------------------------------------------------------
    const toggleApprove = useCallback(() => {
        const current = prRef.current;
        if (!current) {
            return;
        }
        approveMutation.mutate(current.my_state !== 'approved');
    }, [approveMutation.mutate]);

    const toggleRequestChanges = useCallback(() => {
        const current = prRef.current;
        if (!current) {
            return;
        }
        changesMutation.mutate(current.my_state !== 'changes_requested');
    }, [changesMutation.mutate]);

    const toggleFileViewed = useCallback(
        (entry: FileEntry) => {
            const previous = queryClient.getQueryData<ViewedState>(viewedKey)?.files ?? {};
            const next = toggleViewedMap(previous, entry.path, entry.hash);
            queryClient.setQueryData<ViewedState>(viewedKey, {files: next});
            putViewedMutation.mutate(next);
        },
        [queryClient, viewedKey, putViewedMutation.mutate],
    );

    const onLineClick = useCallback(
        (entry: FileEntry, index: number, change: ChangeData, side: 'old' | 'new') => {
            const anchor = anchorForChange(entry.path, change, side);
            const key = getChangeKey(change);
            setCurrentIndex(index);
            setFocus({fileIndex: index, key, anchor});
            setEditor({fileIndex: index, key, anchor});
        },
        [],
    );

    const addDraft = useCallback(
        async (input: CommentInput) => {
            await createDraftMutation.mutateAsync(input);
        },
        [createDraftMutation.mutateAsync],
    );
    const addComment = useCallback(
        async (input: CommentInput) => {
            await postCommentMutation.mutateAsync(input);
        },
        [postCommentMutation.mutateAsync],
    );

    const replyTo = useCallback(
        async (parent: Comment, raw: string, mode: CommentMode) => {
            const base: CommentInput = parent.inline
                ? {raw, inline: parent.inline, parent_id: parent.id}
                : {raw, parent_id: parent.id};
            if (mode === 'draft') {
                await addDraft(base);
                return;
            }
            await addComment(base);
        },
        [addDraft, addComment],
    );

    const updateDraft = useCallback(
        async (draft: Draft, raw: string) => {
            await updateDraftMutation.mutateAsync({id: draft.id, raw});
        },
        [updateDraftMutation.mutateAsync],
    );

    const removeDraft = useCallback(
        async (draft: Draft) => {
            await deleteDraftMutation.mutateAsync(draft.id);
        },
        [deleteDraftMutation.mutateAsync],
    );

    const threadActions: ThreadActions = useMemo(
        () => ({onReply: replyTo, onUpdateDraft: updateDraft, onDeleteDraft: removeDraft}),
        [replyTo, updateDraft, removeDraft],
    );

    const submitInlineEditor = useCallback(
        async (key: string, raw: string, mode: CommentMode) => {
            const current = editorRef.current;
            if (!current || current.key !== key) {
                return;
            }
            const input: CommentInput = {raw, inline: current.anchor};
            if (mode === 'draft') {
                await addDraft(input);
            } else {
                await addComment(input);
            }
            setEditor(null);
        },
        [addDraft, addComment],
    );

    const cancelEditor = useCallback(() => setEditor(null), []);

    const loadSources = useCallback(
        async (entry: FileEntry): Promise<{old: string; new: string} | null> => {
            const detail = prRef.current;
            if (!detail) {
                return null;
            }
            const [oldText, newText] = await Promise.all([
                entry.oldPath
                    ? api.getFile(ws, repo, detail.merge_base, entry.oldPath).catch(() => '')
                    : Promise.resolve(''),
                entry.newPath
                    ? api.getFile(ws, repo, detail.source_commit, entry.newPath).catch(() => '')
                    : Promise.resolve(''),
            ]);
            return {old: oldText, new: newText};
        },
        [ws, repo],
    );

    const submitReview = useCallback(
        (action: SubmitAction) => {
            submitMutation.mutate(action === 'comment' ? null : action);
        },
        [submitMutation.mutate],
    );

    // --- keyboard -----------------------------------------------------------
    const toggleTree = useCallback(() => {
        if (window.matchMedia('(max-width: 1099px)').matches) {
            setTreeOpen((open) => !open);
            return;
        }
        setTreeVisible((visible) => !visible);
    }, []);

    const toggleView = useCallback(() => {
        setViewPref((current) => {
            const next: ViewType = current === 'split' ? 'unified' : 'split';
            writePref('bb-review:view', next);
            return next;
        });
    }, []);

    const goHunk = useCallback(
        (delta: 1 | -1) => {
            const list = filesRef.current;
            if (list.length === 0) {
                return;
            }
            let index = currentIndexRef.current;
            for (let guard = 0; guard <= list.length; guard += 1) {
                const section = fileRefs.current[index];
                const hunks = section ? Array.from(section.querySelectorAll<HTMLElement>('.diff-hunk')) : [];
                if (hunks.length > 0) {
                    const sameFile = hunkCursor.current.fileIndex === index;
                    const cursor = sameFile ? hunkCursor.current.hunk : delta === 1 ? -1 : hunks.length;
                    const next = cursor + delta;
                    if (next >= 0 && next < hunks.length) {
                        hunkCursor.current = {fileIndex: index, hunk: next};
                        setCurrentIndex(index);
                        hunks[next]?.scrollIntoView({behavior: 'smooth', block: 'center'});
                        return;
                    }
                }
                index = (index + delta + list.length) % list.length;
                const nextSection = fileRefs.current[index];
                const nextHunks = nextSection
                    ? Array.from(nextSection.querySelectorAll<HTMLElement>('.diff-hunk'))
                    : [];
                if (nextHunks.length > 0) {
                    const target = delta === 1 ? 0 : nextHunks.length - 1;
                    hunkCursor.current = {fileIndex: index, hunk: target};
                    setCurrentIndex(index);
                    nextHunks[target]?.scrollIntoView({behavior: 'smooth', block: 'center'});
                    return;
                }
            }
        },
        [],
    );

    const goLine = useCallback((delta: 1 | -1) => {
        const list = filesRef.current;
        const index = currentIndexRef.current;
        const entry = list[index];
        if (!entry) {
            return;
        }
        const changes = flattenChanges(entry.hunks, entry.path);
        if (changes.length === 0) {
            return;
        }
        const current = focusRef.current?.fileIndex === index ? focusRef.current.key : null;
        const at = current ? changes.findIndex((item) => item.key === current) : -1;
        const nextIndex =
            at === -1 ? (delta === 1 ? 0 : changes.length - 1) : Math.min(changes.length - 1, Math.max(0, at + delta));
        const target = changes[nextIndex];
        if (!target) {
            return;
        }
        setFocus({fileIndex: index, key: target.key, anchor: target.anchor});
        const el = document.getElementById(`change-${index}-${target.key}`);
        el?.scrollIntoView({behavior: 'smooth', block: 'center'});
    }, []);

    const commentOnFocus = useCallback(() => {
        const current = focusRef.current;
        if (!current) {
            return;
        }
        setEditor({...current});
    }, []);

    const toggleViewedAndAdvance = useCallback(() => {
        const list = filesRef.current;
        const index = currentIndexRef.current;
        const entry = list[index];
        if (!entry) {
            return;
        }
        const previous = queryClient.getQueryData<ViewedState>(viewedKey)?.files ?? {};
        const next = toggleViewedMap(previous, entry.path, entry.hash);
        queryClient.setQueryData<ViewedState>(viewedKey, {files: next});
        putViewedMutation.mutate(next);

        if (viewedStatus(next, entry.path, entry.hash) === 'viewed') {
            return;
        }
        const target = list.findIndex(
            (item, i) => i > index && viewedStatus(next, item.path, item.hash) !== 'viewed',
        );
        if (target >= 0) {
            scrollToFile(target);
        }
    }, [queryClient, viewedKey, putViewedMutation.mutate, scrollToFile]);

    const actions = useMemo<Record<KeyAction, () => void>>(
        () => ({
            'next-file': () => scrollToFile(Math.min(filesRef.current.length - 1, currentIndexRef.current + 1)),
            'prev-file': () => scrollToFile(Math.max(0, currentIndexRef.current - 1)),
            'next-hunk': () => goHunk(1),
            'prev-hunk': () => goHunk(-1),
            'next-line': () => goLine(1),
            'prev-line': () => goLine(-1),
            comment: commentOnFocus,
            'toggle-viewed': toggleViewedAndAdvance,
            approve: toggleApprove,
            submit: () => setReviewOpen(true),
            'toggle-tree': toggleTree,
            'toggle-split': toggleView,
            inbox: () => navigate('/'),
            help: () => setHelpOpen(true),
            escape: () => {
                setEditor(null);
                setReviewOpen(false);
                setTreeOpen(false);
                setHelpOpen(false);
            },
        }),
        [
            scrollToFile,
            goHunk,
            goLine,
            commentOnFocus,
            toggleViewedAndAdvance,
            toggleApprove,
            toggleTree,
            toggleView,
            navigate,
        ],
    );
    const actionsRef = useRef(actions);
    actionsRef.current = actions;

    useEffect(() => {
        function onKeyDown(event: KeyboardEvent) {
            const result = resolveKey(
                {
                    key: event.key,
                    ctrlKey: event.ctrlKey,
                    metaKey: event.metaKey,
                    altKey: event.altKey,
                    target: event.target,
                },
                pendingChord.current,
            );
            pendingChord.current = result.pending;
            if (!result.action) {
                return;
            }
            const handler = actionsRef.current[result.action];
            if (!handler) {
                return;
            }
            event.preventDefault();
            handler();
        }

        window.addEventListener('keydown', onKeyDown);
        return () => window.removeEventListener('keydown', onKeyDown);
    }, []);

    // --- render -------------------------------------------------------------
    if (!valid) {
        return (
            <div className="page">
                <ErrorState
                    message="That pull request URL is not valid."
                    onRetry={() => navigate('/')}
                />
            </div>
        );
    }

    if (prQuery.isPending) {
        return (
            <div className="page pr-page">
                <Skeleton rows={6} />
            </div>
        );
    }

    if (prQuery.isError || !pr) {
        return (
            <div className="page">
                <ErrorState
                    message={messageOf(prQuery.error)}
                    onRetry={() => void prQuery.refetch()}
                />
            </div>
        );
    }

    const busyReview = approveMutation.isPending || changesMutation.isPending;
    const diffError = diffQuery.isError ? messageOf(diffQuery.error) : null;

    return (
        <div className={`pr-page${treeVisible ? '' : ' no-tree'}`}>
            {treeOpen ? <div className="drawer-backdrop" onClick={() => setTreeOpen(false)} /> : null}

            <div className={`tree-slot${treeOpen ? ' is-open' : ''}`}>
                <FileTree
                    files={files}
                    currentIndex={currentIndex}
                    comments={commentCountByPath}
                    viewedFiles={viewedFiles}
                    onSelect={(index) => {
                        scrollToFile(index);
                        setTreeOpen(false);
                    }}
                    onToggleViewed={toggleFileViewed}
                    onClose={isMobile ? () => setTreeOpen(false) : undefined}
                />
                {treeOpen ? (
                    <button type="button" className="btn drawer-close" onClick={() => setTreeOpen(false)}>
                        Close files
                    </button>
                ) : null}
            </div>

            <main className="pr-main">
                <PRHeader
                    pr={pr}
                    busy={busyReview}
                    onApprove={toggleApprove}
                    onRequestChanges={toggleRequestChanges}
                />

                <div className="pr-toolbar">
                    <button type="button" className="btn btn-small tree-toggle" onClick={toggleTree}>
                        Files
                    </button>
                    <button type="button" className="btn btn-small" onClick={toggleView} title="Toggle split / unified (u)">
                        {viewType === 'split' ? 'Unified' : 'Split'}
                    </button>
                    <button type="button" className="btn btn-small" onClick={() => setHelpOpen(true)} title="Keyboard help (?)">
                        ?
                    </button>
                    <span className="toolbar-spacer" />
                    <ReviewBar
                        draftCount={drafts.length}
                        busy={submitMutation.isPending}
                        open={reviewOpen}
                        onOpenChange={setReviewOpen}
                        onSelect={submitReview}
                        failures={failures}
                        onDismissFailures={() => setFailures([])}
                    />
                </div>

                {submitMutation.isError ? (
                    <div className="warn-banner" role="alert">
                        Submit failed: {messageOf(submitMutation.error)} — your drafts are still saved.
                    </div>
                ) : null}

                {commentsQuery.isError ? (
                    <div className="warn-banner" role="alert">
                        Comments failed to load: {messageOf(commentsQuery.error)}
                    </div>
                ) : null}

                {diffError ? (
                    <ErrorState message={diffError} onRetry={() => void diffQuery.refetch()} />
                ) : diffQuery.isPending ? (
                    <Skeleton rows={8} />
                ) : files.length === 0 ? (
                    <div className="state state-empty">
                        <p className="state-title">No changes</p>
                        <p className="state-hint">This pull request has no file diff yet.</p>
                    </div>
                ) : (
                    <div className="file-list">
                        {files.map((entry, index) => (
                            <FileDiff
                                key={`${entry.path}:${entry.hash}`}
                                entry={entry}
                                index={index}
                                viewType={viewType}
                                focusedKey={focus?.fileIndex === index ? focus.key : null}
                                comments={comments}
                                drafts={drafts}
                                draftReplies={draftReplies}
                                actions={threadActions}
                                editorKey={editor?.fileIndex === index ? editor.key : null}
                                onEditorSubmit={submitInlineEditor}
                                onEditorCancel={cancelEditor}
                                viewed={viewedStatus(viewedFiles, entry.path, entry.hash)}
                                onToggleViewed={toggleFileViewed}
                                onLineClick={onLineClick}
                                onLoadSources={loadSources}
                                onVisible={setCurrentIndex}
                                registerRef={registerRef}
                            />
                        ))}
                    </div>
                )}

                <GeneralComments
                    threads={generalThreads}
                    drafts={generalDrafts}
                    draftReplies={draftReplies}
                    actions={threadActions}
                    onAdd={async (raw, mode) => {
                        if (mode === 'draft') {
                            await addDraft({raw});
                            return;
                        }
                        await addComment({raw});
                    }}
                />

                <p className="pr-foot">
                    <Link to="/">← Back to inbox</Link>
                    <span className="sep">·</span>
                    <button type="button" className="btn btn-small btn-ghost" onClick={() => setHelpOpen(true)}>
                        Keyboard shortcuts (?)
                    </button>
                </p>
            </main>

            <HelpOverlay open={helpOpen} onClose={() => setHelpOpen(false)} />
        </div>
    );
}

function flattenChanges(
    hunks: HunkData[],
    path: string,
): Array<{key: string; anchor: InlineAnchor; change: ChangeData}> {
    const output: Array<{key: string; anchor: InlineAnchor; change: ChangeData}> = [];
    for (const hunk of hunks) {
        for (const change of hunk.changes) {
            output.push({
                key: getChangeKey(change),
                anchor: anchorForChange(path, change, isDelete(change) ? 'old' : 'new'),
                change,
            });
        }
    }
    return output;
}
