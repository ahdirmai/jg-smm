# UI/UX Audit + Improvement Plan

> Audit date: 2026-09-23 · Scope: semua page dashboard (`/`, workers, accounts, actions, scrape,
> templates, monitoring + per-platform, reports, audit, settings, login) + shared shell + `packages/ui`.
> Reference: Homies Lab design system (cream canvas, single yellow accent, ink as second accent,
> generous radius, hero numbers, soft shadows).

## 1. Findings (before redesign)

### F-01 Duplicate page titles — inconsistency, double hierarchy

`/templates`, `/reports`, `/monitoring`, `/monitoring/[platform]` each render their own `<h1>` +
subtitle, while the shell header (`h-14`, `TITLES` map) already renders title + subtitle for every
route. Result: two titles stacked, inconsistent sizing (`text-3xl` on templates/reports vs
`text-2xl` on monitoring), and a subtitle that can drift from the shell's.

**Fix applied:** removed the in-page header from all four; the shell header is the single source
of page copy. Each page now opens with an actions row (buttons only) or straight into content.

### F-02 KPI cards had no hierarchy

`KpiCard` variants across pages put the label above the value, both small, icon floating
top-right. Numbers were not heroes — nothing led the eye.

**Fix applied:** value is now the hero (`text-2xl/3xl font-semibold tabular-nums`), label is a
small muted line beneath it, and the icon sits in a soft rounded square (`bg-secondary`,
principle 6). Applied to: `/`, `/workers`, `/monitoring`, `/monitoring/[platform]`.

### F-03 Status badges fought the accent

`Badge` variants `success`/`warning`/`info`/`destructive` used **saturated fills** (solid green,
solid yellow, solid red). On a page with a yellow primary CTA, a solid-yellow "warning" pill and a
solid-green "ACTIVE" pill compete with the brand accent — principle 1 (one saturated hue) violated
on nearly every table row.

**Fix applied:** status pills are now soft-tinted (`bg-<status>/12`–`/16`) with an ink or
destructive label. Yellow stays reserved for primary actions.

### F-04 Hardcoded colors broke theming

`text-emerald-600 dark:text-emerald-400` was sprinkled across workers, reports, actions, and the
wizard — green that does not track `--success` and would not follow a palette change.

**Fix applied:** replaced with `text-success` (workers live ticker + provision log + reports
Succeeded columns + action result spans).

### F-05 Inconsistent radius scale

Mixed `rounded-md` (6px default) on cards, tables, inputs, thumbnails, dialogs — no scale, and
nothing matched the reference's generous corners. `--radius` was `0.5rem` with no xl step.

**Fix applied:** token scale `--radius-sm 8 / md 12 / lg 16 / xl 24`; `Card` → `rounded-lg` +
`shadow-card`; tables/thumbnails/previews → `rounded-lg`; empty states → dashed
`rounded-lg border`. Control-internal radius (buttons, mode-toggle) left at `rounded-md` for
small-control density.

### F-06 Old palette (indigo on charcoal)

Light theme was off-white + indigo; dark was indigo on charcoal — no relation to the reference.
`.dark` held a completely different hue family from `.light`.

**Fix applied:** both themes are now Homies Lab — warm cream light / warm near-black dark, single
yellow accent in both, ink-on-yellow text preserved. `docs/DESIGN_SYSTEM.md` §3/§4 updated to match
shipped tokens.

### F-07 Active nav state was gray-tinted

Sidebar active item used `bg-primary/12 text-primary` (a faint yellow tint). It did not read as
"selected" — principle 5 says active/selected is solid ink (black), never gray.

**Fix applied:** active nav leaf and nested group item are now `bg-foreground text-background`
(solid ink pill). Settings tab active state follows the same rule.

### A-01 Account rows had no direct action — only a menu

`/accounts` rows offered a kebab menu (Log in / Pause / Remove) and nothing else. The two things
an operator actually wants per row depend on login state, and neither was surfaced: during a login
the noVNC live view (to watch or drive the challenge), and once authenticated the public profile.
Both required knowing the container, the port, and navigating away.

**Fix applied:** the row's primary cell is now state-driven.

