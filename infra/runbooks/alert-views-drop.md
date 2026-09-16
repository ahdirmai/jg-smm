# ALERT: alert-views-drop

**Severity:** SEV3 (critical tier when views < 25% of prior window) — a post's
24h views collapsed against its prior 24h.
**Alert source:** `AlertEngine.checkViewsDrop`
(`apps/api/internal/service/alert_engine.go`), rule `views_drop`. Enabled by
`ALERT_INTERVAL_SECONDS`. The engine logs `alert fired kind=views_drop`.

## Symptoms

- API logs contain `alert fired` with `kind=views_drop`,
  `subject=post:<id>`.
- The message gives both windows: `views fell to 30% of the prior 24h (1000 -> 300)`.
- `views` on the post's latest `metric_snapshot` is far below the previous one.

## Impact

A business signal, not an infra one. The post stopped being served (algorithmic
suppression, takedown, or a broken scrape) while the platform itself is fine.
Critical-tier drops (< 25%) usually mean the post is effectively dead, not
fading.

## Likely causes

1. **Platform algorithm / suppression** — the most common real cause; nothing
   in our stack is wrong.
2. **Content takedown or policy action** by the platform.
3. **Scrape failure misread as a drop** — the last scrape failed, so the
   *latest* snapshot is stale or zero. The engine skips a `prevViews == 0` or
   `curViews == 0` pair, but a partial scrape (low counts, not zero) still
   reads as a drop.
4. **Baseline artefact** — a single viral day inflates the prior window, so the
   following day looks like a collapse.
5. **Clock skew between the analytics provider and the scrape clock** — the
   24h windows are computed at evaluation time.

## Diagnosis

```bash
# 1. The fired alert with the numbers.
make logs S=api | grep 'views_drop' | tail -20

# 2. The post's actual metric series — see the collapse, and whether the
#    samples are regular (irregular = scrape problem, not a real drop).
docker compose exec -T postgres psql -U smm -d smm -c \
  "select post_id, ts, views from metric_snapshot where post_id = '<id>' order by ts desc limit 24;"

# 3. Was the latest scrape successful? A failed scrape produces the artefact
#    in cause 3.
docker compose exec -T postgres psql -U smm -d smm -c \
  "select id, status, started_at, finished_at, error from scrape_job order by started_at desc limit 10;"
make logs S=api | grep -i 'scrape' | tail -20

# 4. Is the engine running and evaluating cleanly?
docker compose exec -T api printenv ALERT_INTERVAL_SECONDS
make logs S=api | grep -E 'alert engine|views-drop check failed' | tail -10

# 5. Aggregator churn (a re-sample glitch looks like a drop):
docker compose exec -T api printenv AGGREGATE_INTERVAL_SECONDS
# 6. Is the drop isolated to one post or account-wide? Account-wide = provider
#    or platform issue, not one post's fate.
docker compose exec -T postgres psql -U smm -d smm -tAc \
  "select count(*) from metric_snapshot where ts > now() - interval '24 hours';"
```

## Remediation

```bash
# A. Real suppression: no ops fix exists. Confirm the post is still live on the
#    platform directly, then decide on content strategy. Do NOT re-scrape
#    aggressively — that cannot recover an algorithmic drop.

# B. Scrape artefact (step 3 shows a failed/absent scrape): re-run the scrape
#    once, then let the next evaluation tick fire or clear.
make logs S=api | grep -i 'scrape_job' | tail -20
#    After a successful scrape the alert either clears (counts recovered) or
#    is confirmed real.

# C. Viral-day baseline artefact: the "drop" is a return to normal. Confirm in
#    step 2 that the prior-24h sample was the outlier, then treat as SEV4.

# D. Provider/clock skew: fix the provider config, not the alert.
docker compose exec -T api printenv ANALYTICS_PROVIDER ANALYTICS_PROVIDER_BASE_URL
make down && make up
```

## Verification

```bash
# The post's series is regular again (no scrape gap):
docker compose exec -T postgres psql -U smm -d smm -c \
  "select ts, views from metric_snapshot where post_id = '<id>' order by ts desc limit 12;"
# No new views_drop alert since remediation:
make logs S=api --since 30m | grep 'views_drop' | tail -5 || echo 'no new alerts'
# The alert engine ticked without check failures:
make logs S=api | grep 'alert engine' | tail -3
```

## Post-mortem prompt

Real drop or measurement artefact? If scrape failure masqueraded as a collapse,
the fix is alert-time staleness detection, not a content decision.
See [README](README.md#post-mortem-prompt).
