# Manual Testing Checklist

Run-through of the locally deployed stack before each release. Every item is a
single, checkable action against the real containers — no unit tests, no mocks.

Stack state for this run: **empty fleet by design** (P1-19). Workers and accounts
start at zero and are created from the UI, so the empty states below are the
correct first screen, not a seeding failure.

## 1. Prerequisites

| Requirement             | Check                                                                  |
| ----------------------- | ---------------------------------------------------------------------- |
| Colima / Docker running | `docker ps` lists 5 `smm-*` containers, all `healthy`                  |
| Repo env loaded         | `.env` present at repo root (ports + seed admin)                       |
| Ports free              | 24080 api, 24081 web, 24901 MinIO console, 24543 postgres, 24637 redis |

Bring the stack up from the repo root:

```bash
docker compose up -d --wait
curl -sf http://localhost:24080/healthz   # {"status":"ok"}
curl -sf http://localhost:24081/          # 200
```

## 2. Access

| Service            | URL                    | Credentials                         |
| ------------------ | ---------------------- | ----------------------------------- |
| Web dashboard      | http://localhost:24081 | owner@smm.local / changeme-changeme |
| API                | http://localhost:24080 | same, cookie session                |
| API docs (OpenAPI) | `openapi/openapi.yaml` | —                                   |
| MinIO console      | http://localhost:24901 | see `.env` `MINIO_*`                |
| Postgres           | localhost:24543        | see `.env` `POSTGRES_*`             |

The seeded user is **OWNER** — the superset role (F1.4). Login is required for
every `/api` route; `/healthz`, `/readyz`, and `/metrics` stay public.

## 3. Authentication & RBAC

- [ ] Login with the seed credentials → redirects to the dashboard, no error toast.
- [ ] Wrong password → 401, stays on the login page.
- [ ] `curl -s -b cj http://localhost:24080/api/auth/me` → 200, `role: OWNER`.
- [ ] Logout → session cookie cleared, `/api/auth/me` → 401.
- [ ] **Role matrix** (domain/role.go is the source of truth):
  - OWNER = read + act + export + admin (implicit superset)
  - STRATEGIST = read
  - OPERATOR = read + act
  - ANALYST = read + export
- [ ] Verify a gate: queue an action as STRATEGIST → 403 `forbidden`; export a
      report as OPERATOR → 403. (Create the users in postgres or seed them first.)

## 4. Dashboard & Layout

- [ ] `/` renders the overview (analytics summary cards + empty-state copy).
- [ ] With **zero containers**, `/` shows the "Getting started" card instead of
      an empty grid (containers are empty by design until a user creates one).
- [ ] Sidebar nav shows the full tree: Dashboard, Workers, Accounts, Actions,
      Templates, **Monitoring** (expandable: Overview + 7 platforms), Reports,
      Audit, Settings.
- [ ] The **Monitoring group expands** and each of the 7 platforms links to its
      own analytics page.
- [ ] The page title + subtitle in the h-14 header match the route (no manual
      in-page `h1` duplicating it).
- [ ] **Theme toggle** present; switching light → dark persists across a reload.
- [ ] `/settings` → Appearance tab → light/dark/system all apply and persist.
- [ ] Layout width/spacing consistent on every page (same shell, same gutter).
- [ ] No console errors on any page (open devtools network tab).

## 5. Workers / Containers

The fleet starts empty — this section is where the first real data appears.

- [ ] `/workers` shows the empty state with a **Create** button (0 containers).
- [ ] The **KPI strip** shows Containers/Ready/Busy/Error/Accounts enrolled
      (all zero on an empty fleet).
- [ ] Create a worker from the UI. With the default `PROVISIONER_MODE=docker`
      the API **launches a real container** within one reconcile tick
      (`RECONCILE_INTERVAL_SECONDS`, default 5s) — the row starts `PENDING`
      and flips to `READY` on the worker's first heartbeat. Pick a city from
      the **location dropdown** (all Indonesia) and create.
      The API rejects a region that is not ISO alpha-2 (use `ID`) and a location
      that is not a seeded city name (use e.g. `Jakarta`).
