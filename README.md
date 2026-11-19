# jg-smm — Social Media Management

Scrape, monitor, and automate actions across social platforms. The MVP covers
Instagram and Threads end to end, plus YouTube top-level comments. The full
spec set lives in [`docs/`](docs/) (`docs/PRD.md`, `docs/ERD.md`,
`docs/SYSTEM_DESIGN.md`, ...).

## Layout

```
apps/api      Go 1.26 API (Echo + pgx + sqlc)
apps/web      Next.js 15 dashboard (shadcn/ui)
apps/worker   Node 22 worker (Playwright + Apify)
packages/     shared TS constants/types (OpenAPI-generated), shadcn/ui, base tsconfig
infra/        docker + k8s manifests, runbooks, load tests
docs/         spec set: PRD, ERD, system design, dev rules, tickets, ADRs,
              platform briefs, prototype, runbooks index
              docs/MANUAL_TESTING.md — the pre-release run-through
docs/adr/     architecture decision records
docs/tickets/ one file per ticket (generated from docs/TICKETS.md)
docs/development-analyst/ audit of built vs approved prototype; derives the P6
             prototype-parity tickets
```

## Prerequisites (local build target: Mac M2 16 GB)

| Tool       | Version | Notes                                               |
| ---------- | ------- | --------------------------------------------------- |
| Node       | 22.x    | `nvm use` (see `.nvmrc`)                            |
| pnpm       | 9.12    | `corepack enable`                                   |
| Go         | 1.26+   |                                                     |
| colima     | latest  | container runtime (start with `--cpu 4 --memory 8`) |
| Docker CLI | any     | provided by colima                                  |

Local containers run via **colima + docker compose** — no Kubernetes (K8s is the
production path; see `docs/SYSTEM_DESIGN.md` → Local Tier).

Production (K8s) install order, manifests and the tier's known gaps:
[`docs/DEPLOY_K8S.md`](docs/DEPLOY_K8S.md), with manifests in `infra/k8s/`.

The active boxd deployment (hub + login worker + 10 workers) and its session
import procedure are in
[`docs/stateless-worker-pool/06-BOXD-INFRA-DEPLOYMENT-RUNBOOK.md`](docs/stateless-worker-pool/06-BOXD-INFRA-DEPLOYMENT-RUNBOOK.md).
Current demo entry point: `https://smm-hub.boxd.sh` (the custom domain is still
pending).

## Bootstrap

```sh
make bootstrap
make typecheck
```

## Database migrations

Migrations live in `apps/api/db/migrations/` (golang-migrate, `*.up.sql` /
`*.down.sql`). The `migrate` compose service runs them on `make up`; the
Makefile targets below run them on demand against the composed Postgres.

```sh
make migrate                  # apply all pending
make migrate-status           # current version + dirty flag
make migrate-down N=1         # roll back the last N (default 1)
make migrate-create NAME=x    # scaffold a new up/down pair
```

## Query code generation (sqlc)

Queries live in `apps/api/db/queries/*.sql` and are compiled by sqlc against
the migration DDL into `apps/api/internal/repository/sqlcgen/`. Generated code
is committed; edit the `.sql` sources, never the `sqlcgen` files.

```sh
make sqlc          # regenerate (runs sqlc in a pinned container)
make sqlc-check    # CI: fail if generated output is stale
```

## API contract generation (OpenAPI)

`openapi/openapi.yaml` is the single source of truth for the HTTP contract.
`make generate` emits Go types/handler interfaces
(`apps/api/internal/http/oapigen`) and FE types
(`packages/shared/src/generated/api.ts`). Generated files are committed and
must be reproducible — edit the spec, never the output.

```sh
make generate        # regenerate Go + TS from the spec
make generate-go     # Go only (oapi-codegen)
make generate-ts     # TS only (openapi-typescript)
```

`make up` brings the whole stack online (`postgres`, `redis`, `minio`,
`migrate`, `api`, `web`, and `WORKERS` worker replicas; default 3).

