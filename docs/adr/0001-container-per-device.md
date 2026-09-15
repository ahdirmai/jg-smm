# 0001. One container per device hosts N accounts (max one per platform)

- **Status**: accepted
- **Date**: 2026-09
- **Tags**: infra, worker, provisioning

## Context and Problem Statement

We manage hundreds of social accounts. A naive model of one pod per account multiplies resource cost and reconnect churn. A real device, however, is naturally one session per platform. We need an isolation unit that matches how the platforms see us while staying cheap.

## Decision Drivers

- Resource efficiency on a 16 GB machine.
- Match the platform's mental model (one device = one session per platform).
- Keep an enforceable uniqueness invariant.

## Considered Options

1. One container per account (max isolation, max cost).
2. One container per device hosting N accounts, at most one per platform.
3. One container per platform pooled across accounts.

## Decision Outcome

Chosen option: **"One container per device hosting N accounts, at most one per platform."** because it halves the blast-radius/cost trade-off: one real device maps to one container, and the platform-per-container rule is a database invariant.

### Consequences

- **Good**: Cost scales with devices, not accounts; matches how platforms fingerprint.
- **Bad**: Blast radius = all N accounts on the device if the pod dies; mitigated by `MAX_ACCOUNTS_PER_CONTAINER`.
- **Neutral**: PVC `smm-session-<workerId>` holds all sessions on the device.

### Confirmation

Enforced by `@@unique([workerId, platform])` on `Account`; bounded by `MAX_ACCOUNTS_PER_CONTAINER`.

## Pros and Cons of the Options

### One container per device hosting N accounts, at most one per platform. *(chosen)*

- **Good**: Cost scales with devices, not accounts; matches how platforms fingerprint.
- **Bad**: Blast radius = all N accounts on the device if the pod dies; mitigated by `MAX_ACCOUNTS_PER_CONTAINER`.

### Alternatives

- **One container per account (max isolation, max cost).** — rejected; its trade-offs did not beat the chosen option against the decision drivers.

- **One container per platform pooled across accounts.** — rejected; its trade-offs did not beat the chosen option against the decision drivers.

## More Information

- PRD F2/F3; ERD `Account`/`Worker`; SYSTEM_DESIGN provisioning; P1-01, P1-05.
- See also: `DEVELOPMENT_RULE.md` §17.1, `PRD.md`, `ERD.md`, `SYSTEM_DESIGN.md`.
