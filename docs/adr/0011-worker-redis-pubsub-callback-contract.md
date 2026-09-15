# 0011. Worker contract: subscribe control + POST callbacks (never touches DB)

- **Status**: accepted
- **Date**: 2026-09
- **Tags**: worker, backend, contract

## Context and Problem Statement

Workers run untrusted platform sessions and may be re-created often. They must not hold database credentials. We need a contract that keeps the DB a backend-only concern while still reporting per-attempt outcomes.

## Decision Drivers

- Workers never hold DB credentials.
- Per-attempt verdicts are recorded durably (audit).
- Mirror the proven pattern from the reference project `JG/automation`.

## Considered Options

1. Workers write directly to Postgres.
2. Workers only SUBSCRIBE (control) and POST callbacks (verdict); BE owns the DB.

## Decision Outcome

Chosen option: **"Workers only SUBSCRIBE (control) and POST callbacks (verdict); BE owns the DB."** because isolating DB access to the backend keeps credentials safe and centralises validation; each verdict becomes an upserted row.

### Consequences

- **Good**: Small blast radius if a worker is compromised; single validation point.
- **Bad**: Pub/Sub has no delivery guarantee; callback authorization must be enforced beyond loopback.
- **Neutral**: Verdict per attempt = a row (upsert on attempt id).

### Confirmation

Worker config contains no DATABASE_URL; callbacks validated + size-limited.

## Pros and Cons of the Options

### Workers only SUBSCRIBE (control) and POST callbacks (verdict); BE owns the DB. *(chosen)*

- **Good**: Small blast radius if a worker is compromised; single validation point.
- **Bad**: Pub/Sub has no delivery guarantee; callback authorization must be enforced beyond loopback.

### Alternatives

- **Workers write directly to Postgres.** — rejected; its trade-offs did not beat the chosen option against the decision drivers.

## More Information

- SYSTEM_DESIGN worker contract; P1-13; reference `JG/automation`.
- See also: `DEVELOPMENT_RULE.md` §17.1, `PRD.md`, `ERD.md`, `SYSTEM_DESIGN.md`.