```sh
make up        # start everything (-d)
make ps        # status
make logs S=api
make down      # stop (volumes kept)
```

Smoke checks after boot: `curl localhost:24080/healthz` (API),
`localhost:24081` (web), `localhost:24901` (MinIO console).

## Observability (app + infra logging)

The platform must log both application behaviour and infrastructure signals so
the team can debug a failed action and see the container state behind it.

| Layer       | What is logged                                       | Where it lands                     |
| ----------- | ---------------------------------------------------- | ---------------------------------- |
| API         | Structured `slog` JSON (request, error, latency)     | `internal/obs` → stdout, collected |
| API         | Every worker callback (heartbeat / auth / attempt)   | `JobService` log lines + DB rows   |
| Worker      | Lifecycle, queue take/ack, browser state transitions | `apps/worker/src/core/logger`      |
| Provisioner | Every CREATE/DELETE op                               | `provision_log` table (audit)      |
| Infra       | Worker CPU/mem/jobs telemetry                        | `heartbeat` table + worker row     |
| Infra       | Health probes (postgres/redis readiness)             | `/healthz` + container HEALTHCHECK |

Principles (see `docs/DEVELOPMENT_RULE.md`):

- Never log credentials or ciphertext (the CI `credential-guard` job enforces this).
- Structured logs only (key/value JSON), never `fmt.Println` in services.
- Heartbeats double as liveness: a stale `worker.last_heartbeat` is the signal
  the reconciler acts on.

## Auth (local)

Create the bootstrap owner, then log in. Sessions are cookie-based: an
HttpOnly access JWT (`smm_at`, 24h) plus a rotating refresh token (`smm_rt`,
30d) stored hashed in `auth_session`.

```sh
SEED_ADMIN_EMAIL=owner@local.test SEED_ADMIN_PASSWORD='devpass-123456' make seed

curl -s -c jar.txt -X POST localhost:24080/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"owner@local.test","password":"devpass-123456"}'
curl -s -b jar.txt localhost:24080/api/auth/me
```

Set `JWT_SECRET` (>= 16 bytes) via env before running the API with a database;
compose provides a dev default. Roles: `OWNER` / `STRATEGIST` / `OPERATOR` /
`ANALYST` (see `apps/api/internal/domain/role.go` for the permission matrix).
See `docs/DEVELOPMENT_RULE.md` for conventions and `docs/DEVELOPMENT_PHASE.md` for the roadmap.

## Operator login & live view

Worker accounts log in through the dashboard's `/accounts` page (the login
cockpit), never through the API:

- **Log in** dispatches `auth-login` to the account's worker; the worker's
  headful Chromium (on Xvfb) opens the platform login page and autofills the
  stored credential, and the operator completes any 2FA/checkpoint in the
  noVNC live view the panel embeds. The credential crosses exactly one
  private hop (the per-worker control channel) and is never logged.
- **Open profile** (an authenticated account) dispatches `browser-open`: the
  worker navigates its live, session-hydrated browser to the public profile,
  so the operator watches the logged-in profile in the noVNC live view — not
  a logged-out tab in their own browser. The URL is SSRF-guarded to the
  account's platform hosts by the API (`POST /api/containers/{id}/browser`).

The live view is published per worker on `127.0.0.1:24100–24299`
(`NOVNC_PORT_MIN/MAX` in `compose.yaml`).

Platform quirks worth knowing before debugging a login:

- **The login form mounts late.** Both Meta platforms are client-rendered:
  Threads' `/login` shows the "Scan to get the app" shell for ~3–10s before
  the fields exist, so the credential fill polls for up to 20s. A
  `login form field not found` line after a couple of seconds is not a
  selector problem.