- [ ] The create response and the card carry the live-view URL immediately
      (`novncUrl`, e.g. `http://localhost:24100`) because the port is allocated
      at create time from `NOVNC_PORT_MIN..MAX`. An out-of-range port in the
      form's **noVNC port** field is a 400 naming the range; a port another
      container holds is a 409.
- [ ] The new row carries the city + a frozen coordinate:
      `GET /api/containers/<id>` shows `location`, `latitude`, `longitude`.
      The point must sit inside the city's radius, not at (0,0).
- [ ] With ≥2 cities present, containers are **grouped under a city heading**
      with the anchor coordinates; the **city filter** narrows the grid.
- [ ] `docker ps` shows the worker container as
      `smm-worker-<uuid>`, published on `127.0.0.1:<port>` (loopback only).
- [ ] Worker heartbeat reaches the API: `GET /api/containers/<id>` → 200.
- [ ] **Geolocation is applied**: `GET /internal/worker/<id>/geolocation` → 200
      with the same frozen coordinate as the row (the worker spoofs this fixed
      GPS via Playwright before every job).
- [ ] **Live browser (P4-08)**: the **Live view** button is always rendered;
      without a `novncUrl` it is disabled with a tooltip naming the reason.
      On a provisioned worker it opens the modal and the Xvfb screen connects.
- [ ] **An action runs end to end**: with `ACTION_INTERVAL_SECONDS>0`, enqueue a
      like from the actions page. The scheduler publishes to the worker's queue,
      the worker BLPOPs it and callbacks `SUCCESS` (`ACTION_DRY_RUN=true` skips
      the real browser step by design). The actions page flips to `success`.
- [ ] Delete/stop the worker → row leaves the list, container and its named
      volumes are gone.

## 6. Accounts

- [ ] `/accounts` empty state + **Add account** button → `/accounts/new` wizard
      (Platform → Credentials → Review).
- [ ] `/accounts/import` paste-CSV import → rows created; bad rows reported.
- [ ] Add one account per platform (Threads, Facebook, Instagram, LinkedIn, X,
      YouTube, TikTok) → rows appear with the right platform label.
- [ ] **Bulk select**: checkbox column + "select all"; the bulk bar offers
      Pause / Resume / Remove over the filtered set. Apply a platform filter →
      selections outside the filter are dropped from the count.
- [ ] Credentials are stored encrypted, not plaintext (check `accounts.cred` blob
      in postgres — must be ciphertext, not the raw password).
- [ ] One credential set per platform per worker (1 worker = N platforms, 1 account
      each) — enforced at creation; a duplicate is rejected, not silently overwritten.

## 7. Templates

- [ ] `/templates` → create a comment template with spun variants.
- [ ] Template appears in the list and is selectable when queueing an action.
- [ ] Duplicate name rejected with a clear message.

## 8. Actions (queue + batch)

- [ ] `/actions` "Action to target" card: pick an account, paste target URL(s),
      run **Like** and **Comment**.
- [ ] **Report post / Reply comment also enqueue** (job types `action_report`,
      `action_reply_comment`). They were inert in P6; the enum now carries them.
- [ ] Job transitions queued → running → done; the actions table shows per-attempt
      status, the **pipeline stepper** fills with progress, and clicking a row
      opens the detail dialog.
- [ ] Batch sequential execution: several actions on one worker run one after
      another, not concurrently (check `action_logs` ordering).
- [ ] Invalid target URL → 400 validation, nothing queued.
- [ ] A batch larger than 50 URLs is rejected client-side with a message.

## 9. Reports

The export gate (`export` permission) and the date/platform filters are the
things most likely to silently regress — cover them all.

- [ ] `/reports/actions` renders table + trend chart.
- [ ] **Filters actually apply**: `?platform=instagram&from=<date>&to=<date>`
      changes the rows. (This was a silent-binding bug; see `binder.go`.)
- [ ] `/reports/targets` renders the per-target rollup.
- [ ] `/reports/analytics?accountId=<official-account-id>` renders the metric
      series; a missing account → 404 `unknown official account`.
