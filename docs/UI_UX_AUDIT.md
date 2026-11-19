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

### F-03 — Fake or ambiguous data on the overview

The overview drew a decorative trend sketch, reported zero likes/reports/replies
that were not measured, and labelled a link to another page “Export CSV”.

**Implemented:** queue, failure, fleet, and authentication numbers now come from
the live hooks. The analytics KPIs stay as an em dash until the API returns a
snapshot. The trend card explains the empty state and links to Reports. Queue
health counts actions by their real `actionType`. “Open reports” is the label.

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