- **A stored session wins.** `runLogin` hydrates `/data/sessions/session-<platform>.json`
  first and treats a still-valid session as proof, so a scheduled refresh
  never needs a human. Seed or import a session (dashboard export/import, or
  copy the file into the worker's `smm-session-<workerId>` volume) and the
  login settles on the first poll.
- **Proof is cookies, never the URL.** Instagram and Threads both require
  `sessionid` + `ds_user_id`; a redirect to the home page without both is
  not a login.

## Comment replies

The New-Action form's Reply flow targets a specific comment, not a post.
Two ways to pick one:

- choose a scraped comment from the threaded picker (its URL is derived from
  the stored ids — never persisted), or
- paste the comment's own "Copy link".

The URL shape is platform-specific: Instagram addresses a comment as
`/p/<post>/c/<commentId>/`, while Threads has **no `/c/` route** (it 404s) — a
Threads reply is itself a post, so its address is `/post/<comment code>`.
`Enqueue` enforces the platform's shape and rejects the other one. A Threads
per-post scrape therefore stores replies as comments keyed by their own
shortcode (`is_reply` rows, linked to the requested post).

Manual, end-to-end run-through of the live stack (containers up, no mocks):
`docs/MANUAL_TESTING.md`.

### Actions queue

The Actions page targets a post or a specific comment. For YouTube, only
top-level comments are supported: the Apify actor returns the video row, not
comment bodies, so the target is a Short/watch URL rather than a scraped
comment row. Selecting a post that is already stored skips the re-scrape; the
form still fetches comments when the target is a comment.

`Retry failed` retries every FAILED action in the current page, not one row at
a time. The generated text stays reusable for 24 hours: a retry reuses the
original comment when it is still alive, otherwise the AI text, and only then
regenerates. A retry that is refused by the per-platform rate limiter does not
consume a token. The button reports how many actions were retried and skipped.

## Apify actors

Actor ids are **full ids**, never derived from `APIFY_ACTOR_PREFIX`: each
search actor lives under a different Apify namespace, and a prefix-derived
Threads id 404s on every scrape. All are env-overridable and pinned in
`compose.yaml`.

| Purpose | Actor | Env |
| ------- | ----- | --- |
| Threads per-post | `themineworks/threads-scraper` | `APIFY_POST_ACTOR_THREADS` |
| Threads keyword search | `futurizerush/meta-threads-scraper` | `APIFY_SEARCH_ACTOR_THREADS` |
| Instagram per-post | `apify/instagram-post-scraper` | `APIFY_POST_ACTOR_INSTAGRAM` |
| Instagram keyword search | `sasky/instagram-keyword-posts-urls-scraper` | `APIFY_SEARCH_ACTOR_INSTAGRAM` |
| TikTok keyword search | `xmolodtsov/tiktok-search-scraper` | `APIFY_SEARCH_ACTOR_TIKTOK` |
| YouTube keyword search / post | `streamers/youtube-scraper` | `APIFY_SEARCH_ACTOR_YOUTUBE`, `APIFY_POST_ACTOR_YOUTUBE` |
| LinkedIn keyword search | `harvestapi/linkedin-post-search` | `APIFY_SEARCH_ACTOR_LINKEDIN` |
| X keyword search | `apidojo/twitter-scraper-lite` | `APIFY_SEARCH_ACTOR_X` |
| Facebook keyword search | `danek/facebook-search-ppr` | `APIFY_SEARCH_ACTOR_FACEBOOK` |

Per-platform scrape detail (input, output rows, normalization) is in
`docs/platforms/`; batch keyword scraping in `docs/BATCH_SCRAPE.md`.

⚠ **Per-query fees.** X bills ~$0.015–0.016 per query on top of per-result
pricing; the store page advertises only the per-result rate, and X has no
result cap or date filter, so a keyword set is the unit of cost, not a row.
The YouTube actor was replaced with `streamers/youtube-scraper`; its pricing
has not been re-measured, so treat YouTube runs as unmeasured cost until the
Apify run receipt is checked. `APIFY_BUDGET_USD` is parsed into config but not
yet enforced anywhere — do not run at volume until it is.

A wrong field name on an actor is **silent** — the actor falls back to its
own default and the scrape quietly returns the wrong thing, not an error.
The payload pinning for each actor lives in `runner_test.go` for that reason.

An actor that accepts **no date bound** cannot be filtered server-side, so
`max_posts` is spent on posts that may fall outside the operator's window and
are then dropped by the ingestor. Measured on run `d3fc79a8` (Instagram,
3 keywords, 28-day window): 150 items fetched, 27 kept — an 18% yield. A
narrower window raises the yield; a larger `max_posts` only buys more lottery
tickets. When an actor does expose a date filter, send it — see
`docs/platforms/` for which ones do.

The converse is worth stating too: an actor that *does* accept a date bound must
be given one, or the same waste is bought a second time. **TikTok did not.**
This integration shipped `oldestPostDateUnified`/`newestPostDate` on the
strength of a code comment, and `clockworks/tiktok-scraper` has no date field
of any kind in its input schema — both were accepted and discarded, silently,
while a full page was billed. A payload test proves what we sent; it cannot
prove the actor obeyed it, and a comment asserting an actor honours a field is
not evidence that it does. `xmolodtsov/tiktok-search-scraper` also has no date
input, so the window is enforced only in the ingestor — but it caps results at
`maxItems`, which the previous actor did not, and returns both cover and
thumbnail on every row with no billed flag.

TikTok is **pay-per-event**, $0.30 / 1000 results on the free tier against
$3.70 for the actor it replaced. That difference is the whole reason for the
swap, and it is paid per result *returned* — which is why `maxItems` is the
cost ceiling of a run and is pinned by a test. The `suggest`/`hashtag`/`music`
expansion pools are billed as results too and return rows that are not posts,
so they are left off.

`Post.published_at` (migration `000016`) holds the post's own publication
date. `Post.scraped_at` is when *we* looked and is not a substitute; the UI
labels the fallback `scrape …` so the two are never confused.

## Worker capacity

A worker container runs a headful Chromium (plus Xvfb, x11vnc, noVNC) for as
long as it lives: ~85 MiB idle, up to ~460 MiB after a session, 4 GiB limit for
the spike. The fleet is therefore sized for concurrency, never for the number of
accounts:

| Env | Default | Meaning |
| --- | --- | --- |
| `ELASTIC_POOL_MAX_ACTIVE` | `0` | Hard ceiling on **containers running**, fleet-wide. `0` disables both the pool and the cap. |
| `ELASTIC_POOL_IDLE_SECONDS` | `300` | Quiet time before an AUTO worker is paused. |
| `RECONCILE_INTERVAL_SECONDS` | `5` compose / `0` code | Tick for the reconciler and the pool. `0` starts neither, so no container is created or stopped. |

Two independent layers enforce it, and both are needed:

- **The reconciler never creates a container for a PAUSED row.** Absent is the
  correct state for a paused worker; the container is built on demand by the
  waker when a login, action or live view actually needs it. Without this, the
  454 packer-seeded `AUTO/PAUSED` accounts all materialised on the first tick.
- **Creates and resumes are capped**, counted from containers actually running
  on the platform (one `ListRunning` per tick, a slot per create/resume,
  returned on stop/delete). A failed count admits nothing — deferring a tick is
  cheaper than an OOM.

The pool only steers `desired_state` on rows, so it cannot stop a container no
RUNNING row explains; the cap is what holds in that gap. Sizing, deploy and
rollback: [`docs/DEPLOYMENT_ELASTIC_POOL.md`](docs/DEPLOYMENT_ELASTIC_POOL.md).

Worker containers run with `restart: on-failure:3`, **never** `unless-stopped`:
a crashed fleet restarted itself indefinitely through an OOM with the API down,
and `docker stop` alone did not stick. If a fleet ever runs away, the reset is
`docker rm -f $(docker ps -aq --filter name=smm-worker)` — then check
`RECONCILE_INTERVAL_SECONDS` before starting the API again.

## Changelog

`CHANGELOG.md` is generated from conventional commits. `docs/PROGRESS.md`
is the living log of completed tickets (`Ticket · Status · Commit · Date ·
Notes`) and `docs/STATUS_SNAPSHOT.md` is the periodic read-out of what
shipped, the live state, and what is still open.
