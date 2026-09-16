# 0013. Live monitoring (TikTok/IG live viewers) is deferred

- **Status**: accepted
- **Date**: 2026-09
- **Tags**: monitoring, deferral, p2

## Context and Problem Statement

PRD F5 asks the platform to monitor live broadcasts: TikTok Live and Instagram
Live viewer counts, surfaced on the monitoring page next to the reach/views
KPIs (ticket P2-09).

There is no supported, stable source for live viewer counts on either platform:

- Neither TikTok nor Instagram exposes a public, documented "current viewers of
  this live" endpoint.
- The only routes are unofficial: scraping the live page HTML/WS frames via
  Playwright from a logged-in session, or a third-party scraper with no
  availability guarantee.
- Both are brittle and session-bound. A live monitor that silently stops
  reporting is worse than no monitor: an operator trusting a stale viewer count
  makes worse decisions than one seeing "not monitored".

The rest of the monitoring spine is in place and is not gated on this:
`analytics_snapshot` (P2-10), the ingestor (P2-12), the read API
(P2-14), and the alert engine (P2-07) all cover reach, views, mentions and
engagement for official accounts.

## Decision Drivers

- No reliable source exists; a flaky live metric poisons trust in the whole
  monitoring page.
- Every live-viewer route needs a logged-in Playwright session, i.e. a worker
  account and its health tied to a monitoring read path — coupling the
  read-only analytics tier to the action tier, which the architecture keeps
  deliberately separate.
- The MVP's stated monitoring value (reach, views, mentions, freshness) is
  already delivered.

## Considered Options

1. Build it now on an unofficial scraper.
2. Build it now on a Playwright worker session.
3. Defer: mark P2-09 deferred, keep the schema and UI seat, revisit when a
   source exists.

## Decision Outcome

Chosen option: **"Defer: mark P2-09 deferred"** because the two build-now
options both ship a feature whose data source can disappear overnight, and
neither is required by any MVP acceptance criterion.

### Consequences

- **Good**: The monitoring page's numbers stay trustworthy; the read-only
  analytics tier stays decoupled from worker sessions.
- **Bad**: Live viewer counts are absent until a source lands. The prototype's
  "Live sources" card (IG/TikTok live) has no data behind it.
- **Neutral**: No schema change is needed later — `analytics_snapshot.metrics`
  (JSONB) already holds provider-specific values, so a live-viewers series can
  be ingested as a new metric without a migration.

### Confirmation

P2-09 is marked DEFERRED in `../PROGRESS.md` referencing this ADR. The
monitoring page ships without the live card.

## Pros and Cons of the Options

### Defer: mark P2-09 deferred. _(chosen)_

Zero implementation risk; honest UI. Cost is the missing feature, which no MVP
AC requires.

### Build it now on an unofficial scraper

Fast to prototype, but uptime is not ours to promise and a provider outage
silently degrades the monitoring page.

### Build it now on a Playwright worker session

Reuses the worker fleet, but binds a monitoring read path to session health,
checkpoint risk, and proxy availability — exactly the coupling the read-only
analytics tier was designed to avoid.

## Revisit Trigger

Revisit when **either** platform publishes a live-viewers API, **or** a
contracted third-party provider offers one with an SLA. At that point: add the
metric to the provider adapter (`port.AnalyticsProvider`), ingest it into
`analytics_snapshot`, and render the card — no schema migration required.
