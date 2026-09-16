# P6-12 — Parity sign-off: prototype vs build

> Phase: **P6 — Prototype Parity** · Status: **signed off with accepted deviations**
> Audit method: side-by-side read of `docs/prototype/*.html` (19 screens) against the built
> Next.js routes in `apps/web/app/`, plus live HTTP checks against the running web container
> (`:24081`). Every route was confirmed to return 200 and to render its shell + key sections.

## Route coverage

All 19 prototype screens have a built route. `index.html` is the prototype's own static
launcher; the app sidebar replaces it, so it has no route by design.

| Prototype                  | Built route             | Status                                         |
| -------------------------- | ----------------------- | ---------------------------------------------- |
| `index.html` (launcher)    | —                       | Intentionally none (sidebar covers navigation) |
| `dashboard.html`           | `/`                     | PARTIAL — accepted                             |
| `workers.html`             | `/workers`              | PARTIAL — accepted                             |
| `accounts.html`            | `/accounts`             | PARTIAL — accepted                             |
| `add-account.html`         | `/accounts/new`         | PARTIAL — accepted                             |
| `bulk-import.html`         | `/accounts/import`      | PARTIAL — accepted                             |
| `actions.html`             | `/actions`              | PARTIAL — accepted                             |
| `templates.html`           | `/templates`            | PARTIAL — accepted                             |
| `monitoring.html`          | `/monitoring`           | PARTIAL — accepted                             |
| `analytics-instagram.html` | `/monitoring/instagram` | PARTIAL — accepted                             |
| `analytics-threads.html`   | `/monitoring/threads`   | PARTIAL — accepted                             |
| `analytics-facebook.html`  | `/monitoring/facebook`  | PARTIAL — accepted                             |
| `analytics-linkedin.html`  | `/monitoring/linkedin`  | PARTIAL — accepted                             |
| `analytics-x.html`         | `/monitoring/x`         | PARTIAL — accepted                             |
| `analytics-youtube.html`   | `/monitoring/youtube`   | PARTIAL — accepted                             |
| `analytics-tiktok.html`    | `/monitoring/tiktok`    | PARTIAL — accepted                             |
| `audit.html`               | `/audit`                | PARITY — audit-log endpoint wired              |
| `settings.html`            | `/settings`             | PARITY — Team tab wired (OWNER)                |
| `login.html`               | `/login`                | PARITY                                         |

Every page is light + dark via the muted palette in `globals.css` (no neon), matching
`docs/prototype/styles.css`.

## Accepted deviations

Each deviation below is either (a) a deliberate scope guard — the API endpoint does not
exist, so the control renders an explicit "not wired" state instead of faking a call — or
(b) a real feature the build has instead of the prototype's demo version. Nothing here is
silent: the UI says what it does and does not do.

### Deliberate (no API endpoint — honest empty state, no fake data)

1. **Settings: Proxy groups / Limits / Danger zone.** No proxy-group-assignment,
   rate-limit, or danger-op endpoints exist. Those three tabs render an explicit "Not
   wired in the MVP" panel. **Team is wired for real** (P6-11): roster, invite, role
   change, remove — OWNER-gated, with the last-owner and self-removal guards. **Appearance
   is wired for real** through `next-themes` (light/dark/system). Density is shown inert,
   single density in the MVP.
2. **Analytics: Top posts rows.** `PlatformAnalytics` carries KPIs + trend + freshness, no
   per-post data. The table matches the prototype's structure and renders the prototype's
   own empty copy: "Not yet enabled in the MVP — layout shown for review." The Filter
   button is inert for the same reason.
3. **RESOLVED — Audit trail.** The audit-log endpoint now exists (`GET /api/audit`) and the
   page renders Time / Actor / Action / Target / Result / IP with actor + action filters
   and pagination. The trail is written by middleware on every state-changing `/api` call,
   so successes and rejections are both recorded. Actor resolves to the user's email,
   or `system` for background work. Request bodies are deliberately not captured — they
   carry secrets (account passwords, proxy pool keys).
4. **Add-account: live-login side panel, 2FA/OTP branch, failure branch.** Account creation
   is a plain 3-step wizard; login is resolved asynchronously by the worker, not inline
   over SSE, so the step list and OTP branch have no source stream.
5. **Bulk import: progress card and challenges queue.** `importAccounts` accepts
   `platform,username,password` rows and returns a result list; there is no enrollment
   progress stream and no challenge queue. Validation output is the flat result list.
6. **Add-account / bulk-import: proxy-group field.** Not a field the API accepts.
7. **Actions: Report post / Reply comment — RESOLVED.** The `job_type` enum now carries
   `ACTION_REPORT` and `ACTION_REPLY_COMMENT` (migrations 000008/000009), the service
   validates them through `IsAction()`, and both enqueue for real (verified 201). The
   buttons on `/actions` are live. Kept here as history; see the build commit.

### Real feature replaces the prototype's demo version (build is the source of truth)

8. **Actions queue.** The prototype animates dummy rows through a pipeline. The build has a
   real queue: SSE-driven (`action-updated`), windowed for 5k rows, grouped per account,
   with a detail dialog and a 4-dot pipeline stepper driven by the real `JobStatus`. No
   "Simulate failure" control — real failures are reported, not staged.
9. **Comment text.** The prototype's free-text comment input is intentionally absent:
   comments are composed from templates and screened before dispatch (PRD requirement).
   The page says so next to the action row.
10. **Monitoring overview KPIs.** The prototype's reach/mentions metrics assume an ingest
    that the 3rd-party source does not yet expose; the build shows monitored accounts /
    platforms / total followers / stale accounts, which the API actually serves, plus a
    freshness badge and Sync now.
11. **Dashboard.** Adds an empty-fleet "Getting started" card the prototype lacks
    (containers are empty by design until a user creates one). Top posts renders its empty
    state until ingest lands; the "Automation today" strip wires Comments to the real queue
    and shows Likes/Reports/Replies as `0` until the queue produces them.
12. **Login.** Real cookie session (`/api/auth/login` → `/api/auth/me`), `next` redirect,
    visible Suspense fallback. Dropped the prototype's "Forgot?" link and the JWT footnote
    (there is no password-reset flow).

## Verification performed

- `make ci` — **EXIT 0** (typecheck, lint, web build, Go unit tests).
- Web image rebuilt; all 19 routes return **200**.
- API smoke: login → `/api/auth/me` 200; container create 201 (region must be ISO alpha-2,
  e.g. `ID`; location must be a seeded city name, e.g. `Jakarta`); container delete 204.
- Login page SSR: renders a visible fallback immediately, hydrates the form client-side.
