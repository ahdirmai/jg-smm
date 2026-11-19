# UI/UX Audit — Coastal Glass Redesign

> Audit date: 2026-11-19 · Scope: dashboard shell, overview, accounts, actions,
> scrape, workers, reports, audit, settings, sign-in, shared UI primitives.
> Reference: `../dashboard-smm-new` (glossy minimal, layered cool canvas) and
> ReactBits Spotlight Card + Glare Hover.
> Removed from scope: Monitoring and Templates.

## 1. What changed, and why

### F-01 — The shell was the default sidebar + top bar + four cards

The old shell was the generic dashboard shape: a solid 240px sidebar, a bordered
header, then a page of equal cards. It did not express the calm, atmospheric
reference and it did not prioritise the operator's next action.

**Implemented:** a floating glass rail with labelled sections (Workspace,
Insights, System), a compact header, and a phone dock with Home, Accounts,
Actions, Scrape, and More. The content canvas is the same at every route; only
the page sections differ. The shell is the only strong-blur surface; cards use a
quieter translucent surface, so glass remains a hierarchy signal.

### F-02 — Navigation carried two features the product no longer supports

Monitoring (overview + seven platform pages) and Templates occupied a third of
the nav and produced routes that were no longer part of the approved surface.

**Implemented:** removed both route trees, nav entries, page titles, the
template hook, client template methods, and their e2e specs. Every remaining nav
item points at a real route; the dead-link sweep now enumerates only those pages.

### F-03 — The overview showed analytics before it showed work

The overview drew a decorative trend sketch and a reach/likes/comments KPI strip
that was blank for most of the workspace's life. It also labelled a link to
another page “Export CSV”.

**Implemented:** the overview is now decision-first. It leads with a ranked
attention list (failed actions, accounts needing sign-in, offline workers),
then fleet readiness, account health, platform mix, and the latest real queue
entries. Every count comes from the live hooks; there is no decorative chart or
invented delta. Queue health counts real `actionType` values. “Open reports” is
the label.

### F-09 — Add account left the list

The Accounts page offered “Add account” as a link to a separate route, which
lost the fleet context while an operator added credentials.

**Implemented:** the button opens `AddAccountDialog` over the current list. It
mirrors the full page step for step — explicit Auto/city location, all seven
platforms (including YouTube), username/password/tags, and review — and
refreshes the list on success; closing it returns to the same filtered view.

### F-04 — Low-contrast grey-on-glass risk

The reference is atmospheric; glass can quietly lower contrast, especially for
small labels.

**Implemented:** the light palette uses ink `#1a2c3a` (12.56:1 on the canvas),
muted `#4b5f70` (5.81:1), and white on primary `#28506b` (8.57:1). Dark mode uses
`#dce8f2` on `#0e1a24` (14.15:1) and muted `#a9b8c4` (8.68:1). Input borders use
a stronger `--input` token (3:1 boundary target), while separators stay quiet.
Cards are 70% white / 80% dark rather than fully transparent.

### F-05 — Tables failed at phone width

The accounts table had eight columns and a long opaque handle; the action queue
and scrape history had no shrink boundary. On a 390px viewport the page grew to
886px wide.

**Implemented:** page sections are allowed to shrink (`min-width: 0`); account
columns collapse to Account + Status, with auth/status combined below `md`;
opaque handles fall back to timezone; the queue hides stepper/detail columns on
phones and wraps its header actions; history captions clamp and truncate. The
measured page width is now 390px on every audited route.

### F-06 — Keyboard and touch details

The old mobile drawer had no focus trap, the mobile nav was a bare hamburger,
and several controls were below 44px.

**Implemented:** skip link; focus-trapped drawer with Escape close and labelled
More trigger; 44px nav rows and dock items; `:focus-visible` rings on every
interactive primitive; pointer-coarse-friendly button/input heights; reduced
motion respected. Status is never colour-only: every badge carries text.

### F-08 — The workspace had no availability contract

Workers can hibernate after an idle hour, but the dashboard had no way to tell
the operator why a page or an action was slow. A sleeping fleet looked like a
broken UI.

**Implemented:** `WorkerGate` mirrors the one-hour idle contract in the browser.
It blocks the workspace, explains the state, and offers `Start all workers`.
That calls the new authenticated wake endpoint, then polls the health endpoint
every 2 seconds; the gate unlocks only when **every** worker answers. The modal
lists each worker and its realtime awake/unreachable state, marks the page
`inert`, traps focus, and has no dismiss path. While the workspace is open the
gate does not poll: a probe every few seconds would itself keep the microVMs
awake and defeat the idle policy.

### F-07 — Empty and loading states were silent

Several cards said “No data” or rendered a spinner without context.

**Implemented:** loading copy names what is loading; empty states name the cause
and the next action (“No trend series yet… connect an official account”);
failed queue copy points to Retry/Cancel. The onboarding state remains a
three-step path to the first action.

### F-10 — Actions carried three competing control clusters

The form, scraped history, and queue were stacked in one long page; the queue
also showed disabled Cancel/Run controls when there was nothing to act on, and
status labels used internal names (“PENDING”).

**Implemented:** Actions is now three sections — Create action, Scraped
posts, Job queue — with a count badge for failed work. Queue controls appear
only when relevant: failed jobs show Retry, queued jobs show Run, and the
destructive Cancel sits apart. Status labels are operator language (Queued,
Running, Succeeded, Failed, Cancelled). Rows keep the action type on mobile and
name the executing worker when one was recorded.

### F-11 — Release session looked broken on a stale parked login

`CloseLiveView` returned success when `worker_id` was already NULL, but left
the account `NEEDS_INPUT`; the UI then refreshed to the same row with no
visible change. The backend now settles that state to `FAILED` with a reason,
and the Accounts UI closes the panel and reports “Session released”.

## 2. Verification

- `pnpm --filter @smm/web typecheck` — pass
- `pnpm --filter @smm/web lint` — pass
- `pnpm --filter @smm/web build` — pass; 14 routes (Monitoring/Templates gone)
- Browser pass at 1440×1000 and 390×844 in light and dark themes:
  overview, accounts, actions, scrape, workers, reports, audit, settings, login.
- Worker gate: forced an expired `smm:last-active`, confirmed the blocking
  modal and inert background, clicked Start, and observed the workspace unlock
  after all 11 health probes returned awake.
- No horizontal document overflow on audited routes at 390px (`scrollWidth === clientWidth`).
- Mobile drawer opens, closes on Escape/overlay, and traps Tab; mobile dock stays
  reachable without covering the final row.
- Contrast ratios computed with WCAG 2.x relative luminance (see F-04).

## 3. Remaining limits

- The account list keeps a table on phones (combined status cell) rather than a
  separate card list; at 390px usernames truncate, but the row opens the full
  detail panel. This is a deliberate density trade-off.
- The workers page still exposes the legacy “Add container” path because the
  backend contract exists; in the boxd stateless deployment, fleet creation is
  an operator/API action, not a dashboard action.
- The design uses the platform system font stack (SF Pro / Segoe UI) rather than
  shipping a web font: faster first paint and closer to the reference.
