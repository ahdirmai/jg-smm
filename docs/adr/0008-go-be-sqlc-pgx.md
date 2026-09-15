# 0008. Backend in Go with sqlc + pgx + Echo + slog

- **Status**: accepted
- **Date**: 2026-09
- **Tags**: backend, stack

## Context and Problem Statement

The API must serve REST + SSE + a scheduler in one process, be cheap to run, and be easy to reason about by a small team. We want compile-time safety between Go and SQL.

## Decision Drivers

- Statically typed SQL (no ORM magic).
- Fast cold start and a single deployable binary.
- One process to host REST, SSE and the scrape scheduler.

## Considered Options

1. Node/TypeScript API (same language as FE/worker).
2. Go + Echo + pgx + sqlc + slog.
3. Go + a full ORM (GORM).

## Decision Outcome

Chosen option: **"Go + Echo + pgx + sqlc + slog."** because sqlc generates typed Go from SQL keeping queries explicit; Echo + slog keep the surface small; a single binary is trivial to containerise.

### Consequences

- **Good**: Type-safe queries, small footprint, simple deployment.
- **Bad**: A second language/toolchain alongside TS.
- **Neutral**: Migrations via golang-migrate; generated code in `internal/repository/sqlcgen`.

### Confirmation

CI runs gofmt/vet/build/test and fails on sqlc/OpenAPI drift.

## Pros and Cons of the Options

### Go + Echo + pgx + sqlc + slog. *(chosen)*

- **Good**: Type-safe queries, small footprint, simple deployment.
- **Bad**: A second language/toolchain alongside TS.

### Alternatives

- **Node/TypeScript API (same language as FE/worker).** — rejected; its trade-offs did not beat the chosen option against the decision drivers.

- **Go + a full ORM (GORM).** — rejected; its trade-offs did not beat the chosen option against the decision drivers.

## More Information

- DEVELOPMENT_RULE §backend; P0-03, P0-05; SYSTEM_DESIGN.
- See also: `DEVELOPMENT_RULE.md` §17.1, `PRD.md`, `ERD.md`, `SYSTEM_DESIGN.md`.
