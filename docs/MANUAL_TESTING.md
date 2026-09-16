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
- [ ] Sidebar nav shows: Workers, Accounts, Actions, Templates, Monitoring, Reports.
- [ ] **Theme toggle** present; switching light → dark persists across a reload.
- [ ] Layout width/spacing consistent on every page (same shell, same gutter).
- [ ] No console errors on any page (open devtools network tab).

## 5. Workers / Containers

The fleet starts empty — this section is where the first real data appears.

- [ ] `/workers` shows the empty state with a **Create** button (0 containers).
- [ ] Create a worker from the UI. In static mode (`PROVISIONER_MODE=static`)
      this **records the row + audit log only** — it does not start a container.
      Pick a city from the **location dropdown** (all Indonesia) and create.
- [ ] The new row carries the city + a frozen coordinate:
      `GET /api/containers/<id>` shows `location`, `latitude`, `longitude`.
      The point must sit inside the city's radius, not at (0,0).
- [ ] **Scale the container up manually** (static provisioner, by design — there
      are zero worker containers until a user adds one):
      `docker compose up -d --scale worker=1`, then refresh; the row picks up a
      heartbeat and status transitions to running.
- [ ] `docker ps` shows the worker container.
- [ ] Worker heartbeat reaches the API: `GET /api/containers/<id>` → 200.
- [ ] **Geolocation is applied**: `GET /internal/worker/<id>/geolocation` → 200
      with the same frozen coordinate as the row (the worker spoofs this fixed
      GPS via Playwright before every job).
- [ ] **Live browser (P4-08)**: open the worker's noVNC modal → screen connects
      (needs `WORKER_NOVNC_URL`/`WORKER_NOVNC_BASE_URL` in `.env`; skip if unset
      and note it).
- [ ] Delete/stop the worker → row leaves the list, container gone.

## 6. Accounts

- [ ] `/accounts` empty state + **Add account** form (platform select + credentials).
- [ ] Add one account per platform (Threads, Facebook, Instagram, LinkedIn, X,
      YouTube, TikTok) → rows appear with the right platform icon.
- [ ] **Bulk import** (CSV) → rows created; bad rows are reported per-line.
- [ ] Credentials are stored encrypted, not plaintext (check `accounts.cred` blob
      in postgres — must be ciphertext, not the raw password).
- [ ] One credential set per platform per worker (1 worker = N platforms, 1 account
      each) — enforced at creation; a duplicate is rejected, not silently overwritten.

## 7. Templates

- [ ] `/templates` → create a comment template with spun variants.
- [ ] Template appears in the list and is selectable when queueing an action.
- [ ] Duplicate name rejected with a clear message.

## 8. Actions (queue + batch)

- [ ] `/actions` → queue a **like** and a **comment** against a target URL.
- [ ] Targeted-comment/like actions on a **target comment** also queue.
- [ ] Job transitions queued → running → done; the actions table shows per-attempt
      status and a screenshot when the worker attaches one.
- [ ] Batch sequential execution: several actions on one worker run one after
      another, not concurrently (check `action_logs` ordering).
- [ ] Report-on-target action is available and records a `report` action type.
- [ ] Invalid target URL → 400 validation, nothing queued.

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

- [ ] `/monitoring` lists added official accounts across platforms.
- [ ] `/monitoring/[platform]` renders a per-platform analytics page.
- [ ] Reach/views metrics update after an analytics run (third-party provider).
- [ ] Adding an official account does not create a worker container.

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
