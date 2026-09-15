# 0010. Real-time uses SSE, not WebSocket

- **Status**: accepted
- **Date**: 2026-09
- **Tags**: frontend, backend, realtime

## Context and Problem Statement

The dashboard needs live updates (worker health, provisioning, job verdicts, analytics). Traffic is one-way server→browser; commands go over REST. WebSocket would add a bidirectional connection we do not need.

## Decision Drivers

- Simplest transport that fits one-way push.
- Work with plain HTTP infra (proxies, auth headers).
- Easy client consumption and reconnect semantics.

## Considered Options

1. WebSocket / socket.io (bidirectional).
2. SSE via `EventSource`; commands over REST.
3. Long-polling.

## Decision Outcome

Chosen option: **"SSE via `EventSource`; commands over REST."** because SSE is one-way, HTTP-native, auto-reconnects, and needs no extra protocol; REST already covers commands.

### Consequences

- **Good**: Trivial server implementation, works through standard proxies.
- **Bad**: No server→client binary frames; one connection per open tab.
- **Neutral**: Frames carry full entities so the client can reconcile without a separate fetch.

### Confirmation

Client reconnects and refetches on drop; frames validated by schema.

## Pros and Cons of the Options

### SSE via `EventSource`; commands over REST. *(chosen)*

- **Good**: Trivial server implementation, works through standard proxies.
- **Bad**: No server→client binary frames; one connection per open tab.

### Alternatives

- **WebSocket / socket.io (bidirectional).** — rejected; its trade-offs did not beat the chosen option against the decision drivers.

- **Long-polling.** — rejected; its trade-offs did not beat the chosen option against the decision drivers.

## More Information

- SYSTEM_DESIGN realtime; P1-17, P2-14.
- See also: `DEVELOPMENT_RULE.md` §17.1, `PRD.md`, `ERD.md`, `SYSTEM_DESIGN.md`.
