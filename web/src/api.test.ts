import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest';
import * as api from './api';

type FakeInit = {
    status?: number;
    headers?: Record<string, string>;
    text?: string;
};

function respond(body: unknown, init: FakeInit = {}): Response {
    const status = init.status ?? 200;
    const headers = new Map(
        Object.entries({'content-type': 'application/json', ...(init.headers ?? {})}).map(([k, v]) => [
            k.toLowerCase(),
            v,
        ]),
    );
    const isJson = headers.get('content-type')?.includes('application/json') ?? true;
    return {
        ok: status >= 200 && status < 300,
        status,
        statusText: status === 204 ? 'No Content' : 'OK',
        headers: {get: (name: string) => headers.get(name.toLowerCase()) ?? null},
        json: async () => body,
        text: async () => init.text ?? (isJson ? JSON.stringify(body) : String(body)),
    } as unknown as Response;
}

const fetchMock = vi.fn();

beforeEach(() => {
    fetchMock.mockReset();
    vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => {
    vi.unstubAllGlobals();
});

function lastCall(): {url: string; init: RequestInit} {
    const call = fetchMock.mock.calls.at(-1);
    if (!call) {
        throw new Error('fetch was not called');
    }
    return {url: String(call[0]), init: call[1] as RequestInit};
}

function headerOf(init: RequestInit, name: string): string | undefined {
    return (init.headers as Record<string, string> | undefined)?.[name];
}

describe('api request headers', () => {
    it('does not send X-BBR on GET', async () => {
        fetchMock.mockResolvedValueOnce(respond([]));
        await api.listPRs('reviewer');
        const {url, init} = lastCall();
        expect(url).toBe('/api/prs?role=reviewer&state=OPEN');
        expect(init.method).toBe('GET');
        expect(headerOf(init, 'X-BBR')).toBeUndefined();
    });

    it('sends X-BBR on POST, PUT and DELETE', async () => {
        fetchMock.mockResolvedValue(respond({id: 'd1', raw: 'x', inline: null, parent_id: null, created_on: ''}));
        await api.createDraft('ws', 'repo', 1, {raw: 'hello'});
        expect(headerOf(lastCall().init, 'X-BBR')).toBe('1');

        await api.updateDraft('ws', 'repo', 1, 'd1', 'edited');
        expect(headerOf(lastCall().init, 'X-BBR')).toBe('1');
        expect(lastCall().init.method).toBe('PUT');
        expect(lastCall().init.body).toBe(JSON.stringify({raw: 'edited'}));

        fetchMock.mockResolvedValueOnce(respond(undefined, {status: 204}));
        await api.deleteDraft('ws', 'repo', 1, 'd1');
        expect(headerOf(lastCall().init, 'X-BBR')).toBe('1');
        expect(lastCall().init.method).toBe('DELETE');
    });

    it('sends a JSON content type with a body', async () => {
        fetchMock.mockResolvedValueOnce(respond({id: 'd1'}));
        await api.createDraft('ws', 'repo', 1, {
            raw: 'hi',
            inline: {path: 'a.ts', from: null, to: 3},
        });
        const {init} = lastCall();
        expect(headerOf(init, 'Content-Type')).toBe('application/json');
        expect(JSON.parse(String(init.body))).toEqual({
            raw: 'hi',
            inline: {path: 'a.ts', from: null, to: 3},
        });
    });

    it('sends X-BBR for review-state changes, both directions', async () => {
        fetchMock.mockResolvedValue(respond({my_state: 'approved'}));
        await expect(api.setApprove('ws', 'repo', 1, true)).resolves.toBe('approved');
        expect(lastCall().url).toBe('/api/prs/ws/repo/1/approve');
        expect(lastCall().init.method).toBe('POST');
        expect(headerOf(lastCall().init, 'X-BBR')).toBe('1');

        await expect(api.setApprove('ws', 'repo', 1, false)).resolves.toBe('approved');
        expect(lastCall().init.method).toBe('DELETE');
        expect(headerOf(lastCall().init, 'X-BBR')).toBe('1');

        await api.setRequestChanges('ws', 'repo', 1, true);
        expect(lastCall().url).toBe('/api/prs/ws/repo/1/request-changes');
        expect(headerOf(lastCall().init, 'X-BBR')).toBe('1');
    });

    it('submits drafts with the requested action', async () => {
        fetchMock.mockResolvedValueOnce(respond({posted: [], failed: []}));
        await api.submitDrafts('ws', 'repo', 7, 'request_changes');
        expect(lastCall().url).toBe('/api/prs/ws/repo/7/drafts/submit');
        expect(JSON.parse(String(lastCall().init.body))).toEqual({action: 'request_changes'});
        expect(headerOf(lastCall().init, 'X-BBR')).toBe('1');
    });
});

describe('api responses', () => {
    it('reports the X-BBR-Partial header', async () => {
        fetchMock.mockResolvedValueOnce(
            respond([], {headers: {'X-BBR-Partial': '1', 'content-type': 'application/json'}}),
        );
        await expect(api.listPRs('author')).resolves.toMatchObject({data: [], partial: true});
    });

    it('reports fetch time and rate limiting headers', async () => {
        const fetchedAt = '2026-11-10T10:00:00Z';
        fetchMock.mockResolvedValueOnce(
            respond([], {
                headers: {
                    'X-BBR-Fetched-At': fetchedAt,
                    'X-BBR-Rate-Limited': '1',
                    'content-type': 'application/json',
                },
            }),
        );
        await expect(api.listPRs('reviewer')).resolves.toEqual({
            data: [],
            partial: false,
            fetchedAt,
            rateLimited: true,
            scanning: null,
        });
    });

    it('reports the X-BBR-Scanning sweep progress', async () => {
        fetchMock.mockResolvedValueOnce(
            respond([], {headers: {'X-BBR-Scanning': '120/337', 'content-type': 'application/json'}}),
        );
        await expect(api.listPRs('reviewer')).resolves.toMatchObject({scanning: '120/337'});
    });

    it('defaults fetchedAt to null and rateLimited to false', async () => {
        fetchMock.mockResolvedValueOnce(respond([]));
        await expect(api.listPRs('reviewer')).resolves.toEqual({
            data: [],
            partial: false,
            fetchedAt: null,
            rateLimited: false,
            scanning: null,
        });
    });

    it('triggers a rescan with POST /api/scan', async () => {
        fetchMock.mockResolvedValueOnce(respond(undefined, {status: 204}));
        await expect(api.scanPRs()).resolves.toBeUndefined();
        const {url, init} = lastCall();
        expect(url).toBe('/api/scan');
        expect(init.method).toBe('POST');
        expect(headerOf(init, 'X-BBR')).toBe('1');
        expect(init.body).toBeUndefined();
    });

    it('surfaces a scan failure', async () => {
        fetchMock.mockResolvedValueOnce(
            respond(
                {error: 'scan already running', status: 409},
                {status: 409, headers: {'content-type': 'application/json'}},
            ),
        );
        await expect(api.scanPRs()).rejects.toMatchObject({
            name: 'ApiError',
            message: 'scan already running',
            status: 409,
        });
    });

    it('resolves a bitbucket PR url', async () => {
        fetchMock.mockResolvedValueOnce(respond({workspace: 'ws', repo: 'repo', id: 42}));
        const url = 'https://bitbucket.org/ws/repo/pull-requests/42';
        await expect(api.resolvePR(url)).resolves.toEqual({workspace: 'ws', repo: 'repo', id: 42});
        const {url: called, init} = lastCall();
        expect(called).toBe(`/api/resolve?url=${encodeURIComponent(url)}`);
        expect(init.method).toBe('GET');
        expect(headerOf(init, 'X-BBR')).toBeUndefined();
    });

    it('surfaces the resolve 400 message', async () => {
        fetchMock.mockResolvedValueOnce(
            respond(
                {error: 'not a pull request url', status: 400},
                {status: 400, headers: {'content-type': 'application/json'}},
            ),
        );
        await expect(api.resolvePR('https://example.com/x')).rejects.toMatchObject({
            name: 'ApiError',
            message: 'not a pull request url',
            status: 400,
        });
    });

    it('returns plain text for diffs', async () => {
        fetchMock.mockResolvedValueOnce(
            respond('diff --git a/x b/x\n', {headers: {'content-type': 'text/plain'}, text: 'diff --git a/x b/x\n'}),
        );
        await expect(api.getDiff('ws', 'repo', 1)).resolves.toBe('diff --git a/x b/x\n');
    });

    it('encodes file paths but keeps slashes', async () => {
        fetchMock.mockResolvedValueOnce(
            respond('code', {headers: {'content-type': 'text/plain'}, text: 'code'}),
        );
        await api.getFile('ws', 'repo', 'abc123', 'src/a b.ts');
        expect(lastCall().url).toBe('/api/file/ws/repo/abc123/src/a%20b.ts');
        expect(headerOf(lastCall().init, 'X-BBR')).toBeUndefined();
    });

    it('throws ApiError with the server message', async () => {
        fetchMock.mockResolvedValueOnce(
            respond(
                {error: 'boom', status: 502},
                {status: 502, headers: {'content-type': 'application/json'}},
            ),
        );
        await expect(api.getPR('ws', 'repo', 1)).rejects.toMatchObject({
            name: 'ApiError',
            message: 'boom',
            status: 502,
        });
    });

    it('returns undefined for 204', async () => {
        fetchMock.mockResolvedValueOnce(respond(undefined, {status: 204}));
        await expect(api.deleteDraft('ws', 'repo', 1, 'x')).resolves.toBeUndefined();
    });
});
