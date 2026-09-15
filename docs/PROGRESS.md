# PROGRESS — MVP-1-SMM

> Living log of every completed step/ticket. Updated alongside each commit.
> Format: `Ticket · Status · Commit · Date · Notes`. Source of truth for
> backlog remains `TICKETS.md`; this file records what is actually **done**.

## Legend

- **Status**: `TODO` · `WIP` · `DONE` · `BLOCKED`
- Commit column = short SHA on `main` (commit only, never pushed).
- One row per ticket. Sub-steps within a ticket are notes, not rows.

---

## P0 — Foundation & Walking Skeleton

| Ticket | Title                                    | Status    | Commit    | Date    | Notes                                                                                                                                      |
| ------ | ---------------------------------------- | --------- | --------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------ |
| —      | Spec set + ticket backlog                | DONE      | `3bb8c4a` | 2026-09 | PRD, ERD, System Design, Design System, Dev Rule, Phase, TICKETS (74), Platform briefs, Infra Analyst                                      |
| —      | Docs: worker provisioning user-initiated | DONE      | `727d96a` | 2026-09 | Fleet default empty; manual create from UI                                                                                                 |
| —      | Docs: pin container runtime              | DONE      | `35047a1` | 2026-09 | Superseded: runtime is now colima (see below)                                                                                              |
| P0-01  | Monorepo scaffold                        | DONE      | `8dc8c79` | 2026-09 | pnpm workspaces, turbo, app skeletons                                                                                                      |
| P0-02  | Local compose stack                      | DONE      | `cfb7d90` | 2026-09 | postgres/redis/minio/api/web/worker, host port range 24xxx                                                                                 |
| P0-03  | API skeleton (Echo)                      | DONE      | `49bccdd` | 2026-09 | healthz/readyz, config, graceful shutdown                                                                                                  |
| P0-04  | Core schema migration                    | DONE      | `ba4d724` | 2026-09 | make migrate targets                                                                                                                       |
| P0-05  | sqlc query generation                    | DONE      | `1334ee8` | 2026-09 | sqlc wired into API                                                                                                                        |
| P0-06  | Auth (JWT, argon2, RBAC)                 | DONE      | `1e08943` | 2026-09 | + Go 1.26 bump                                                                                                                             |
| P0-07  | OpenAPI as contract SSOT                 | DONE      | `94b6052` | 2026-09 | oapi-codegen, openapi-typescript                                                                                                           |
| P0-08  | Web dashboard shell (shadcn tokens)      | DONE      | `7babc59` | 2026-09 | sidebar shell, theme tokens, Tailwind 4.1.14                                                                                               |
| P0-09  | Worker skeleton                          | CODE DONE | —         | 2026-09 | src structure per Dev Rule §7.2; typecheck+build+6 unit tests green locally. Container boot verification blocked on colima DNS (see below) |
| P0-10  | CI pipeline                              | TODO      | —         |         |                                                                                                                                            |
| P0-11  | Pre-commit & Makefile finalize           | TODO      | —         |         |                                                                                                                                            |

### P0-09 detail

- Files: `apps/worker/src/{index.ts,types.ts}` + `core/` (config, logger, heartbeat, browser, session, auth, controller), `platforms/` (adapter, registry, instagram, threads), `transport/` (queue, control, callback), `sel/`.
- Unit test: `apps/worker/src/test/unit.test.ts` (6 tests, `node:test`) — green via `pnpm --filter @smm/worker test`.
- Dev-rule deviation accepted: tests compile to `dist/test/**` and run there (native TS strip does not rewrite `.js` specifiers).
- **Open**: `docker compose build worker` fails at `apt-get` inside colima due to intermittent DNS to `deb.debian.org` (registry DNS fine). Mitigation in progress: colima restarted with `--dns 1.1.1.1 --dns 8.8.8.8`. Container-boot + heartbeat-cadence verification pending that.

## Frontend prototype (pre-implementation review)

| Item                | Status | Commit | Date    | Notes                                                                                                                          |
| ------------------- | ------ | ------ | ------- | ------------------------------------------------------------------------------------------------------------------------------ |
| Static FE prototype | REVIEW | —      | 2026-09 | `docs/prototype/` — 12 pages, Tailwind CDN + design tokens, no build step. Awaiting user approval before porting to `apps/web` |

---