- [ ] **Export CSV**: `kind=actions&format=csv` → 200,
      `Content-Disposition: filename=report-actions.csv`, header row present.
- [ ] **Export JSON**: `kind=targets&format=json` → 200, parses as JSON.
- [ ] **Bad kind** (`kind=bogus`) → 400 with `Content-Type: application/json`
      (not `text/csv` — the header ordering was a real bug).
- [ ] **Bad date** (`from=notadate`) → 400 `invalid query parameters`.

## 10. Monitoring (official accounts)

Official accounts are the _monitored_ brand accounts — distinct from worker
accounts; they never appear in worker analytics.

- [ ] `/monitoring` lists added official accounts across platforms with a
      freshness badge and **Sync now**.
- [ ] The four cards read **Total reach / Total views / Total interactions /
      Monitored accounts**, and each equals the API's total for that one metric
      — `curl -b "$CJ" 'localhost:24080/api/analytics/overview?windowDays=30'`.
      A card that equals the sum of *all* metrics is the old bug.
- [ ] The table shows per-account **Reach / Views / Followers** columns, each
      `—` when the provider did not report that metric (never `0`).
- [ ] `/monitoring/instagram` renders the metric vocabulary the provider
      actually fills: Reach, Views, Interactions, Profile visits, Followers.
      There is no card for a metric Instagram does not report.
- [ ] The account filter narrows the KPI strip, and the default
      "All official accounts" shows every account rather than an empty strip.
- [ ] The metric picker switches the trend series; the header names the metric
      actually drawn and shows the period-over-period change. With no earlier
      period it says so instead of a fabricated percentage.
- [ ] Reach/views metrics update after **Sync now** (Composio).
- [ ] Adding an official account does not create a worker container.
- [ ] **Engagement breakdown** splits the interaction total into Likes /
      Comments / Saves / Shares / Accounts engaged, with the
      interactions-per-reach rate beside the handle. A part the provider did
      not report reads `—`, never `0`; a provider-reported `0` stays `0`.
- [ ] **Top content** lists one row per post — type badge, publish date, a
      permalink link, the caption, and the owning handle — with Reach / Views /
      Interactions / Likes / Comments / Saves / Shares / Eng. rate. Ordered by
      reach, highest first.
- [ ] Every cell the provider did not report renders `—`. Instagram gives a
      static feed post no `views`; a `0` there is a bug, not a small number.
- [ ] The account filter narrows **both** new tables as well as the KPI strip,
      and the Top content caption says which account it is showing.
- [ ] On a platform with no adapter (e.g. `/monitoring/tiktok`) both new
      sections show their empty state, not an empty table or a zeroed one.
- [ ] **Sync now** refetches the content table too, and a second sync does not
      duplicate rows.

**Wiring.** `ANALYTICS_PROVIDER=composio` + `COMPOSIO_API_KEY` selects the real
adapter (Instagram only). The official account must carry the Composio
connected-account id, or the ingest fails loudly:

```bash
curl -b "$CJ" -X POST localhost:24080/api/official-accounts \
  -H 'content-type: application/json' \
  -d '{"platform":"instagram","handle":"<ig_username>","providerRef":"ca_..."}'
```

- [ ] A run against a bound account records `status: success`,
      `accountsOk: 1`; an unbound one records `status: failed` with
      `errorClass` naming the cause (never zeros).

**Per-content endpoint.** `limit` rejects `0`, negatives and junk with 400;
omitting `accountId` returns every account on the platform.

```bash
curl -b "$CJ" 'localhost:24080/api/analytics/instagram/content?windowDays=30&limit=5'
```

- [ ] Absent metrics are JSON `null`, not `0`. The UI renders `null` as `—` and
      a genuine provider-reported `0` as `0`, so confirm the wire format for a
      metric the provider does not report.
- [ ] Repeated ingests do not duplicate: one row per media id.

```bash
docker compose exec -T postgres psql -U smm -d smm -tAc \
  "SELECT count(*), count(DISTINCT external_id) FROM analytics_content_metric;"
```

- [ ] The two counts match.

## 10b. Audit, Settings, Login (P6)

