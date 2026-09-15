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

| Ticket | Title                                         | Status    | Commit    | Date    | Notes                                                                                                                                                                                                                      |
| ------ | --------------------------------------------- | --------- | --------- | ------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| —      | Spec set + ticket backlog                     | DONE      | `3bb8c4a` | 2026-09 | PRD, ERD, System Design, Design System, Dev Rule, Phase, TICKETS (74), Platform briefs, Infra Analyst                                                                                                                      |
| —      | Spec: Official Accounts + 3rd-party analytics | DONE      | —         | 2026-09 | PRD F5 rewrite, ERD (+4 models/enums), System Design analytics path, Platform Matrix §2.3; tickets 74→80 (P2-10..P2-15)                                                                                                    |
| —      | Docs: worker provisioning user-initiated      | DONE      | `727d96a` | 2026-09 | Fleet default empty; manual create from UI                                                                                                                                                                                 |
| —      | Docs: pin container runtime                   | DONE      | `35047a1` | 2026-09 | Superseded: runtime is now colima (see below)                                                                                                                                                                              |
| P0-01  | Monorepo scaffold                             | DONE      | `8dc8c79` | 2026-09 | pnpm workspaces, turbo, app skeletons                                                                                                                                                                                      |
| P0-02  | Local compose stack                           | DONE      | `cfb7d90` | 2026-09 | postgres/redis/minio/api/web/worker, host port range 24xxx                                                                                                                                                                 |
| P0-03  | API skeleton (Echo)                           | DONE      | `49bccdd` | 2026-09 | healthz/readyz, config, graceful shutdown                                                                                                                                                                                  |
| P0-04  | Core schema migration                         | DONE      | `ba4d724` | 2026-09 | make migrate targets                                                                                                                                                                                                       |
| P0-05  | sqlc query generation                         | DONE      | `1334ee8` | 2026-09 | sqlc wired into API                                                                                                                                                                                                        |
| P0-06  | Auth (JWT, argon2, RBAC)                      | DONE      | `1e08943` | 2026-09 | + Go 1.26 bump                                                                                                                                                                                                             |
| P0-07  | OpenAPI as contract SSOT                      | DONE      | `94b6052` | 2026-09 | oapi-codegen, openapi-typescript                                                                                                                                                                                           |
| P0-08  | Web dashboard shell (shadcn tokens)           | DONE      | `7babc59` | 2026-09 | sidebar shell, theme tokens, Tailwind 4.1.14                                                                                                                                                                               |
| P0-09  | Worker skeleton                               | CODE DONE | `45e8aab` | 2026-09 | src structure per Dev Rule §7.2; typecheck+build+6 unit tests green locally. Container boot verification blocked on colima DNS (see below)                                                                                 |
| P0-10  | CI pipeline                                   | DONE      | —         | 2026-09 | `.github/workflows/ci.yml`: 6 jobs — js (lint/typecheck/build/test/proto-verify), go, codegen drift, migrate up+down, Trivy fs, `ci-ok` gate. ESLint 9 flat config added (worker/web/shared/ui). `make ci` mirrors locally |
| P0-11  | Pre-commit & Makefile finalize                | TODO      | —         |         |                                                                                                                                                                                                                            |

### P0-10 detail

- **Workflow** `.github/workflows/ci.yml` — jobs: `js`, `go`, `codegen`, `migrate-dry`, `trivy`, and a single `ci-ok` gate for branch protection. Concurrency cancels superseded runs.
- **Lint was broken and is now fixed**: worker/shared/ui had an `eslint` script but no ESLint dependency or config, and `apps/web` used the interactive `next lint`. Added a shared ESLint 9 flat config (`eslint.config.mjs`) extended by every workspace; switched web to `eslint .`.
- **codegen job** regenerates Go types from `openapi/openapi.yaml` and fails on drift (verified deterministic locally: no diff).
- **migrate-dry job** runs `migrate up` then `down -all` against a Timescale Postgres service (validated locally — cycle clean).
- **trivy** scans fs (vuln + secret + misconfig), blocks fixable HIGH/CRITICAL.
- **`make ci`** runs the same checks locally without containers.

