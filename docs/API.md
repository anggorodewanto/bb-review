# bb-review HTTP API (backend ↔ frontend contract)

All endpoints are JSON unless noted. Base path `/api`. The Go server proxies the
Bitbucket Cloud REST API 2.0 and normalizes responses into the shapes below.

`{ws}` = workspace slug, `{repo}` = repo slug, `{id}` = PR id (int).
PR prefix: `/api/prs/{ws}/{repo}/{id}`.

## Security rules (server enforces)

- Every non-GET request must carry header `X-BBR: 1`; otherwise `403`. (CSRF guard:
  browsers can't send custom headers cross-origin without a CORS preflight, and the
  server never answers CORS preflights.)
- `Host` header must be in the allowlist (`localhost`, `127.0.0.1`, `[::1]` with any
  port, plus `config.allowed_hosts`, e.g. the tailnet name `mybox.tail1234.ts.net`).
  Otherwise `421`. (DNS-rebinding guard.)
- Server binds `127.0.0.1` only. Phone access is via `tailscale serve`.
- Every response also carries `X-Content-Type-Options: nosniff`,
  `Referrer-Policy: no-referrer` and this CSP:

  ```
  default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' https: data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'
  ```

## Errors

Non-2xx responses: `{"error": "human readable message", "status": <upstream status or 0>}`.

## Types

```ts
type User = { uuid: string; account_id: string; display_name: string; nickname: string; avatar_url: string };

type ReviewState = "approved" | "changes_requested" | "none";

type BuildState = "SUCCESSFUL" | "FAILED" | "INPROGRESS" | "STOPPED" | "NONE";

type PRSummary = {
  workspace: string; repo: string; id: number;
  title: string; author: User; state: "OPEN" | "MERGED" | "DECLINED" | "SUPERSEDED";
  source_branch: string; dest_branch: string;
  created_on: string; updated_on: string;        // RFC3339
  comment_count: number; task_count: number;
  my_state: ReviewState;                          // current user's review state
  approvals: number;                              // count of participants approved
  build_state: BuildState;                        // aggregate of commit statuses on source commit
  url: string;                                    // bitbucket.org html link
};

type Participant = { user: User; role: "REVIEWER" | "PARTICIPANT"; state: ReviewState };

type BuildStatus = { key: string; name: string; state: BuildState; url: string };

type PRDetail = PRSummary & {
  description_raw: string;
  description_html: string;                       // Bitbucket-rendered HTML
  participants: Participant[];
  source_commit: string;                          // full hash
  dest_commit: string;
  merge_base: string;                             // full hash; "old" side of the diff
  builds: BuildStatus[];
};

type InlineAnchor = { path: string; from: number | null; to: number | null };
// from = line in old file (merge_base side), to = line in new file (source side).
// Exactly one of from/to is set for a line comment (to for added/context lines, from for removed lines).

type Comment = {
  id: number; parent_id: number | null;
  author: User; raw: string; html: string;
  created_on: string; updated_on: string;
  inline: InlineAnchor | null;                    // null = general PR comment
  deleted: boolean;
  outdated: boolean;                              // Bitbucket "outdated" flag on inline comments
};

type Draft = {
  id: string;                                     // server-generated uuid
  raw: string;
  inline: InlineAnchor | null;
  parent_id: number | null;                       // reply to existing comment
  created_on: string;
};

type ViewedState = { files: Record<string, string> }; // path -> hash of that file's diff text when marked viewed

type PRRef = { workspace: string; repo: string; id: number };   // GET /api/resolve
```

Pull request list responses carry four optional headers:

| Header | Meaning |
|---|---|
| `X-BBR-Fetched-At` | RFC3339 timestamp of the data behind this list ("updated 30s ago") — for `reviewer` it is the last completed scan tick |
| `X-BBR-Partial` | comma-separated repos (`repo` or `ws/repo`) that failed in the last tick; the rest is still returned |
| `X-BBR-Rate-Limited` | `1` when Bitbucket answered `429` and the scanner is backing off |
| `X-BBR-Scanning` | `done/total` while the first full scan sweep is still running (results so far are returned) |

