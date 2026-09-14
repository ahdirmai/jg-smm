# jg-smm-automation — Social Media Management

Scrape, monitor, and automate actions across social platforms (IG/Threads for MVP).
Spec set lives in the repo root (`PRD.md`, `ERD.md`, `SYSTEM_DESIGN.md`, ...).

## Layout

```
apps/api      Go 1.25 API (Echo + pgx + sqlc)
apps/web      Next.js 15 dashboard (shadcn/ui)
apps/worker   Node 22 worker (Playwright + Apify)
packages/     shared TS constants/types, shadcn/ui, base tsconfig
infra/        docker + k8s manifests
docs/adr/     architecture decision records
tickets/      one file per ticket (generated from TICKETS.md)
```

## Prerequisites (local build target: Mac M2 16 GB)

| Tool       | Version | Notes                                               |
| ---------- | ------- | --------------------------------------------------- |
| Node       | 22.x    | `nvm use` (see `.nvmrc`)                            |
| pnpm       | 9.12    | `corepack enable`                                   |
| Go         | 1.25+   |                                                     |
| colima     | latest  | container runtime (start with `--cpu 4 --memory 8`) |
| Docker CLI | any     | provided by colima                                  |

Local containers run via **colima + docker compose** — no Kubernetes (K8s is the
production path; see `SYSTEM_DESIGN.md` → Local Tier).

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

See `DEVELOPMENT_RULE.md` for conventions and `DEVELOPMENT_PHASE.md` for the roadmap.
