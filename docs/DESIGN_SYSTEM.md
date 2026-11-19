# Design System

> Base: **shadcn/ui** (Radix + Tailwind v4 + CSS variables). Theme:
> **Coastal Glass** — cool atmospheric canvas, navy ink, one deep-blue accent,
> translucent surfaces. The reference is `../dashboard-smm-new` (glossy minimal).
> Contrast targets WCAG 2.1 AA in both themes.

## 1. Stack

- **shadcn/ui** — source lives in `packages/ui/src/components` (exported by `@smm/ui`).
- **Tailwind CSS v4** — tokens live in `apps/web/app/globals.css`; `@source` scans
  the shared UI package.
- **Radix UI** — accessible primitives (dialogs, menus, selects).
- **lucide-react** — icons at 16–20px, paired with text labels in navigation.
- **next-themes** — `class` dark mode on `<html>`.
- **ReactBits** — `SpotlightCard` and `GlareHover` (adapted in
  `apps/web/components/reactbits`) for pointer and gloss feedback.
- System font stack (SF Pro / Segoe UI / Roboto) — no web-font request.

## 2. Principles

1. **One ink, one accent.** Ink/navy carries hierarchy; the deep blue accent is
   reserved for primary actions, the active route, and link emphasis. Status
   colours are semantic only.
2. **Glass is hierarchy, not a texture.** The shell and the primary dashboard
   surface get the stronger blur. Ordinary cards use a quieter translucent
   surface; the page canvas stays atmospheric and readable.
3. **Numbers are heroes.** Value first, label beneath in a small muted line.
   Do not invent values; show `—` until real data lands.
4. **Atmosphere without noise.** Layered radial light + two soft cloud fields;
   no grid texture, no looping glow, no decorative pulse dots.
5. **Rounded, but deliberate.** Shell 28px, cards 26px, inputs/buttons 12–13px,
   pills full. Not every control is a pill.
6. **Quiet motion.** Hover/press/focus only; ReactBits spotlight follows the
   pointer, glare sweeps on hover, and all of it stops under
   `prefers-reduced-motion`.
7. **Real states.** Empty, loading, and error copy names the cause and the next
   action. Status is text + colour, never colour alone.
8. **The phone is a designed state.** The sidebar becomes a labelled bottom dock
   (Home, Accounts, Actions, Scrape, More); the More drawer traps focus and
   closes on Escape; pages never grow a horizontal scrollbar.

## 3. Tokens

The shipped values are in `apps/web/app/globals.css`. Highlights:

| Token | Light | Dark |
| --- | --- | --- |
| `--background` | `210 36% 94%` mist | `213 28% 8%` near-black blue |
| `--foreground` | `210 34% 15%` ink | `205 30% 92%` |
| `--primary` | `205 48% 30%` deep blue | `202 58% 64%` |
| `--primary-foreground` | `0 0% 100%` | `213 40% 8%` |
| `--muted-foreground` | `210 17% 34%` | `205 14% 70%` |
| `--border` | `210 22% 82%` | `210 14% 25%` |
| `--input` | `210 14% 52%` | `210 12% 52%` |
| `--radius` | `16px`; xl 26px | same |
| `--glass-card` | `rgb(255 255 255 / 0.7)` | `rgb(23 38 50 / 0.8)` |
| `--shadow-card` | soft blue-grey float | soft black float |

Verified pairings: ink on canvas **12.56:1**, muted on canvas **5.81:1**,
white on primary **8.57:1**; dark ink on canvas **14.15:1**, muted **8.68:1**.
Input boundaries use the stronger `--input` token to meet the 3:1 non-text bar.

## 4. Typography

| Use | Class | Notes |
| --- | --- | --- |
| Page title | `text-[19px] md:text-[22px] font-semibold tracking-[-0.025em]` | Owned by the shell |
| Section | `text-sm font-semibold tracking-tight` | Card title |
| Body | `text-sm leading-6` | 14px |
| Small | `text-xs text-muted-foreground` | Labels/help |
| Eyebrow | `text-[11px] font-bold uppercase tracking-[0.14em]` | Use sparingly |
| Mono | `font-mono text-xs` | IDs, logs |
| Hero number | `text-3xl/4xl font-semibold tracking-[-0.05em] tabular-nums` | KPI/fleet |

## 5. Layout

- Shell: fixed 236px glass rail at `>=1024px`, content max-width 1440px.
- Header: title/subtitle + optional global action; no page-level duplicate H1.
- Mobile: 48px targets in a fixed glass dock; content reserves
  `calc(92px + env(safe-area-inset-bottom))`; drawer is 300px/max 86vw.
- Page rhythm: `.page-stack` gap 20px desktop / 14px phone. Direct children get
  `min-width: 0` and `width: 100%`.
- Tables: hide non-decision columns at narrow widths, keep the account/status/
  action columns. Any remaining scroll region is local and labelled.

## 6. Components

| Component | Rule |
| --- | --- |
| `Card` | `.card-surface`: 1px glass border, translucent surface, card shadow, 26px radius. No second strong blur. |
| `Button` | 44px default height (36px `sm`), 13px radius, primary accent with inset top highlight; visible `:focus-visible` ring. |
| `Input` | 44px height, 12px radius, `--input` boundary, white/45 field in light, white/5 in dark. |
| `Table` | Uppercase 11px column heads, hairline row separators; no outer double border beyond its card. |
| `Badge` | Text + soft semantic tint; never a saturated fill that competes with the primary action. |
| `Dialog`/`Select` | Native Radix focus order; dialog closes with Escape; labels always present. |
| ReactBits | Spotlight/Glare only on primary or clickable surfaces; no `loop` mode; reduced motion respected. |

## 7. Worker availability gate

After an hour without interaction, the shell renders a blocking glass modal:
the page behind it is `inert`, the dialog traps focus, and the only way in is
`Start all workers`. The action wakes every worker through the API and the
dialog polls worker health until all report awake. No close button or Escape
path bypasses the check. The gate is intentionally not a background poller;
health probes wake boxd machines, so they run only while starting.

## 8. Removed surfaces

Monitoring and Templates were removed from the dashboard navigation, routes,
page titles, client hooks, API-client methods, and e2e specs. Their backend
endpoints are intentionally retained for data/API compatibility.