### P0-09 detail

- Files: `apps/worker/src/{index.ts,types.ts}` + `core/` (config, logger, heartbeat, browser, session, auth, controller), `platforms/` (adapter, registry, instagram, threads), `transport/` (queue, control, callback), `sel/`.
- Unit test: `apps/worker/src/test/unit.test.ts` (6 tests, `node:test`) — green via `pnpm --filter @smm/worker test`.
- Dev-rule deviation accepted: tests compile to `dist/test/**` and run there (native TS strip does not rewrite `.js` specifiers).
- **Open**: `docker compose build worker` fails at `apt-get` inside colima due to intermittent DNS to `deb.debian.org` (registry DNS fine). Mitigation in progress: colima restarted with `--dns 1.1.1.1 --dns 8.8.8.8`. Container-boot + heartbeat-cadence verification pending that.

## Frontend prototype (pre-implementation review)

| Item                     | Status | Commit    | Date    | Notes                                                                                                                                                      |
| ------------------------ | ------ | --------- | ------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Static FE prototype      | REVIEW | `6b59312` | 2026-09 | `docs/prototype/` — Tailwind CDN + design tokens, no build step. Awaiting user approval before porting.                                                    |
| Light + dark themes      | DONE   | `4216756` | 2026-09 | `theme.js` maps tokens into the Play CDN, persisted per browser, header toggle + floating button on standalone                                             |
| Per-platform analytics   | REVIEW | —         | 2026-09 | 7 new pages `analytics-{instagram,threads,facebook,linkedin,x,youtube,tiktok}.html`; Monitoring nav group                                                  |
| Dummy action-to-target   | REVIEW | —         | 2026-09 | `actions.html` + `actions.js` — inline stepper Queued→Dispatched→Running→Verifying→Success\|Failed                                                         |
| Automated browser verify | DONE   | —         | 2026-09 | `docs/prototype/verify.mjs` — 19/19 pages PASS (CSS resolves, theme toggles, nav shell, no console errors)                                                 |
| Screenshot capture tool  | DONE   | —         | 2026-09 | `docs/prototype/shots.mjs` → downscaled JPEGs in `docs/prototype/_shots/`; supports explicit targets and `--all` (28 shots)                                |
| Design refinement pass   | DONE   | —         | 2026-09 | Desaturated palette (no neon), segmented Light\|Dark switch, primary-tinted active nav, soft card shadow in light only. Re-verified 19/19 + 28 screenshots |

### Prototype verification finding (fixed)

- `actions.html` carried `class="dark"` on `<body>`, which locked the theme
  tokens so the light/dark toggle had no effect (the body background did not
  change). Fixed by removing it — theme state lives on `<html>` only, set by
  `theme.js`. Confirmed by `pnpm run proto:verify` (body bg now differs between
  themes on every page).

### Design refinement (this pass)

- **Palette desaturated** (`docs/prototype/styles.css`): light `--background`
  `220 20% 97%` (soft off-white, not pure white), dark `222 20% 10%` (soft
  charcoal, not near-black); `--primary` muted indigo `230 45% 48%` / `230 52% 66%`;
  success/warning/info/destructive desaturated. Badges softened to `/0.28`
  border + `/0.1` fill; stepper-dot glow removed.
- **Segmented theme switch** (`theme.js` `switchMarkup()`/`bindThemeSwitch()`, used
  by `shell.js`, `login.html`, `index.html`): explicit `Light|Dark`, `aria-pressed`
  state, synced via the `smmthemechange` event.
- **Active nav** now uses a primary tint (`hsl(var(--primary) / 0.12)`) + primary
  text so the current route reads clearly.
- **Evidence**: `node docs/prototype/shots.mjs --all` → 28 screenshots (14 pages ×
  2 themes) in `docs/prototype/_shots/`.

### Scope split confirmed

- **Worker accounts** execute actions (not measured in analytics).
- **Official accounts** (client/brand, read-only) are the analytics subject;
  their metrics come from a **3rd-party provider**, ingested distinct from the
  worker action path.

---