## Endpoints

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/me` | | `User` |
| GET | `/api/prs?role=reviewer\|author&state=OPEN` | | `PRSummary[]` sorted by `updated_on` desc. `reviewer`: served immediately from the scanner state (union of the per-repo `open_review_prs`); never blocks on Bitbucket, and the scanner only tracks OPEN pull requests (the `state` parameter applies to `author`). `author`: one workspace-wide query, no fan-out. See headers above. |
| POST | `/api/scan` | | `{started: boolean}` — triggers an immediate reviewer scan tick in the background. Debounced: `started` is `false` when a tick is running or one started less than 10 s ago. |
| GET | `{pr}` | | `PRDetail` |
| GET | `{pr}/diff` | | `text/plain` unified git diff (Bitbucket `/diff`, redirects followed) |
| GET | `{pr}/comments` | | `Comment[]` (all pages, oldest first) |
| POST | `{pr}/comments` | `{raw, inline?, parent_id?}` | `Comment` (posted immediately) |
| GET | `{pr}/drafts` | | `Draft[]` |
| POST | `{pr}/drafts` | `{raw, inline?, parent_id?}` | `Draft` |
| PUT | `{pr}/drafts/{draftId}` | `{raw}` | `Draft` |
| DELETE | `{pr}/drafts/{draftId}` | | `204` |
| POST | `{pr}/drafts/submit` | `{action?: "approve"\|"request_changes"\|null}` | `{posted: Comment[], failed: {draft: Draft, error: string}[]}` — posts drafts in order, deletes posted ones, then applies action |
| POST | `{pr}/approve` | | `{my_state}` |
| DELETE | `{pr}/approve` | | `{my_state}` |
| POST | `{pr}/request-changes` | | `{my_state}` |
| DELETE | `{pr}/request-changes` | | `{my_state}` |
| GET | `{pr}/viewed` | | `ViewedState` |
| PUT | `{pr}/viewed` | `ViewedState` | `ViewedState` |
| GET | `/api/file/{ws}/{repo}/{commit}/{path...}` | | `text/plain` raw file content at commit (for expanding diff context). `404` if missing. Files > 2 MB → `413`. |
| GET | `/api/resolve?url=<bitbucket pr url>` | | `PRRef`. Accepts `https://bitbucket.org/{ws}/{repo}/pull-requests/{id}[/anything][?query][#frag]`; `400` for anything else. Pure parsing, no upstream call. |

Anything not under `/api/` serves the embedded SPA (`index.html` fallback for client routes).

## Bitbucket upstream mapping (for the backend)

- Auth: HTTP Basic, username = Atlassian account email, password = Atlassian API token
  (scopes: `read:user:bitbucket`, `read:pullrequest:bitbucket`, `write:pullrequest:bitbucket`,
  `read:repository:bitbucket`). App passwords are deprecated; don't support them.
- `GET /2.0/user` → me.
- PR list, role=reviewer: there is no cross-repo "PRs I review" endpoint, so an
  incremental scanner owns this list (`internal/scanner`, one background loop, tick every
  `scan_interval`, default 60 s):
  - every tick re-reads discovery:
    `GET /2.0/repositories/{ws}?sort=-updated_on&pagelen=100&fields=next,values.slug,values.updated_on`,
    paging only while `updated_on` is inside `active_days` (default 14) — 1-4 requests;
  - the tick queries a repo only when it was never scanned, its `updated_on` changed since
    the last scan, it still has open review PRs ("hot"), or it is listed in `config.repos`;
    every `full_sweep_interval` (default 6 h), or when no state exists yet, every active repo
    is queried. Concurrency is 4;
  - a repo query is
    `GET /2.0/repositories/{ws}/{repo}/pullrequests?state=OPEN&q=reviewers.uuid="{uuid}"`,
    `pagelen=50`, `fields=+values.participants`;
  - repos that leave the active window keep their PRs only while those are non-empty, and
    keep being re-checked ("hot") until they drain;
  - on `429` the tick stops early (partial results are kept), and the scanner backs off for
    `Retry-After` or 60 s, doubling up to 10 min;
  - state is persisted after each tick under `"scanner"` in the state file: per repo
    `last_scanned_updated_on`, `last_scanned_at`, `open_review_prs`, plus
    `last_full_sweep_at` (and `last_tick_at`).
  `GET /api/prs?role=reviewer` returns that state immediately; `POST /api/scan` starts a
  tick on demand, and any write on a PR (comment, approve, request changes, submit)
  re-queries that one repo.
- PR list, role=author: one call,
  `GET /2.0/workspaces/{ws}/pullrequests/{uuid}?state=OPEN&pagelen=50&fields=+values.participants`
  (the uuid is URL-encoded, braces included). Pagination followed.
- Detail: `GET .../pullrequests/{id}`, `GET .../pullrequests/{id}/merge-base`,
  `GET /2.0/repositories/{ws}/{repo}/commit/{source_commit}/statuses`.
- Diff: `GET .../pullrequests/{id}/diff` (302 → follow).
- Comments: `GET .../pullrequests/{id}/comments?pagelen=100` (follow `next`).
  Post: `POST .../comments` with `{"content":{"raw":...},"inline":{"path":...,"to":N}|{"path":...,"from":N},"parent":{"id":N}}`.
- Approve: `POST|DELETE .../approve`. Request changes: `POST|DELETE .../request-changes`.
- File: `GET /2.0/repositories/{ws}/{repo}/src/{commit}/{path}`.
