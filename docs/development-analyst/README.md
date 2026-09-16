# Development Analyst — Prototype Parity Audit & Build Brief

> **Role of this folder.** `docs/development-analyst/` is the analyst seat: it
> audits what is **built** against what is **specified** (prototype, PRD,
> `../TICKETS.md`) and turns every gap into a ticket. The rest of `docs/` is the
> contract; this folder is the **variance report + backlog derivation**.
>
> Source of truth, in order: `../PRD.md` → `../SYSTEM_DESIGN.md` → `../prototype/`
> (approved UI) → `../TICKETS.md` → this folder.
>
> Rule from `../DEVELOPMENT_RULE.md`: a screen is **not done** when it returns 200.
> It is done when it matches the approved prototype, in **both light and dark**.

---

## 1. Method

1. **Inventory** every page in `docs/prototype/*.html` (the approved UI).
2. **Inventory** every route in `apps/web/app/**/page.tsx` (what is built).
3. **Diff**: missing page / missing feature / missing state / visual drift.
4. **Cross-check** the backend: does the API + worker actually back the feature,
   or is the FE page a dead control?
5. Output one row per gap → one **P6 ticket**. A gap with no user value is
   dropped with a recorded reason (YAGNI), not silently skipped.

The audit was run against commit `ef2a02e` (post geolocation fix).

---

## 2. Page-by-page variance report

Legend: `OK` = built and faithful · `PARTIAL` = route exists but drifts ·
`MISSING` = no route · `DEAD` = route exists, backend does not back it.

| Prototype page             | Built route             | Status  | Variance (what differs from the approved prototype)                                                                                          |
| -------------------------- | ----------------------- | ------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| `dashboard.html`           | `/` (`app/page.tsx`)    | PARTIAL | Exists but is not the prototype's dashboard grid/cards; nav group structure differs.                                                         |
| `login.html`               | —                       | MISSING | No `/login` page. Auth runs through the shell only; a hard refresh on an expired session has no branded login screen.                        |
| `workers.html`             | `/workers`              | PARTIAL | Route + create form exist. City dropdown added (geolocation). **Drift**: prototype groups workers by region/location; built page lists flat. |
| `accounts.html`            | `/accounts`             | PARTIAL | List exists. Prototype's per-platform status chips + session-health column not present.                                                      |
| `add-account.html`         | —                       | MISSING | No route. Create-account wizard (platform → credentials → worker assignment) absent.                                                         |
| `bulk-import.html`         | —                       | MISSING | No route. CSV/paste bulk import of accounts absent.                                                                                          |
| `actions.html`             | `/actions`              | PARTIAL | Queue + batch controls exist. Prototype's target-picker (post URL → preview → action matrix) not present.                                    |
| `templates.html`           | `/templates`            | OK-ish  | CRUD present. Minor: prototype's per-platform template tag + live preview pane absent.                                                       |
| `monitoring.html`          | `/monitoring`           | PARTIAL | Overview exists. Prototype's official-account selector + reach summary cards absent.                                                         |
| `analytics-instagram.html` | `/monitoring/instagram` | PARTIAL | Platform route exists via `[platform]`. Content is generic, not the IG-specific analytics layout from the prototype.                         |
| `analytics-threads.html`   | `/monitoring/threads`   | PARTIAL | Same generic layout.                                                                                                                         |
| `analytics-facebook.html`  | `/monitoring/facebook`  | PARTIAL | Same generic layout.                                                                                                                         |
| `analytics-linkedin.html`  | `/monitoring/linkedin`  | PARTIAL | Same generic layout.                                                                                                                         |
| `analytics-x.html`         | `/monitoring/x`         | PARTIAL | Same generic layout.                                                                                                                         |
| `analytics-youtube.html`   | `/monitoring/youtube`   | PARTIAL | Same generic layout.                                                                                                                         |
| `analytics-tiktok.html`    | `/monitoring/tiktok`    | PARTIAL | Same generic layout.                                                                                                                         |
| `audit.html`               | —                       | MISSING | No `/audit` route. RBAC audit-log table (already backed by `AuditLog` + `/reports`?) is not exposed as its own page.                         |
| `settings.html`            | —                       | MISSING | No `/settings`. Team/role management + system config absent from UI.                                                                         |

**Summary: 19 prototype screens → 7 built routes. 5 MISSING pages, 0 DEAD, 12 PARTIAL.**

### 2.1 What is _not_ a gap (checked, deliberately not ticketed)

- **Analytics for worker accounts.** Confirmed: analytics is for **official
  (monitored) accounts** only. Worker accounts never appear in analytics.
  The prototype's analytics pages are official-account pages — correct as-is.
- **Analytics data source.** Confirmed: a **3rd party** feeds official-account
  analytics, so P6 analytics pages are read-only views over ingested metrics,
  not a scraper. See `P6-06`.
