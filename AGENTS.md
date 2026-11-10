# bb-review — agent notes

A local web app for reviewing Bitbucket Cloud pull requests: fast diffs, keyboard-first,
usable on a phone (via `tailscale serve`). A single Go binary embeds the React SPA.

## Layout

- `cmd/bb-review/` — main package (flags, wiring, `embed` of `web/dist`)
- `internal/bitbucket/` — Bitbucket Cloud REST 2.0 client
- `internal/server/` — HTTP handlers, security middleware, SPA serving
- `internal/store/` — local JSON state (drafts, viewed files)
- `internal/config/` — config loading
- `web/` — React + Vite + TypeScript SPA. Build output lands in `web/dist` (embedded).
- `docs/API.md` — **the backend ↔ frontend contract. Follow it exactly.** If it must change, say so in your STATUS.

## Conventions

- Early-return style everywhere. No nested if/else pyramids.
- Go: stdlib `net/http` (1.22+ pattern routing), no web framework. `log/slog` for logs.
  Small focused packages, table-driven tests, `httptest` for the Bitbucket client.
- Frontend: React 18 + TS strict, Vite, plain CSS with CSS variables (light + dark via
  `prefers-color-scheme`). Mobile-first: works at 360px width.
- Never log or return the API token. Never commit `config.json`, tokens, or real
  workspace data (this repo is public).

## Config

`~/.config/bb-review/config.json` (mode 0600), overridable with `-config`:

```json
{
  "email": "you@example.com",
  "token": "ATATT...",                        // or token_file, or env BB_REVIEW_TOKEN
  "token_file": "~/.config/bb-review/token",  // optional, mode 0600
  "workspace": "myworkspace",
  "repos": ["repo-a", "other/repo-b"],        // optional "always check" list ("repo" or "ws/repo")
  "listen": "127.0.0.1:7777",
  "allowed_hosts": ["mybox.tail1234.ts.net"],
  "active_days": 14,                          // repo auto-discovery window
  "scan_interval": "60s",                     // reviewer scan tick
  "full_sweep_interval": "6h",                // re-query every active repo this often
  "hot_interval": "5m",                       // re-check a repo with open review PRs this often
  "request_budget": 800                       // upstream requests/hour ceiling
}
```

Token precedence: `BB_REVIEW_TOKEN` env > `token` > `token_file` (whitespace trimmed).
`token_file` may hold the bare token or env style (`BB_REVIEW_TOKEN=...`, optional `export`
and quotes; `#` comments and blank lines are ignored).

`repos` is optional: it is an "always check" list. The reviewer list is maintained by an
incremental scanner (`internal/scanner`): each tick (default 60 s) re-reads discovery and
queries only new/changed repos, hot repos whose `hot_interval` (default 5 min) elapsed, and
`repos`; a full sweep of every active repo happens every `full_sweep_interval` (default 6 h).
The client counts upstream requests over a rolling hour; when the count exceeds
`request_budget` (default 800) a tick skips hot re-checks and full sweeps. List rows get
`build_state` from a per-commit cache (terminal states kept, `INPROGRESS`/`NONE` refreshed at
most every 2 min). Requests are served from scanner state, which is persisted in the state
file under `"scanner"`. `POST /api/scan` forces a tick (debounced 10 s); `GET /api/stats`
reports `upstream_last_hour` and scanner health. The author list uses the workspace-wide
endpoint, so it never fans out per repo.

State (drafts, viewed): `~/.local/share/bb-review/state.json`.

## Commands

- `make build` — builds web then Go binary to `bin/bb-review`
- `make test` — `go test ./...` and `npm --prefix web test`
- `make dev` — Go server on :7777 + Vite dev server (proxies `/api` to :7777)
