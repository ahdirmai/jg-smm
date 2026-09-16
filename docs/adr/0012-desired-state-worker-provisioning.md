# 0012. Dynamic workers via a desired-state reconciler (empty fleet by default)

- **Status**: accepted
- **Date**: 2026-09
- **Tags**: infra, provisioning, worker

## Context and Problem Statement

The number of worker containers should be dynamic and manageable from the dashboard. A static list of `WORKER_IDS` cannot express create/delete/pause and drifts from reality. We also want the fleet to start empty rather than pre-provisioning idle containers.

## Decision Drivers

- Operators add/remove containers from the UI, no server access.
- Reconciliation must be idempotent and auditable.
- Do not run (and pay for) containers with no work.

## Considered Options

1. Static `WORKER_IDS` env list.
2. Desired-state reconciler with `Worker.desiredState` + `generation`; fleet empty by default; auto-create fallback on add-account.

## Decision Outcome

Chosen option: **"Desired-state reconciler with `Worker.desiredState` + `generation`; fleet empty by default; auto-create fallback on add-account."** because desired-state reconciliation expresses intent declaratively, is idempotent, and lets the dashboard drive the fleet; an empty default avoids idle cost.

### Consequences

- **Good**: UI-driven fleet, idempotent reconcile, full audit trail.
- **Bad**: More moving parts: reconciler, generation labels, orphan sweeper.
- **Neutral**: Two triggers: events + a 60 s cron; every op in `ProvisionLog`.

### Confirmation

Idempotency via `Worker.generation` + pod label; empty containers auto-delete only for `source=AUTO` (`MANUAL` containers persist).

## Pros and Cons of the Options

### Desired-state reconciler with `Worker.desiredState` + `generation`; fleet empty by default; auto-create fallback on add-account. *(chosen)*

- **Good**: UI-driven fleet, idempotent reconcile, full audit trail.
- **Bad**: More moving parts: reconciler, generation labels, orphan sweeper.

### Alternatives

- **Static `WORKER_IDS` env list.** — rejected; its trade-offs did not beat the chosen option against the decision drivers.

## More Information

- INFRA_ANALYST §15; P1-04, P1-05, P1-19; SYSTEM_DESIGN provisioning.
- See also: `../DEVELOPMENT_RULE.md` §17.1, `../PRD.md`, `../ERD.md`, `../SYSTEM_DESIGN.md`.
