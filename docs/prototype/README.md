# Frontend Prototype (static review)

Static-only mock of the entire operator dashboard. **No** build step and **no**
API calls — review it in a browser, approve, then port 1:1 to
`apps/web` (Next.js + shadcn/ui) and wire it up with the real endpoints.

## Usage

```sh
# open the landing page in any browser
open index.html

# or serve it via http (better for relative assets)
pnpm run proto:serve        # python3 -m http.server 24085 --directory docs/prototype
# then browse http://localhost:24085
```

### Automated checks (real browser)

```sh
pnpm run proto:serve   # in one shell
pnpm run proto:verify  # renders every page, asserts CSS + shell + console
pnpm run proto:shots   # writes downscaled screenshots to docs/prototype/_shots
```

The screenshot tool accepts explicit targets or a full sweep:

```sh
node docs/prototype/shots.mjs dashboard.html:light actions.html:dark
node docs/prototype/shots.mjs --all   # every page in both themes (28 shots)
```

`verify.mjs` loads each page in headless Chromium, toggles dark↔light, and
asserts that the body background **actually changes** (i.e. the Tailwind Play
CDN mapped the design tokens and the theme is not locked). It fails on any
non-favicon HTTP error or page error. Set `CHROME_PATH` to override the browser
binary; otherwise it auto-discovers one from the Playwright cache.

## Screens

| File               | Route to port                                                                      | Phase ticket |
| ------------------ | ---------------------------------------------------------------------------------- | ------------ |
| `login.html`       | `/(auth)/login`                                                                    | P0-06        |
| `dashboard.html`   | `/(dashboard)` · Strategist home: KPI strip, trend, top automation, top posts      | P0-08        |
| `workers.html`     | `/(dashboard)/workers` · container grid + live ticker + create-container dialog    | P1-19        |
| `accounts.html`    | `/(dashboard)/accounts` · worker-account roster + bulk ops                         | P1-18        |
| `add-account.html` | `/(dashboard)/accounts` · Add Account modal (3 steps)                              | P1-15        |
| `bulk-import.html` | `/(dashboard)/accounts/bulk` · CSV upload, validation, challenge queue             | P1-15        |
| `actions.html`     | `/(dashboard)/actions` · action-to-target trigger + live job queue (dummy process) | P3-03        |
| `templates.html`   | `/(dashboard)/templates` · composer + variables + banned-words detector + preview  | P1-15        |
| `monitoring.html`  | `/(dashboard)/monitoring` · Official Accounts overview + per-platform links        | P2-08        |
| `analytics-*.html` | `/(dashboard)/monitoring/<platform>` · reach/views/mentions (7 platforms)          | P2-08        |
| `audit.html`       | `/(dashboard)/audit`                                                               | P1-16        |
| `settings.html`    | `/(dashboard)/settings`                                                            | P1-16        |

**Monitoring group** (nav submenu) holds the overview plus one analytics page per
platform: `analytics-instagram`, `analytics-threads`, `analytics-facebook`,
`analytics-linkedin`, `analytics-x`, `analytics-youtube`, `analytics-tiktok`.

## What this covers

- Shell (sidebar nav + topbar + `⌘K`) — `shell.js`
- **Light + dark mode** — `theme.js` seeds from `localStorage` / `prefers-color-scheme` (dark default), maps the design tokens into the Tailwind Play CDN, and is toggled from a **labeled segmented Light|Dark switch** in the header (shell pages) or the floating switch (login / index). Choice persists per browser and stays in sync across pages via the `smmthemechange` event.
- **Calm palette.** Saturation is deliberately modest — no neon/glow. Light mode uses a soft off-white (`220 20% 97%`) rather than pure white; dark mode a soft charcoal (`222 20% 10%`) rather than near-black. Accent is a muted indigo (`230 45% 48%` light / `230 52% 66%` dark). Status/feedback colors are desaturated too. Intended for long operator shifts by the Social Media Specialist team.
- shadcn/ui-style components: button variants, card, badge variants (success / warning / destructive / info / outline), table, input, textarea, select, dialog (`<dialog>`), drawer (static aside), skeleton shimmer, status dot, stepper
- Status colors from DESIGN SYSTEM §4.1: cold / ready / busy / error
- Operator console layout pattern: filters → auto-fit grid + right-hand SSE ticker column

## Layout contract (all shell pages)

```
header (h-14)  →  main (p-6)  →  #page (space-y-4)
```

Top-level sections carry **no** `mt-*`; vertical rhythm comes from `space-y-4` on
`#page`. Two-column pages use `grid ... gap-4`. Login and the index landing are
the only standalone pages (no shell).

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

## Verification status

Last run: **19/19 pages PASS** (`pnpm run proto:verify`), screenshots refreshed
for all 14 shipped pages in both themes. One real bug was found and fixed during
verification: `actions.html` had `class="dark"` on `<body>`, which locked the
theme tokens so the light/dark toggle did nothing. Theme state belongs on
`<html>` only (set by `theme.js`).

## Design pass log

- **Desaturated palette** (`styles.css`): killed neon tints, softened badges to
  `/0.28` border + `/0.1` fill, removed the stepper-dot glow, kept a single soft
  card shadow in light mode only (dark relies on border contrast).
- **Segmented theme switch** (`theme.js`, `shell.js`, `login.html`, `index.html`):
  explicit Light|Dark control, `aria-pressed` reflects state, synced on change.
- **Active nav** is a primary tint (`hsl(var(--primary) / 0.12)`) with primary
  text, not a plain neutral accent, so the current page reads clearly.
