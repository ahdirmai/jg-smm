# 0009. All services self-hosted containers (no managed cloud)

- **Status**: accepted
- **Date**: 2026-09
- **Tags**: infra, devops

## Context and Problem Statement

The MVP targets a cheap self-hosted deployment and a one-command local dev experience. Managed services (RDS/ElastiCache/S3) add cost, vendor coupling and a dev/prod parity gap.

## Decision Drivers

- Low cost and full control for the MVP.
- Dev/prod parity (same compose topology).
- Avoid cloud lock-in while the product is unproven.

## Considered Options

1. Managed Postgres/Redis/S3.
2. Self-hosted Postgres(Timescale)+Redis+MinIO as containers.

## Decision Outcome

Chosen option: **"Self-hosted Postgres(Timescale)+Redis+MinIO as containers."** because self-hosting keeps MVP cost near zero and makes local = prod topology, at the price of owning DB/Redis ops.

### Consequences

- **Good**: Cheap, portable, identical local and server topology.
- **Bad**: Backups, upgrades and HA are on us (runbooks required).
- **Neutral**: MinIO provides the S3 API, so the storage client is portable.

### Confirmation

compose.yaml is the single topology; no managed endpoints in config.

## Pros and Cons of the Options

### Self-hosted Postgres(Timescale)+Redis+MinIO as containers. *(chosen)*

- **Good**: Cheap, portable, identical local and server topology.
- **Bad**: Backups, upgrades and HA are on us (runbooks required).

### Alternatives

- **Managed Postgres/Redis/S3.** — rejected; its trade-offs did not beat the chosen option against the decision drivers.

## More Information

- INFRA_ANALYST; P0-02; SYSTEM_DESIGN storage.
- See also: `../DEVELOPMENT_RULE.md` §17.1, `../PRD.md`, `../ERD.md`, `../SYSTEM_DESIGN.md`.
