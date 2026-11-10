# bb-review

A keyboard-first, mobile-friendly review UI for Bitbucket Cloud pull requests.
One Go binary with the React app embedded. It runs on your machine, so your API token never leaves it.

- **Inbox:** PRs you review and PRs you wrote, across the whole workspace, with build status and your review state.
- **Diffs:** split or unified, syntax highlighted, wrapped lines, expandable context, a file tree with "viewed" marks that reset when a file changes.
- **Reviews:** batch draft comments and submit them in one go as Comment, Approve or Request changes.
- **Keyboard:** `j/k` files, `n/p` hunks, `c` comment, `v` viewed, `a` approve, `s` submit, `w` wrap, `o` open a PR link, `?` help.
- **Phone:** responsive layout, can be added to the home screen, reachable through `tailscale serve`.

## Setup

1. Create an Atlassian API token with scopes for Bitbucket at
   <https://id.atlassian.com/manage-profile/security/api-tokens>:
   `read:user:bitbucket`, `read:pullrequest:bitbucket`, `write:pullrequest:bitbucket`, `read:repository:bitbucket`.
2. Write `~/.config/bb-review/config.json` (mode `600`):

   ```json
   {
     "email": "you@example.com",
     "token_file": "~/.config/bb-review/token",
     "workspace": "your-workspace"
   }
   ```

   See [AGENTS.md](AGENTS.md#config) for every option.
3. Build and run (needs Go 1.22+ and Node 20+):

   ```sh
   make build
   ./bin/bb-review
   ```

   Open <http://localhost:7777>.

## Phone access with Tailscale

```sh
tailscale serve --bg --https=443 http://127.0.0.1:7777
```

Add your tailnet hostname (e.g. `mybox.tail1234.ts.net`) to `allowed_hosts` in the config, then open
`https://mybox.tail1234.ts.net` on your phone.

## How the "To review" list works

Bitbucket has no workspace-wide "PRs I review" endpoint. bb-review finds repos updated in the last
`active_days` (default 14), scans them once, then rechecks only repos that changed. It also rechecks
repos with open reviews every 5 minutes and does a full sweep every 6 hours. Upstream requests
stay well under Bitbucket's rate limits; `GET /api/stats` shows the count.
