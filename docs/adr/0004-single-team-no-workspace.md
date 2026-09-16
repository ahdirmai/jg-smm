# 0004. Single team, no multi-tenancy in the MVP

- **Status**: accepted
- **Date**: 2026-09
- **Tags**: backend, data-model, auth

## Context and Problem Statement

The MVP serves one social-media team. Multi-tenancy would force a workspace column on every root table and complicate auth, RBAC and queries from day one.

## Decision Drivers

- Ship the core value fast (scrape + action).
- Avoid premature complexity (YAGNI).
- Keep a clear, cheap migration path if tenancy is needed later.

## Considered Options

1. Full multi-tenant (workspaceId everywhere) now.
2. Single team now; a documented migration path to workspaces later.

## Decision Outcome

Chosen option: **"Single team now; a documented migration path to workspaces later."** because tenancy is not needed for MVP value and the migration is bounded (~1-2 weeks per DEVELOPMENT_RULE §tenancy).

### Consequences

- **Good**: Simpler schema, auth and queries; faster delivery.
- **Bad**: A later tenancy move is a breaking migration (requires an ADR).
- **Neutral**: Any change touching tenancy mandates a new ADR.

### Confirmation

`TeamConfig` is a singleton row; no `workspaceId` anywhere.

## Pros and Cons of the Options

### Single team now; a documented migration path to workspaces later. *(chosen)*

- **Good**: Simpler schema, auth and queries; faster delivery.
- **Bad**: A later tenancy move is a breaking migration (requires an ADR).

### Alternatives

- **Full multi-tenant (workspaceId everywhere) now.** — rejected; its trade-offs did not beat the chosen option against the decision drivers.

## More Information

- PRD scope; ERD `TeamConfig`; DEVELOPMENT_RULE §tenancy.
- See also: `../DEVELOPMENT_RULE.md` §17.1, `../PRD.md`, `../ERD.md`, `../SYSTEM_DESIGN.md`.
