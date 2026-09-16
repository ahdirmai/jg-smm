# Architecture Decision Records (ADRs)

Every significant architecture decision is recorded here as a small, immutable
note in **[MADR](https://adr.github.io/madr/)** format: Context → Drivers →
Options → Decision → Consequences → Confirmation.

- **Template**: [`0000-template.md`](0000-template.md) — copy it for a new ADR.
- **Rule**: `../DEVELOPMENT_RULE.md` §17. An ADR is required when we change the
  DB/queue/auth provider, add a platform adapter with new trade-offs, introduce
  a new scaling/provisioning pattern, or add a heavy runtime dependency.
- **Status** values: `proposed` · `accepted` · `rejected` · `deprecated` ·
  `superseded by ADR-NNNN`. ADRs are append-only: to change a decision, write a
  new ADR that supersedes the old one (do not rewrite history).
- Scaffold helper: `python3 scripts/gen_adr.py` regenerates 0001–0012 from the
  narratives in `../DEVELOPMENT_RULE.md` §17.1 (keeps the rule and the ADRs in sync).

## Index

| ADR                                                   | Decision                                                             | Area                  |
| ----------------------------------------------------- | -------------------------------------------------------------------- | --------------------- |
| [0001](0001-container-per-device.md)                  | One container per device hosts N accounts (max one per platform)     | infra · provisioning  |
| [0002](0002-playwright-vs-apify-action.md)            | Actions use Playwright; scraping uses Apify                          | worker                |
| [0003](0003-sequential-batch-action.md)               | Action concurrency is 1 per container (batch sequential)             | worker                |
| [0004](0004-single-team-no-workspace.md)              | Single team, no multi-tenancy in the MVP                             | backend · data        |
| [0005](0005-timescaledb-for-metrics.md)               | Time-series metrics use TimescaleDB                                  | database              |
| [0006](0006-redis-list-durable-action.md)             | Durable action queue via Redis List; control via Pub/Sub             | transport             |
| [0007](0007-shadcn-ui-design-system.md)               | UI built on shadcn/ui                                                | frontend              |
| [0008](0008-go-be-sqlc-pgx.md)                        | Backend in Go with sqlc + pgx + Echo + slog                          | backend               |
| [0009](0009-all-containerized-no-managed-services.md) | All services self-hosted containers (no managed cloud)               | infra                 |
| [0010](0010-sse-over-websocket.md)                    | Real-time uses SSE, not WebSocket                                    | realtime              |
| [0011](0011-worker-redis-pubsub-callback-contract.md) | Worker: subscribe control + POST callbacks (never touches DB)        | worker · contract     |
| [0012](0012-desired-state-worker-provisioning.md)     | Dynamic workers via a desired-state reconciler (empty fleet default) | provisioning          |
| [0013](0013-live-monitoring-deferred.md)              | Live TikTok/IG viewer monitoring deferred (no reliable source)       | monitoring · deferral |
