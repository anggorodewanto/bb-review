import type {
    Comment,
    Draft,
    InlineAnchor,
    PRDetail,
    PRRef,
    PRSummary,
    ReviewState,
    SubmitResult,
    User,
    ViewedState,
} from './types';

const BASE = '/api';

type Method = 'GET' | 'POST' | 'PUT' | 'DELETE';

export class ApiError extends Error {
    readonly status: number;

    constructor(message: string, status: number) {
        super(message);
        this.name = 'ApiError';
        this.status = status;
    }
}

export type ApiResponse<T> = {
    data: T;
    /** True when the server answered with the `X-BBR-Partial` header. */
    partial: boolean;
    /** RFC3339 timestamp from `X-BBR-Fetched-At`, when the server supplied one. */
    fetchedAt: string | null;
    /** True when the server answered with `X-BBR-Rate-Limited: 1`. */
    rateLimited: boolean;
};

const enc = (value: string | number) => encodeURIComponent(String(value));
const encPath = (path: string) => path.split('/').map(enc).join('/');

export function prPath(workspace: string, repo: string, id: number): string {
    return `${BASE}/prs/${enc(workspace)}/${enc(repo)}/${enc(id)}`;
}

async function request<T>(method: Method, path: string, body?: unknown): Promise<ApiResponse<T>> {
    const headers: Record<string, string> = {
        Accept: 'application/json, text/plain, */*',
    };
    // CSRF guard: the Go server rejects any non-GET without this header.
    if (method !== 'GET') {
        headers['X-BBR'] = '1';
    }

    let payload: string | undefined;
    if (body !== undefined) {
        headers['Content-Type'] = 'application/json';
        payload = JSON.stringify(body);
    }

    const res = await fetch(path, {
        method,
        headers,
        body: payload,
        credentials: 'same-origin',
        cache: 'no-store',
    });

    const partial = res.headers.get('X-BBR-Partial') !== null;
    const fetchedAt = res.headers.get('X-BBR-Fetched-At');
    const rateLimited = res.headers.get('X-BBR-Rate-Limited') !== null;

    if (!res.ok) {
        let message = `${res.status} ${res.statusText || 'request failed'}`;
        try {
            const parsed: unknown = await res.json();
            if (isErrorBody(parsed)) {
                message = parsed.error || message;
            }
        } catch {
            // Non-JSON error body: keep the status line.
        }
        throw new ApiError(message, res.status);
    }

    const meta = {partial, fetchedAt, rateLimited};

    if (res.status === 204) {
        return {data: undefined as T, ...meta};
    }

    const contentType = res.headers.get('Content-Type') ?? '';
    if (contentType.includes('application/json')) {
        return {data: (await res.json()) as T, ...meta};
    }

    return {data: (await res.text()) as T, ...meta};
}

function isErrorBody(value: unknown): value is {error: string; status: number} {
    if (typeof value !== 'object' || value === null) return false;
    return typeof (value as {error?: unknown}).error === 'string';
}

// --- user & inbox -----------------------------------------------------------

export async function getMe(): Promise<User> {
    return (await request<User>('GET', `${BASE}/me`)).data;
}

export async function listPRs(role: 'reviewer' | 'author'): Promise<ApiResponse<PRSummary[]>> {
    return request<PRSummary[]>('GET', `${BASE}/prs?role=${role}&state=OPEN`);
}

/** Turn a bitbucket.org pull request URL into workspace/repo/id (backend validates it). */
export async function resolvePR(url: string): Promise<PRRef> {
    return (await request<PRRef>('GET', `${BASE}/resolve?url=${enc(url)}`)).data;
}

// --- pull request -----------------------------------------------------------

export async function getPR(workspace: string, repo: string, id: number): Promise<PRDetail> {
    return (await request<PRDetail>('GET', prPath(workspace, repo, id))).data;
}

export async function getDiff(workspace: string, repo: string, id: number): Promise<string> {
    return (await request<string>('GET', `${prPath(workspace, repo, id)}/diff`)).data;
}

export async function getComments(workspace: string, repo: string, id: number): Promise<Comment[]> {
    return (await request<Comment[]>('GET', `${prPath(workspace, repo, id)}/comments`)).data;
}

export async function postComment(
    workspace: string,
    repo: string,
    id: number,
    body: {raw: string; inline?: InlineAnchor; parent_id?: number},
): Promise<Comment> {
    return (await request<Comment>('POST', `${prPath(workspace, repo, id)}/comments`, body)).data;
}

// --- drafts -----------------------------------------------------------------

export async function getDrafts(workspace: string, repo: string, id: number): Promise<Draft[]> {
    return (await request<Draft[]>('GET', `${prPath(workspace, repo, id)}/drafts`)).data;
}

export async function createDraft(
    workspace: string,
    repo: string,
    id: number,
    body: {raw: string; inline?: InlineAnchor; parent_id?: number},
): Promise<Draft> {
    return (await request<Draft>('POST', `${prPath(workspace, repo, id)}/drafts`, body)).data;
}

export async function updateDraft(
    workspace: string,
    repo: string,
    id: number,
    draftId: string,
    raw: string,
): Promise<Draft> {
    return (
        await request<Draft>('PUT', `${prPath(workspace, repo, id)}/drafts/${enc(draftId)}`, {raw})
    ).data;
}

export async function deleteDraft(
    workspace: string,
    repo: string,
    id: number,
    draftId: string,
): Promise<void> {
    await request<void>('DELETE', `${prPath(workspace, repo, id)}/drafts/${enc(draftId)}`);
}

export async function submitDrafts(
    workspace: string,
    repo: string,
    id: number,
    action: 'approve' | 'request_changes' | null,
): Promise<SubmitResult> {
    return (
        await request<SubmitResult>('POST', `${prPath(workspace, repo, id)}/drafts/submit`, {action})
    ).data;
}

// --- review state -----------------------------------------------------------

export async function setApprove(
    workspace: string,
    repo: string,
    id: number,
    on: boolean,
): Promise<ReviewState> {
    const method: Method = on ? 'POST' : 'DELETE';
    return (await request<{my_state: ReviewState}>(method, `${prPath(workspace, repo, id)}/approve`))
        .data.my_state;
}

export async function setRequestChanges(
    workspace: string,
    repo: string,
    id: number,
    on: boolean,
): Promise<ReviewState> {
    const method: Method = on ? 'POST' : 'DELETE';
    return (
        await request<{my_state: ReviewState}>(
            method,
            `${prPath(workspace, repo, id)}/request-changes`,
        )
    ).data.my_state;
}

// --- viewed files -----------------------------------------------------------

export async function getViewed(
    workspace: string,
    repo: string,
    id: number,
): Promise<ViewedState> {
    return (await request<ViewedState>('GET', `${prPath(workspace, repo, id)}/viewed`)).data;
}

export async function putViewed(
    workspace: string,
    repo: string,
    id: number,
    state: ViewedState,
): Promise<ViewedState> {
    return (await request<ViewedState>('PUT', `${prPath(workspace, repo, id)}/viewed`, state)).data;
}

// --- raw file content -------------------------------------------------------

export async function getFile(
    workspace: string,
    repo: string,
    commit: string,
    path: string,
): Promise<string> {
    return (
        await request<string>(
            'GET',
            `${BASE}/file/${enc(workspace)}/${enc(repo)}/${enc(commit)}/${encPath(path)}`,
        )
    ).data;
}
