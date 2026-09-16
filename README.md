# jg-smm — Social Media Management

Scrape, monitor, and automate actions across social platforms (IG/Threads for
MVP). The full spec set lives in [`docs/`](docs/) (`docs/PRD.md`, `docs/ERD.md`,
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

Manual, end-to-end run-through of the live stack (containers up, no mocks):
`docs/MANUAL_TESTING.md`.