- **Worker containers at zero data.** By design there are **no worker
  containers by default**; the user creates them. Static provisioner records a
  row, then `docker compose up --scale worker=N` brings the container up. Not a
  parity gap — but it _is_ why "create worker" does not show a running
  container. Documented in `../MANUAL_TESTING.md`.

---

## 3. Cross-cutting variance (affects every page)

| #   | Variance                                                                                                                                                                          | Evidence                                                                                                                        | Ticket  |
| --- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- | ------- |
| C1  | **Light mode broken / missing.** Prototype ships both themes (`theme.js`, default dark); multiple pages render dark-only or with neon-ish colors that hurt long sessions.         | `docs/prototype/theme.js`; user feedback "perbaiki designnya, lightmode dan dark mode harus sudah ada. jangan terlalu neon".    | `P6-01` |
| C2  | **Layout inconsistency across pages.** Prototype enforces one contract: `header (h-14) → main (p-6) → #page (space-y-4)`, no manual `mt-*`. Built pages improvise padding/rhythm. | `docs/prototype/shell.js` "Layout contract for ALL shell pages".                                                                | `P6-02` |
| C3  | **Nav missing the Monitoring group tree + 7 platform entries + Settings + Audit.**                                                                                                | `docs/prototype/shell.js` `NAV`; built `dashboard-shell.tsx` lacks entries.                                                     | `P6-02` |
| C4  | **Dummy process for every action missing.** Actions must run end-to-end as a _dummy_ (dry-run) flow so the UI is demonstrable before the worker lands.                            | User: "semua action buatkan dummy process nya". `ACTION_DRY_RUN=true` exists in the worker; the FE needs a visible dummy state. | `P6-04` |

---

## 4. The P6 phase — Prototype Parity

**Goal:** make the built dashboard indistinguishable from the approved
prototype, in both themes, with every page present and every control wired to
at least a dummy process.

**Scope guard (YAGNI):** P6 is **UI + wiring only**. It does **not** build new
scrapers, new platform adapters, or the 3rd-party analytics ingestion. It
consumes existing APIs; where an API is missing _and_ the page is useless
without it, the ticket says so and the page renders an explicit empty state
instead of faking data.

**Non-goals:** no new DB tables except where a page is unreadable without them
(audit page reads existing `AuditLog` — no new table); no worker changes except
the dummy-action visibility; no infra change.

**Ordering:** `P6-01` (theme) and `P6-02` (layout + nav) first — every later
page inherits them. Then missing pages, then per-platform analytics, last.

**Exit criteria:**

1. All 19 prototype screens have a built route reachable from the nav.
2. Light + dark both ship; no neon; no theme flicker on first paint.
3. Every action control runs a visible dummy process with a clear
   "dummy / dry-run" badge.
4. `docs/prototype/` and `apps/web` reviewable side-by-side with no unexplained
   visual delta; every remaining delta is recorded as an accepted deviation in
   `P6-12`.
5. `make ci` green; every new route has a rendering smoke check.

---

## 5. Tickets

| ID    | Title                                     | Est | Dep    | Pri |
| ----- | ----------------------------------------- | --- | ------ | --- |
| P6-01 | Theme system: real light + dark, no neon  | M   | –      | P0  |
| P6-02 | Shell parity: layout contract + full nav  | M   | P6-01  | P0  |
| P6-03 | Login page + session- expiry handling     | S   | P6-02  | P0  |
| P6-04 | Dummy action process with dry-run badge   | M   | P6-02  | P0  |
| P6-05 | Workers page: location grouping + geo UI  | S   | P6-02  | P1  |
| P6-06 | Official-account analytics (read-only)    | L   | P6-02  | P1  |
| P6-07 | Add-account wizard                        | M   | P6-02  | P0  |
| P6-08 | Bulk import accounts                      | M   | P6-07  | P1  |
| P6-09 | Actions page: target picker + preview     | M   | P6-04  | P0  |
| P6-10 | Audit log page                            | S   | P6-02  | P1  |
| P6-11 | Settings + team/role management           | M   | P6-02  | P1  |
| P6-12 | Parity sign-off + accepted-deviation list | S   | all P6 | P0  |

Detail files: `docs/tickets/p6_*.md` (one per ticket, same shape as existing
`pN_*.md`). Index: `../tickets/README.md`.

---

## 6. Open questions (need user confirmation before P6-06/P6-11)

1. **P6-06** — which 3rd-party analytics provider feeds official accounts? The
   page is read-only either way, but the ingestion ticket (out of P6 scope)
   needs the name. **Assumption until answered:** render from whatever metrics
   already land in the DB; empty state otherwise.
2. **P6-11** — settings must manage team members + roles. The API has 3 roles
   (OWNER/STRATEGIST/OPERATOR/ANALYST). Confirm the settings page may create
   users and flip roles (OWNER-gated), or whether it is read-only in MVP-1.
3. **P6-08** — bulk import format: CSV only, or also paste-JSON? Prototype has
   a textarea; confirm CSV is the required shape.

These are recorded here so they block only their own ticket, not the phase.
