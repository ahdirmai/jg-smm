# 0006. Durable action queue via Redis List; control via Pub/Sub

- **Status**: accepted
- **Date**: 2026-09
- **Tags**: backend, worker, transport

## Context and Problem Statement

Actions are money: losing a queued comment silently is unacceptable. Control messages (login, OTP) are ephemeral and only matter to a live worker. We need durable delivery for one and fire-and-forget for the other, without running a heavy broker.

## Decision Drivers

- At-least-once delivery for actions.
- Ephemeral, low-latency control channel.
- Minimal moving parts (no RabbitMQ/Kafka).

## Considered Options

1. BullMQ over Redis (managed job semantics).
2. Redis List (`LPUSH`/`BLPOP`) with explicit ACK for actions; Pub/Sub for control.
3. Kafka/RabbitMQ.

## Decision Outcome

Chosen option: **"Redis List (`LPUSH`/`BLPOP`) with explicit ACK for actions; Pub/Sub for control."** because a List is a durable, inspectable queue with blocking pop and explicit ACK; Pub/Sub fits ephemeral control without persistence overhead.

### Consequences

- **Good**: Actions survive restarts; the queue is trivially observable.
- **Bad**: We implement retry/ACK semantics ourselves.
- **Neutral**: Queues keyed `queue:action:<workerId>`, control channel `control-<workerId>`.

### Confirmation

DB commit happens before LPUSH; worker ACKs only after a verdict.

## Pros and Cons of the Options

### Redis List (`LPUSH`/`BLPOP`) with explicit ACK for actions; Pub/Sub for control. *(chosen)*

- **Good**: Actions survive restarts; the queue is trivially observable.
- **Bad**: We implement retry/ACK semantics ourselves.

### Alternatives

- **BullMQ over Redis (managed job semantics).** — rejected; its trade-offs did not beat the chosen option against the decision drivers.

- **Kafka/RabbitMQ.** — rejected; its trade-offs did not beat the chosen option against the decision drivers.

## More Information

- SYSTEM_DESIGN transport table; P1-08, P1-09, P1-11.
- See also: `DEVELOPMENT_RULE.md` §17.1, `PRD.md`, `ERD.md`, `SYSTEM_DESIGN.md`.
