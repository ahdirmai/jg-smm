# 0005. Time-series metrics use TimescaleDB

- **Status**: accepted
- **Date**: 2026-09
- **Tags**: database, metrics, monitoring

## Context and Problem Statement

We store high-cardinality metric snapshots (reach/views/mentions) at 30-minute cadence across posts and official accounts, with retention rules. Plain Postgres tables grow and slow down for range queries.

## Decision Drivers

- Fast time-range aggregation for dashboards.
- Automated retention (hot 90 days, cold 1 year).
- Stay on Postgres (one engine, one ops surface).

## Considered Options

1. Plain Postgres tables with manual partitioning.
2. A dedicated TSDB (e.g. Prometheus/InfluxDB).
3. TimescaleDB extension on Postgres.

## Decision Outcome

Chosen option: **"TimescaleDB extension on Postgres."** because TimescaleDB keeps everything in Postgres while adding hypertables, compression and retention policies.

### Consequences

- **Good**: One database to run/back up; fast time-range queries; native retention.
- **Bad**: Ties us to a Postgres extension image (`timescale/timescaledb`).
- **Neutral**: Hypertables: `MetricSnapshot`, `AnalyticsSnapshot`.

### Confirmation

Retention policy active; dashboards hit hypertables via time_bucket.

## Pros and Cons of the Options

### TimescaleDB extension on Postgres. *(chosen)*

- **Good**: One database to run/back up; fast time-range queries; native retention.
- **Bad**: Ties us to a Postgres extension image (`timescale/timescaledb`).

### Alternatives

- **Plain Postgres tables with manual partitioning.** — rejected; its trade-offs did not beat the chosen option against the decision drivers.

- **A dedicated TSDB (e.g. Prometheus/InfluxDB).** — rejected; its trade-offs did not beat the chosen option against the decision drivers.

## More Information

- ERD metrics; SYSTEM_DESIGN storage; P2-01, P2-05, P2-10.
- See also: `../DEVELOPMENT_RULE.md` §17.1, `../PRD.md`, `../ERD.md`, `../SYSTEM_DESIGN.md`.
