# Frontend Prototype (static review)

Static-only mock of the entire operator dashboard. **No** build step and **no**
API calls — review it in a browser, approve, then port 1:1 to
`apps/web` (Next.js + shadcn/ui) and wire it up with the real endpoints.

## Usage

```sh
# open the landing page in any browser
open index.html

# or serve it via http (better for relative assets)
python3 -m http.server 24082
# then browse http://localhost:24082
```

## Screens

| File | Route to port | Phase ticket |
| --- | --- | --- |
| `login.html` | `/(auth)/login` | P0-06 |
| `dashboard.html` | `/(dashboard)` | P0-08 |
| `workers.html` | `/(dashboard)/workers` · container grid + live ticker + create-container dialog | P1-19 |
| `accounts.html` | `/(dashboard)/accounts` · roster table + bulk ops | P1-18 |
| `add-account.html` | `/(dashboard)/accounts` · Add Account modal (3 steps) | P1-15 |
| `bulk-import.html` | `/(dashboard)/accounts/bulk` · CSV upload, validation, challenge queue | P1-15 |
| `actions.html` | `/(dashboard)/actions` · job queue + detail drawer | P3-03 |
| `templates.html` | `/(dashboard)/templates` · composer + variables + banned-words detector + preview | P1-15 |
| `monitoring.html` | `/(dashboard)/monitoring` · reach/views/mentions/live metrics | P2-08 |
| `audit.html` | `/(dashboard)/audit` | P1-16 |
| `settings.html` | `/(dashboard)/settings` | P1-16 |

## What this covers

- Shell (sidebar nav + topbar + `⌘K`) — `shell.js`
- Dark (default) / light tokens from `apps/web/app/globals.css`
- shadcn/ui-style components: button variants, card, badge variants (success / warning / destructive / info / outline), table, input, textarea, select, dialog (`<dialog>`), drawer (static aside), skeleton shimmer, status dot, stepper
- Status colors from DESIGN SYSTEM §4.1: cold / ready / busy / error
- Operator console layout pattern: filters → auto-fit grid + right-hand SSE ticker column

## What this excludes (intentional)

- No charts / no real time-series
- No `LiveBrowserModal` noVNC viewport
- No virtualisation (`@tanstack/react-virtual`) — not relevant to visual review
- No role-visibility differences (mock is owned by `owner`)
- No form validation / focus management — Radix's behavior comes for free from real shadcn/ui

## Review flow

After visual approval in each row, mark ✅ in this file. Approved screens become
tickets (already tracked, `tickets/p1_*.md`) and are ported one commit at a
time against OpenAPI + SSE.