- `AUTHENTICATING` / `NEEDS_INPUT` → **Live view** button opening `LiveBrowserModal` on that
  account's worker noVNC URL. The URL is resolved through the containers list
  (`novncByWorker`, keyed by `Container.id` == the account's packed `workerId`), never guessed;
  a worker that has not published a heartbeat renders a disabled button with a tooltip naming the
  reason instead of a dead link.
- `AUTHENTICATED` (and every other state) → **Profile** link to the derived public profile URL.
  Worker accounts carry no `profileUrl` in the contract (that field is official-account analytics
  only), so the URL is derived from `platform` + `handle || username`.
- The menu keeps its ops, and additionally exposes **Live view** for an authenticated account, so
  an operator can still watch a running session without starting a new login.

This also lands the same realtime screen viewing `/workers` already had (per-container
`LiveBrowserModal`) onto `/accounts`.

**Refined (A-01b):** the buttons were still an extra hop — a click on a button in a cell, not the
row. The row itself is now the action. Anywhere on the row opens the live view or profile, per
login state; the icon buttons are kept only as affordances (and stop propagation so they do not
double-fire). A worker with no published heartbeat shows no disabled button at all — there is
nothing to open, so the row does nothing rather than presenting a dead control. Secondary line of
the Account cell now carries the handle, or the container when the handle equals the username
(previously the container id repeated identically in two columns). Header subtitle states the
click affordance.

## 2. Component-level changes

| File | Change |
| --- | --- |
| `packages/ui/src/components/card.tsx` | `rounded-lg border-border/60 shadow-card` (float, no harsh border) |
| `packages/ui/src/components/badge.tsx` | status variants → soft tint + ink label; added `gap-1.5` for status-dot pairing |
| `packages/ui/src/components/table.tsx` | `TableHead` → `text-[11px] uppercase tracking-wider h-11`; row border `border-border/60`; hover `bg-secondary/60` |
| `apps/web/app/globals.css` | Homies Lab `.light` + `.dark` tokens; radius scale + `--shadow-card` / `--shadow-popover` |
| `apps/web/components/dashboard-shell.tsx` | active nav = solid ink pill; logo mark `rounded-lg`; header hairline + subtle blur; sidebar/hairline borders `border-border/60` |

## 3. Page-by-page

| Page | Change |
| --- | --- |
| `/` | KPI hero cards + icon squares; getting-started step chips → soft square; trend/empty boxes hairline |
| `/workers` | stat cards → hero; provision log + session dialogs hairline; live dot `bg-success`; raw `<select>` → `rounded-lg` |
| `/accounts` | bulk bar + table wrap + error banner hairline radius; row action is now one-click by login state (see A-01) |
| `/actions` | queue table hairline + uppercase group headers; dialog comment/error boxes; dashed empty state |
| `/actions` (new-action-form) | scrape preview → `bg-secondary/30` panel; error/result radius |
| `/actions` (scrape-history) | card-title icon in soft square; thumbnails `rounded-lg`; rows hairline |
| `/scrape` | keyword result rows hairline; raw `<select>` → `rounded-lg` |
| `/templates` | removed duplicate h1; error + table wrap hairline |
| `/monitoring` | removed duplicate h1; KPI hero; table wrapped |
| `/monitoring/[platform]` | removed duplicate h1; platform pill switcher kept; KPI hero + icon squares; monitored list hairline dividers; table wrapped |
| `/reports` | removed duplicate h1; error banner radius; `text-emerald` → `text-success` |
| `/audit` | empty-state icon → soft rounded square (`rounded-xl bg-secondary`) |
| `/settings` | tab active = solid ink (principle 5); already token-clean |
| `/login` | logo mark already `rounded-lg`; unchanged (token-driven) |

## 4. Remaining plan

Ordered by impact. Each is independently shippable. ✅ = done in the follow-up commit.

| # | Item | Why | Where |
| --- | --- | --- | --- |
| P-01 | **Pill tab switcher primitive** | Reference uses a segmented pill control for views; settings tabs + report-kind selector + platform switcher are all button-rows today. A `Tabs`-based pill in `packages/ui` unifies them and gives the reference's inset active pill. | `packages/ui/src/components/tabs.tsx` (new); settings, reports, monitoring/[platform] |
| P-02 | **Bar chart with rounded-top bars** | Reference's "Employment Status" chart has rounded-top bars + floating percentage chips. `TrendPlaceholder`/`TrendChart` are sparklines only — no categorical bar chart exists. | `components/trend-chart.tsx` or new `components/bar-chart.tsx` |
| P-03 ✅ | **Density toggle was a dead control** | Settings → Appearance showed two permanently-disabled "Comfortable/Compact" buttons. Removed (no write surface, no request) rather than left as a fake control. | settings page |
| P-04 ✅ | **⌘K search box was a dummy** | Header search field was a static span ("Search accounts, jobs… ⌘K") with no handler and no `Command` behind it. Removed. | dashboard-shell |
| P-05 ✅ | **`Filter` buttons were inert** | Dashboard "Last 30 days" button and platform-analytics "Filter" rendered as buttons with no handler; the latter needs a per-post endpoint that does not exist. "Last 30 days" is now a plain muted label; the analytics Filter button is removed. | page.tsx, monitoring/[platform] |
| P-06 | **Bounce/skeleton loading** | Most pages render "Loading…" text or `—` placeholders. Reference polish level wants `Skeleton` cards (shadcn `Skeleton`) matching the card grid, so load states do not jump layout. | packages/ui + all list pages |
| P-07 | **Toast/feedback layer missing** | `docs/DESIGN_SYSTEM.md §5.1` documents sonner toasts (default/warning/critical); nothing is installed. Enqueue success/failure currently renders an inline `<span>`. Adds a real feedback channel for the scrape + queue flows. | packages/ui + actions pages |
| P-08 ✅ | **Mobile nav absent** | Sidebar was `hidden md:flex`; below `md` there was **no navigation at all**. Added a hamburger in the header that opens a fixed overlay drawer reusing the same `NavTree` (closes on navigation). Desktop sidebar untouched. | dashboard-shell |
| P-09 | **Status = dot + label pairing** | Principle 7: status should be icon+label+dot. `Badge` now has `gap-1.5` ready for it, but no `StatusDot` component is wired into tables yet. | packages/ui |
| P-10 ✅ | **Audience mix was sample data** | `monitoring/[platform]` rendered a hardcoded `AUDIENCE_MIX` with a `sample` tag. Panel + constant removed — the geo split endpoint does not exist, and no example numbers are shown as data of any kind. | monitoring/[platform] |

## 5. Verification

- `pnpm --filter @smm/web exec tsc --noEmit` — clean
- `pnpm --filter @smm/web exec eslint app components` — clean
- `pnpm --filter @smm/web build` — all 15 routes build
- `python3 scripts/check_docs_links.py` — all 828 cross-references resolve

## 6. Design principles honored

1. Warm cream base + single yellow accent — no second saturated hue (status pills are tints now).
2. Generous radius — 16px cards, 24px container, 12px inputs, 8px pills.
3. High whitespace, soft shadow cards, hairline borders.
4. Numbers are heroes — big, bold, ink, label below.
5. Ink (black) is the second accent — active nav, active settings tab.
6. Small monochrome icons in soft rounded squares.
