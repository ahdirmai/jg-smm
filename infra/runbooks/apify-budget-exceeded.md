# ALERT: apify-budget-exceeded

**Severity:** SEV2 — Apify spend passed 90% of the monthly ceiling. Scraping
keeps working until 100%, then it silently stops.
**Alert source:** Prometheus `ApifyBudgetNearCeiling` (infra/obs/alerts.yml):
`increase(smm_apify_run_cost_usd_total[30d]) > 0.9 * smm_budget_ceiling{budget="apify"}`.
**Metric:** `smm_apify_run_cost_usd_total{actor}` — a counter, so it is the one
accurate source of cumulative spend. Gauges reset on restart and hide bursts.

## Symptoms

- The alert fires; scraping may still be fully functional (the failure mode at
  100% is silence, not an error, because the actor call returns a cost error
  the ingestor logs as a warning).
- `analytics_ingest_run` rows start showing `FAILED` runs with a cost/quota
  reason once the ceiling is actually hit.

## Impact

Monitoring data stops. The analytics tier degrades to staleness — the
dashboard's freshness badge flips to "stale" and the KPI cards stop updating.
Worker **actions are unaffected** (they use Playwright, not Apify), so this is
a visibility loss, not an execution loss.

## Likely causes

1. **Normal growth** — more official accounts or a higher scrape cadence than
   the budget was sized for.
2. **A scrape loop** — a scheduler misconfiguration re-running the same actor
   (a requeue storm or a jitter misconfiguration) multiplies runs without
   adding data.
3. **Actor cost change** — the provider's per-run price rose; same volume,
   more dollars.
4. **Retries** — transient actor failures retried at the run level each cost
   money; a flaky actor is a spend leak.

## Diagnosis

```bash
# 1. Spend by actor over the window:
curl -s localhost:24090/api/v1/query --data-urlencode \
  'query=sum by (actor)(increase(smm_apify_run_cost_usd_total[30d]))'

# 2. Run volume vs spend: rising runs with flat data = a loop, not growth.
docker compose exec -T postgres psql -U smm -d smm -c \
  "select provider, scope, status, count(*) as runs, min(started_at), max(started_at)
   from analytics_ingest_run where started_at > now() - interval '24 hours'
   group by provider, scope, status order by runs desc;"

# 3. Are the same accounts being scraped repeatedly? Compare distinct accounts
#    to total runs in the window — close together = healthy, far apart = loop.
docker compose exec -T postgres psql -U smm -d smm -tAc \
  "select count(*) as runs, count(distinct scope) as accounts
   from analytics_ingest_run where started_at > now() - interval '24 hours';"

# 4. Cadence the scheduler is actually running at:
docker compose exec -T api printenv ANALYTICS_INGEST_INTERVAL_SECONDS SCRAPE_INTERVAL_SECONDS
```

## Remediation

```bash
# A. Loop confirmed (step 2/3): fix the cadence, then the spend stops
#    accumulating. The budget is consumed by runs, so fewer runs = less spend.
ANALYTICS_INGEST_INTERVAL_SECONDS=1800 make up   # half-hourly, ticket cadence

# B. Genuine growth: raise the ceiling in the budget gauge, and record why —
#    a ceiling that is only ever raised is not a budget.

# C. Ceiling already hit (runs failing): pause non-essential scrape cadence
#    until the window rolls over. Worker actions keep running; only the
#    analytics tier goes stale.
ANALYTICS_INGEST_INTERVAL_SECONDS=0 SCRAPE_INTERVAL_SECONDS=0 make up
```

## Verification

```bash
# Spend rate flattening:
curl -s localhost:24090/api/v1/query --data-urlencode \
  'query=rate(smm_apify_run_cost_usd_total[1h])'
# Freshness badge back to in-sync once cadence resumes:
curl -s localhost:24080/api/analytics/overview
```

## Post-mortem prompt

Was the spend more runs, or more cost per run? Runs-flat-and-cost-up is a
provider change; runs-up is a loop or a cadence decision. See
[README](README.md#post-mortem-prompt).
