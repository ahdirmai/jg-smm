# jg-smm-automation — Social Media Management

Scrape, monitor, and automate actions across social platforms (IG/Threads for MVP).
Spec set lives in the repo root (`PRD.md`, `ERD.md`, `SYSTEM_DESIGN.md`, ...).

## Layout

```
apps/api      Go 1.23 API (Echo + pgx + sqlc)
apps/web      Next.js 15 dashboard (shadcn/ui)
apps/worker   Node 22 worker (Playwright + Apify)
packages/     shared TS constants/types, shadcn/ui, base tsconfig
infra/        docker + k8s manifests
docs/adr/     architecture decision records
tickets/      one file per ticket (generated from TICKETS.md)
```

## Prerequisites (local build target: Mac M2 16 GB)

| Tool       | Version  | Notes                                            |
| ---------- | -------- | ------------------------------------------------ |
| Node       | 22.x     | `nvm use` (see `.nvmrc`)                         |
| pnpm       | 9.12     | `corepack enable`                                |
| Go         | 1.23+    |                                                  |
| OrbStack   | latest   | container runtime (set VM RAM ~8 GiB in Settings)|
| Docker CLI | any      | OrbStack provides it                             |

Local containers run via **OrbStack + docker compose** — no Kubernetes (K8s is the
production path; see `SYSTEM_DESIGN.md` → Local Tier).

## Bootstrap

```sh
make bootstrap
make typecheck
```

See `DEVELOPMENT_RULE.md` for conventions and `DEVELOPMENT_PHASE.md` for the roadmap.
