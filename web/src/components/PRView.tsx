import {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {useLocation, useNavigate, useParams} from 'react-router-dom';
import {useMutation, useQuery, useQueryClient} from '@tanstack/react-query';
import {getChangeKey, isDelete} from 'react-diff-view';
import type {ChangeData, HunkData, ViewType} from 'react-diff-view';
import * as api from '../api';
import type {Comment, Draft, InlineAnchor, PRDetail, SubmitResult, ViewedState} from '../types';
import {buildFileEntries} from '../lib/diff';
import type {FileEntry} from '../lib/diff';
import {anchorForChange, commentCounts, groupComments} from '../lib/comments';
import {buildCommentNav, findNavItem, navItemDomId, navItemHash, parseCommentHash} from '../lib/commentNav';
import type {NavItem} from '../lib/commentNav';
import {prefersReducedMotion, scrollPageToTop} from '../lib/scroll';
import {fileIndexAtLine, nextNavTarget} from '../lib/fileNav';
import type {DynamicCommand} from '../lib/commands';
import {setCommandDetail} from '../lib/commandFeedback';
import {lastInboxUrl} from '../lib/inboxUrl';
import {backTarget} from '../lib/navigation';
import {useScrollMemory} from '../lib/useScrollMemory';
import {usePageCommands} from '../components/CommandHost';
import {readPref, writePref} from '../lib/prefs';
import {useMediaQuery} from '../lib/useMediaQuery';
import type {CommentMode} from '../components/CommentEditor';
import {toggleViewed as toggleViewedMap, viewedStatus} from '../lib/viewed';
import {ErrorState, Skeleton} from '../components/ui';
import {FileTree} from '../components/FileTree';
import {FileDiff} from '../components/FileDiff';
import {FileDiffBoundary} from '../components/FileDiffBoundary';
import {PRHeader} from '../components/PRHeader';
import {ReviewBar} from '../components/ReviewBar';
import type {SubmitAction} from '../components/ReviewBar';
import {HelpOverlay} from '../components/HelpOverlay';
import {GeneralComments} from '../components/GeneralComments';
import {CommentsPanel} from '../components/CommentsPanel';
import {BackToTop} from '../components/BackToTop';
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
    const location = useLocation();
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
    const meQuery = useQuery({
        queryKey: ['me'],
        queryFn: api.getMe,
        staleTime: Infinity,
        retry: 1,
    });

    const pr = prQuery.data;
    const comments = useMemo(() => commentsQuery.data ?? [], [commentsQuery.data]);
    const drafts = useMemo(() => draftsQuery.data ?? [], [draftsQuery.data]);
    const viewedFiles = viewedQuery.data?.files ?? {};
    const me = meQuery.data ?? null;

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
    const navItems = useMemo(
        () => buildCommentNav(files, comments, drafts, me),
        [files, comments, drafts, me],
    );

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
    const [wrapLines, setWrapLines] = useState<boolean>(() => readPref<boolean>('bb-review:wrap', true));
    const [currentIndex, setCurrentIndex] = useState(0);
    const [focus, setFocus] = useState<FocusState | null>(null);
    const [editor, setEditor] = useState<EditorState | null>(null);
    const [treeVisible, setTreeVisible] = useState(true);
    const [treeOpen, setTreeOpen] = useState(false);
    const [helpOpen, setHelpOpen] = useState(false);
    const [reviewOpen, setReviewOpen] = useState(false);
    const [failures, setFailures] = useState<SubmitResult['failed']>([]);
    const [commentsOpen, setCommentsOpen] = useState(false);
    const [activeNavId, setActiveNavId] = useState<string | null>(null);
    const [reveal, setReveal] = useState<{fileIndex: number; nonce: number} | null>(null);
    const [expandRequest, setExpandRequest] = useState<{
        fileIndex: number;
        nonce: number;
        all: boolean;
    } | null>(null);

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
    const pageRef = useRef<HTMLDivElement | null>(null);
    const navItemsRef = useRef<NavItem[]>(navItems);
    navItemsRef.current = navItems;
    const revealNonce = useRef(0);
    const navCursor = useRef(-1);
    const deepLinkDone = useRef(false);
    const expandNonce = useRef(0);
    const navTargetRef = useRef<number | null>(null);
    const navLockRef = useRef(false);
    const navCorrectRef = useRef(false);
    // scrollY where the last j/k navigation came to rest. While the page stays
    // there, the scroll-spy keeps the navigated file as current; this matters
    // at the bottom of the page, where short last files can't reach the top.
    const navRestY = useRef<number | null>(null);
    const navSettleTimer = useRef<number | null>(null);
    const navHardTimer = useRef<number | null>(null);
    const navFrame = useRef<number | null>(null);

    // Reset per-PR view state only when the PR itself changes (a background
    // refetch of the same PR must not yank the reader back to the first file).
    useEffect(() => {
        setCurrentIndex(0);
        setFocus(null);
        setEditor(null);
        setCommentsOpen(false);
        setActiveNavId(null);
        setReveal(null);
        setExpandRequest(null);
        navCursor.current = -1;
        deepLinkDone.current = false;
        navLockRef.current = false;
        navTargetRef.current = null;
        clearNavTimers();
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

    // --- scroll-spy + navigation lock --------------------------------------
    // The reading line sits just below the sticky toolbar; it must match the
    // CSS `scroll-margin-top` used when a file is scrolled into view.
    const readingLine = useCallback((): number => {
        const toolbar = document.querySelector<HTMLElement>('.pr-toolbar');
        if (!toolbar) {
            return 8;
        }
        const position = window.getComputedStyle(toolbar).position;
        if (position !== 'sticky' && position !== 'fixed') {
            return 8;
        }
        const offset = Math.max(8, Math.round(toolbar.getBoundingClientRect().height) + 8);
        document.documentElement.style.setProperty('--pr-sticky-offset', `${offset}px`);
        return offset;
    }, []);

    const clearNavTimers = useCallback(() => {
        if (navSettleTimer.current !== null) {
            window.clearTimeout(navSettleTimer.current);
            navSettleTimer.current = null;
        }
        if (navHardTimer.current !== null) {
            window.clearTimeout(navHardTimer.current);
            navHardTimer.current = null;
        }
    }, []);

    const settleNavigation = useCallback(() => {
        if (!navLockRef.current) {
            return;
        }
        navLockRef.current = false;
        clearNavTimers();
        const target = navTargetRef.current;
        navTargetRef.current = null;
        if (target === null) {
            return;
        }
        // Update the ref now: fast repeated presses run before React re-renders.
        currentIndexRef.current = target;
        setCurrentIndex(target);
        navRestY.current = window.scrollY;
        if (!navCorrectRef.current) {
            return;
        }
        navCorrectRef.current = false;
        const line = readingLine();
        window.requestAnimationFrame(() => {
            const section = fileRefs.current[target];
            if (!section) {
                return;
            }
            const top = section.getBoundingClientRect().top;
            if (Math.abs(top - line) <= 4) {
                return;
            }
            window.scrollTo({top: window.scrollY + top - line, behavior: 'auto'});
            navRestY.current = window.scrollY;
        });
    }, [clearNavTimers, readingLine]);

    const beginNavigation = useCallback(
        (target: number, correct: boolean) => {
            navTargetRef.current = target;
            navCorrectRef.current = correct;
            navLockRef.current = true;
            clearNavTimers();
            navHardTimer.current = window.setTimeout(() => settleNavigation(), 1500);
        },
        [clearNavTimers, settleNavigation],
    );

    const scrollToFile = useCallback(
        (index: number) => {
            const list = filesRef.current;
            if (list.length === 0) {
                return;
            }
            const target = Math.min(list.length - 1, Math.max(0, index));
            // Keep the CSS scroll-margin in sync with the measured header.
            readingLine();
            beginNavigation(target, true);
            setCurrentIndex(target);
            const el = fileRefs.current[target];
            if (!el) {
                settleNavigation();
                return;
            }
            // Already in place: no scroll (and no scrollend) will happen, so
            // settle now instead of waiting for the 1.5s fallback.
            if (Math.abs(el.getBoundingClientRect().top - readingLine()) <= 4) {
                settleNavigation();
                return;
            }
            // Instant jump: keyboard navigation should feel immediate, and a
            // smooth scroll races the scroll-spy (and is throttled in
            // background windows).
            if (typeof el.scrollIntoView === 'function') {
                el.scrollIntoView({behavior: 'auto', block: 'start'});
            }
            settleNavigation();
        },
        [beginNavigation, readingLine, settleNavigation],
    );

    const onScrollSpy = useCallback(() => {
        if (navLockRef.current) {
            if (navSettleTimer.current !== null) {
                window.clearTimeout(navSettleTimer.current);
            }
            navSettleTimer.current = window.setTimeout(() => settleNavigation(), 120);
            return;
        }
        if (navFrame.current !== null) {
            return;
        }
        navFrame.current = window.requestAnimationFrame(() => {
            navFrame.current = null;
            if (navRestY.current !== null && Math.abs(window.scrollY - navRestY.current) < 2) {
                return;
            }
            navRestY.current = null;
            const tops = filesRef.current.map((_entry, index) => {
                const el = fileRefs.current[index];
                return el ? el.getBoundingClientRect().top : Number.POSITIVE_INFINITY;
            });
            // +2px: scrollIntoView lands on fractional offsets (e.g. 64.4 vs a
            // 64px line), which would otherwise count as the previous file.
            const index = fileIndexAtLine(tops, readingLine() + 2);
            if (index >= 0) {
                setCurrentIndex(index);
            }
        });
    }, [readingLine, settleNavigation]);

    useEffect(() => {
        const onScroll = () => onScrollSpy();
        const onScrollEnd = () => settleNavigation();
        window.addEventListener('scroll', onScroll, {passive: true});
        window.addEventListener('resize', onScroll);
        window.addEventListener('scrollend', onScrollEnd);
        onScrollSpy();
        return () => {
            window.removeEventListener('scroll', onScroll);
            window.removeEventListener('resize', onScroll);
            window.removeEventListener('scrollend', onScrollEnd);
            if (navFrame.current !== null) {
                window.cancelAnimationFrame(navFrame.current);
                navFrame.current = null;
            }
        };
    }, [onScrollSpy, settleNavigation, files.length]);

    useEffect(() => {
        return () => {
            clearNavTimers();
            if (navFrame.current !== null) {
                window.cancelAnimationFrame(navFrame.current);
                navFrame.current = null;
            }
        };
    }, [clearNavTimers]);

    // Wait for the thread to exist (lazy/expanded files render a frame later),
    // open any closed outdated section, then center and flash it.
    const scrollToNavItem = useCallback((item: NavItem) => {
        let frames = 0;
        const tick = () => {
            const el = document.getElementById(navItemDomId(item));
            if (!el) {
                frames += 1;
                if (frames <= 240) {
                    window.requestAnimationFrame(tick);
                }
                return;
            }
            const details = el.closest('details');
            if (details && !details.open) {
                details.open = true;
                frames += 1;
                if (frames <= 240) {
                    window.requestAnimationFrame(tick);
                }
                return;
            }
            const behavior: ScrollBehavior = prefersReducedMotion() ? 'auto' : 'smooth';
            if (typeof el.scrollIntoView === 'function') {
                el.scrollIntoView({behavior, block: 'center'});
            }
            el.classList.add('comment-flash');
            window.setTimeout(() => el.classList.remove('comment-flash'), 1000);
        };
        window.requestAnimationFrame(tick);
    }, []);

    const jumpToItem = useCallback(
        (item: NavItem) => {
            setActiveNavId(item.id);
            const nextHash = navItemHash(item);
            if (window.location.hash !== nextHash) {
                window.history.replaceState(
                    window.history.state,
                    '',
                    `${window.location.pathname}${window.location.search}${nextHash}`,
                );
            }
            if (item.fileIndex < 0) {
                scrollToNavItem(item);
                return;
            }
            beginNavigation(item.fileIndex, false);
            setCurrentIndex(item.fileIndex);
            setReveal({fileIndex: item.fileIndex, nonce: (revealNonce.current += 1)});
            if (item.changeKey && item.anchor) {
                setFocus({fileIndex: item.fileIndex, key: item.changeKey, anchor: item.anchor});
            }
            scrollToNavItem(item);
        },
        [beginNavigation, scrollToNavItem],
    );

    const goComment = useCallback(
        (delta: 1 | -1) => {
            const items = navItemsRef.current;
            if (items.length === 0) {
                return;
            }
            const next = (navCursor.current + delta + items.length) % items.length;
            const item = items[next];
            if (!item) {
                return;
            }
            navCursor.current = next;
            jumpToItem(item);
            setCommandDetail(`${next + 1}/${items.length}`);
        },
        [jumpToItem],
    );

    const selectNavItem = useCallback(
        (item: NavItem) => {
            const index = navItemsRef.current.findIndex((entry) => entry.id === item.id);
            if (index >= 0) {
                navCursor.current = index;
            }
            jumpToItem(item);
            if (isMobile) {
                setCommentsOpen(false);
            }
        },
        [jumpToItem, isMobile],
    );

    const jumpToFileComment = useCallback(
        (path: string) => {
            const item = navItemsRef.current.find((entry) => entry.path === path);
            if (!item) {
                return;
            }
            selectNavItem(item);
        },
        [selectNavItem],
    );

    const goTop = useCallback(() => {
        beginNavigation(0, false);
        scrollPageToTop(pageRef.current);
        setCurrentIndex(0);
        const first = filesRef.current[0];
        const change = first ? flattenChanges(first.hunks, first.path)[0] : undefined;
        setFocus(change ? {fileIndex: 0, key: change.key, anchor: change.anchor} : null);
    }, [beginNavigation]);

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

    const toggleWrap = useCallback(() => {
        setWrapLines((current) => {
            const next = !current;
            writePref('bb-review:wrap', next);
            return next;
        });
    }, []);

    const goHunk = useCallback(
        (delta: 1 | -1) => {
            const list = filesRef.current;
            if (list.length === 0) {
                return;
            }
            let index = navTargetRef.current ?? currentIndexRef.current;
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
                        beginNavigation(index, false);
                        hunks[next]?.scrollIntoView({
                            behavior: prefersReducedMotion() ? 'auto' : 'smooth',
                            block: 'center',
                        });
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
                    beginNavigation(index, false);
                    nextHunks[target]?.scrollIntoView({
                        behavior: prefersReducedMotion() ? 'auto' : 'smooth',
                        block: 'center',
                    });
                    return;
                }
            }
        },
        [beginNavigation],
    );

    const goLine = useCallback(
        (delta: 1 | -1) => {
            const list = filesRef.current;
            const index = navTargetRef.current ?? currentIndexRef.current;
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
            beginNavigation(index, false);
            const el = document.getElementById(`change-${index}-${target.key}`);
            el?.scrollIntoView({behavior: prefersReducedMotion() ? 'auto' : 'smooth', block: 'center'});
        },
        [beginNavigation],
    );

    const commentOnFocus = useCallback(() => {
        const current = focusRef.current;
        if (!current) {
            return;
        }
        setEditor({...current});
    }, []);

    const toggleViewedAndAdvance = useCallback(() => {
        const list = filesRef.current;
        const index = navTargetRef.current ?? currentIndexRef.current;
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

    const requestExpand = useCallback((all: boolean) => {
        const current = focusRef.current;
        if (!current) {
            return;
        }
        setExpandRequest({
            fileIndex: current.fileIndex,
            nonce: (expandNonce.current += 1),
            all,
        });
    }, []);

    const handlers = useMemo<Record<string, () => void>>(
        () => ({
            // Chain fast j/k presses from the in-flight target, not the spy.
            'next-file': () =>
                scrollToFile(
                    nextNavTarget(
                        currentIndexRef.current,
                        1,
                        navTargetRef.current,
                        filesRef.current.length,
                    ),
                ),
            'prev-file': () =>
                scrollToFile(
                    nextNavTarget(
                        currentIndexRef.current,
                        -1,
                        navTargetRef.current,
                        filesRef.current.length,
                    ),
                ),
            'next-hunk': () => goHunk(1),
            'prev-hunk': () => goHunk(-1),
            'next-line': () => goLine(1),
            'prev-line': () => goLine(-1),
            'next-comment': () => goComment(1),
            'prev-comment': () => goComment(-1),
            'toggle-comments': () => setCommentsOpen((open) => !open),
            'scroll-top': goTop,
            comment: commentOnFocus,
            'toggle-viewed': toggleViewedAndAdvance,
            approve: toggleApprove,
            submit: () => setReviewOpen(true),
            'toggle-tree': toggleTree,
            'toggle-split': toggleView,
            'toggle-wrap': toggleWrap,
            'expand-context': () => requestExpand(false),
            'expand-context-all': () => requestExpand(true),
            'inbox': () => navigate(lastInboxUrl()),
            help: () => setHelpOpen(true),
            escape: () => {
                setEditor(null);
                setReviewOpen(false);
                setTreeOpen(false);
                setHelpOpen(false);
                setCommentsOpen(false);
            },
        }),
        [
            scrollToFile,
            goHunk,
            goLine,
            goComment,
            goTop,
            commentOnFocus,
            toggleViewedAndAdvance,
            toggleApprove,
            toggleTree,
            toggleView,
            toggleWrap,
            requestExpand,
            navigate,
        ],
    );

    const dynamicCommands = useMemo<DynamicCommand[]>(
        () =>
            files.map((entry, index) => ({
                id: `file-${index}`,
                title: `Go to file: ${entry.path}`,
                section: 'files',
                search: [entry.path],
                run: () => scrollToFile(index),
            })),
        [files, scrollToFile],
    );

    usePageCommands({
        view: 'pr',
        handlers,
        hasFocus: focus !== null,
        hasFiles: files.length > 0,
        dynamic: dynamicCommands,
    });

    useScrollMemory(`pr:${ws}/${repo}/${prId}`, Boolean(pr));

    const goBack = useCallback(() => {
        if (backTarget(location.state).kind === 'back') {
            navigate(-1);
            return;
        }
        navigate('/');
    }, [location.state, navigate]);

    // Bitbucket links use #comment-<id>; jump once the comments have loaded.
    useEffect(() => {
        if (deepLinkDone.current) {
            return;
        }
        const hash = window.location.hash;
        if (!hash) {
            deepLinkDone.current = true;
            return;
        }
        const parsed = parseCommentHash(hash);
        if (!parsed) {
            deepLinkDone.current = true;
            return;
        }
        const item = findNavItem(navItems, parsed);
        if (!item) {
            return;
        }
        deepLinkDone.current = true;
        const index = navItems.indexOf(item);
        if (index >= 0) {
            navCursor.current = index;
        }
        jumpToItem(item);
    }, [navItems, jumpToItem]);

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
        <div
            ref={pageRef}
            className={`pr-page${treeVisible ? '' : ' no-tree'}${commentsOpen ? ' with-comments' : ''}`}
        >
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
                    onJumpToComment={(path) => {
                        jumpToFileComment(path);
                        setTreeOpen(false);
                    }}
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
                    onBack={goBack}
                    commentTotal={navItems.length}
                    onOpenComments={() => setCommentsOpen((open) => !open)}
                />

                <div className="pr-toolbar">
                    <button
                        type="button"
                        className="btn btn-small tree-toggle"
                        data-command="toggle-tree"
                        onClick={toggleTree}
                    >
                        Files
                    </button>
                    <button
                        type="button"
                        className="btn btn-small"
                        data-command="toggle-split"
                        onClick={toggleView}
                        title="Toggle split / unified (u)"
                    >
                        {viewType === 'split' ? 'Unified' : 'Split'}
                    </button>
                    <button
                        type="button"
                        className="btn btn-small"
                        data-command="toggle-wrap"
                        onClick={toggleWrap}
                        aria-pressed={wrapLines}
                        title="Toggle line wrapping (w)"
                    >
                        Wrap lines
                    </button>
                    <button
                        type="button"
                        className="btn btn-small"
                        data-command="help"
                        onClick={() => setHelpOpen(true)}
                        title="Keyboard help (?)"
                    >
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
                            <FileDiffBoundary
                                key={`${entry.path}:${entry.hash}`}
                                path={entry.path}
                                url={pr.url}
                            >
                                <FileDiff
                                    entry={entry}
                                    index={index}
                                    viewType={viewType}
                                    wrapLines={wrapLines}
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
                                    registerRef={registerRef}
                                    reveal={reveal?.fileIndex === index ? reveal.nonce : null}
                                    expand={
                                        expandRequest?.fileIndex === index
                                            ? {nonce: expandRequest.nonce, all: expandRequest.all}
                                            : null
                                    }
                                />
                            </FileDiffBoundary>
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
                    <button type="button" className="btn btn-small btn-ghost" onClick={goBack}>
                        ← Back to inbox
                    </button>
                    <span className="sep">·</span>
                    <button type="button" className="btn btn-small btn-ghost" onClick={() => setHelpOpen(true)}>
                        Keyboard shortcuts (?)
                    </button>
                </p>
            </main>

            {commentsOpen ? (
                <CommentsPanel
                    items={navItems}
                    activeId={activeNavId}
                    onSelect={selectNavItem}
                    onClose={() => setCommentsOpen(false)}
                />
            ) : null}

            <div className="float-actions">
                <BackToTop onClick={goTop} scrollRoot={pageRef} />
                {isMobile && navItems.length > 0 ? (
                    <button
                        type="button"
                        className="float-btn float-next-comment"
                        data-command="next-comment"
                        onClick={() => goComment(1)}
                        title="Next comment (])"
                        aria-label="Next comment"
                    >
                        💬↓
                    </button>
                ) : null}
            </div>

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