- [ ] `/audit` renders the server audit trail (Time / Actor / Action / Target /
      Result / IP), newest first. Actor resolves to the user's email, or
      `system` for background work.
- [ ] **Actor filter** (dropdown of team members) and **Action filter**
      (distinct actions in the loaded page) narrow the query server-side; the
      free-text filter narrows the loaded rows client-side.
- [ ] **Pagination**: Prev/Next page through `limit=25` pages and the
      "Showing N of M entries" counter matches `total`.
- [ ] **Audit is written by middleware**, not a per-service call: perform any
      mutation (create a container, add an account, queue an action, create a
      user) and a row appears with `result: ok`. A rejected mutation is also
      recorded, with the failure reason in `result`.
- [ ] `/settings` tab nav switches between Team / Proxy groups / Limits /
      Appearance / Danger zone.
- [ ] **Team** (signed in as OWNER): the roster loads; Invite member opens a
      dialog that creates a user (email + name + provisional password + role);
      the role dropdown + Save changes a role; Remove deletes a member. A
      non-OWNER sees the roster read-only with an "Owner-only" badge.
- [ ] **Team guards**: demoting or removing the last OWNER is refused (409),
      and removing your own account is refused (409). A role change revokes
      that user's refresh sessions.
- [ ] Proxy groups, Limits, Danger zone each show an explicit
      "Not wired in the MVP" panel — no button here performs a fake call.
- [ ] **Appearance → theme** actually changes the theme and persists.
- [ ] `/login` renders immediately (visible fallback), hydrates the form, and
      signing in with `owner@smm.local` / `changeme-changeme` redirects to `/`.
- [ ] **Expired session**: clear the cookie → a protected route redirects to
      `/login?next=…` and `next` is honored after sign-in.

## 11. Observability & Infra

- [ ] `GET /metrics` returns Prometheus exposition (no auth, port reachable).
- [ ] Request log lines carry `traceId`, `method`, `path`, `status`, `durationMs`.
- [ ] Postgres / redis / minio containers stay `healthy` for the whole run.
- [ ] `docker compose logs api --tail 50` shows no panics or 500s from this run.

## 12. Sign-off

- [ ] Every page loads with the fleet empty **and** populated.
- [ ] Light + dark themes both legible on every page.
- [ ] No endpoint returned an unexpected status during the run.
- [ ] `make ci` green from the repo root.

## Regression notes

Two defects found and fixed while building this checklist; keep the cases above
because both fail silently:

1. **Report filter 500 (fixed)** — `reportPlatform(nil)` emits `""` not NULL, so
   the platform predicate compared an empty string against the enum column.
   Fixed by casting `a.platform::text` in `report.sql`.
2. **Query-param binding (fixed)** — Echo v4 binds query params by the `query`
   tag while oapi-codegen emits `form` tags, so every GET filter bound to its
   zero value. `/reports/export` 400'd on the empty `kind`; the actions/targets
   filters were ignored outright. Fixed by `formQueryBinder` in `internal/http`.
3. **Container create 500 (fixed)** — the `CreateWorker` INSERT listed `id` as a
   column while the service passed an empty ID → NULL → NOT NULL violation.
   Fixed by letting the DB assign ids (`gen_random_uuid()`). A lowercase region
   is now a 400, since the DB CHECK is `^[A-Z]{2}$`.
4. **Worker geolocation silently dropped (fixed)** — `CreateWorker` had the
   `location/latitude/longitude` columns in `RETURNING` but **not in the
   INSERT**, so every row got NULL geo and `/internal/worker/{id}/geolocation`
   404'd. If a created container ever lacks coordinates again, check that INSERT
   and RETURNING stay in sync after regenerating sqlc.
5. **Web bundle hardcoded `:8080` (fixed)** — `NEXT_PUBLIC_API_URL` is inlined at
   **build time**, so a runtime env cannot fix it. Every `/api/auth/me` call
   from the browser hit `ERR_CONNECTION_REFUSED`. The Dockerfile now takes it as
   a build ARG (default `:24080`) and compose passes it under `build.args`. If
   the API port changes, rebuild the web image — restarting the container alone
   does nothing.
