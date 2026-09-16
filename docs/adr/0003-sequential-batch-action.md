# 0003. Action concurrency is 1 per container (batch sequential)

- **Status**: accepted
- **Date**: 2026-09
- **Tags**: worker, action

## Context and Problem Statement

A single browser session cannot safely perform two actions at once without looking robotic and racing on the same cookies. Yet we want throughput across the fleet.

## Decision Drivers

- Avoid detection from parallel same-session activity.
- Bound CPU/RAM per container (headful Chromium is heavy).
- Minimise wall-clock across the fleet.

## Considered Options

1. Run multiple actions in parallel inside one container.
2. One action at a time per container; parallelism across containers.

## Decision Outcome

Chosen option: **"One action at a time per container; parallelism across containers."** because sequential within a container keeps the session coherent and the resource profile flat; the fleet provides parallelism.

### Consequences

- **Good**: Predictable resource use; no in-session races.
- **Bad**: Per-account throughput is bounded by action latency.
- **Neutral**: `ACTION_BATCH_PARALLELISM` controls how many containers act in a batch (default 4, local 2).

### Confirmation

Worker loop processes exactly one dequeued job at a time (BLPOP → run → ACK).

## Pros and Cons of the Options

### One action at a time per container; parallelism across containers. *(chosen)*

- **Good**: Predictable resource use; no in-session races.
- **Bad**: Per-account throughput is bounded by action latency.

### Alternatives

- **Run multiple actions in parallel inside one container.** — rejected; its trade-offs did not beat the chosen option against the decision drivers.

## More Information

- DEVELOPMENT_RULE §concurrency; P1-09; P3-*.
- See also: `../DEVELOPMENT_RULE.md` §17.1, `../PRD.md`, `../ERD.md`, `../SYSTEM_DESIGN.md`.
