# ALERT: fleet-health-low

**Severity:** SEV2 — the average account health score has fallen below 50,
meaning failures are fleet-wide rather than per-account.
**Alert source:** Prometheus `FleetHealthScoreLow` (infra/obs/alerts.yml):
`avg(smm_worker_health_score) < 50` for 15m.
**Metric:** `smm_worker_health_score` (set by `applyHealth` in
`internal/service/health_score_wire.go`).

## Symptoms

- The dashboard's account list shows many accounts trending amber/red, not one
  or two.
- `smm_worker_health_score` averaged across accounts below 50 and falling.
- Quarantined account count is climbing (`smm_accounts_total{status="quarantined"}`).

## Impact

The fleet is losing capacity: accounts below 30 leave the pool automatically,
and a fleet-wide slide means the cause is environmental (platform, proxy,
target batch) rather than any one account's session. Fixing accounts
individually will not outpace the fall.

## Likely causes

1. **Platform-wide rate limiting** — the platform throttled the whole fleet;
   every account's failures are `RATE_LIMIT`. This is the commonest cause of a
   symmetric slide.
2. **Proxy pool degraded** — residential IPs rotating into dead ranges;
   failures read as `TRANSIENT` with connection-style messages.
3. **A bad target batch** — many accounts pointed at the same dead URLs, all
   failing identically. Health falls in step because the batch is shared.
4. **Image/worker regression** — a new worker image broke a platform flow for
   everyone at once; check `worker.image_version` distribution.

## Diagnosis

```bash
# 1. Average health and its trend (Grafana or the raw query):
curl -s localhost:24090/api/v1/query --data-urlencode 'query=avg(smm_worker_health_score)'

# 2. Break it down by platform: is it both or one?
curl -s localhost:24090/api/v1/query --data-urlencode \
  'query=avg by (platform)(smm_worker_health_score)'

# 3. DB view, which carries the error text the metric drops:
docker compose exec -T postgres psql -U smm -d smm -c \
  "select platform, status, count(*) as n, avg(health_score)::int as avg_health
   from account group by platform, status order by platform, n desc;"

# 4. What are they failing on?
docker compose exec -T postgres psql -U smm -d smm -c \
  "select error_class, count(*) as n from action_log
   where status in ('FAILED','RETRY') and created_at > now() - interval '1 hour'
   group by error_class order by n desc;"

# 5. Did the worker image change recently?
docker compose exec -T postgres psql -U smm -d smm -tAc \
  "select image_version, count(*) from worker group by image_version;"
```

## Remediation

```bash
# A. RATE_LIMIT across the board: back the fleet off globally, let health
#    recover on success (+5 per success vs -10 per failure), then ramp.
docker compose exec -T postgres psql -U smm -d smm -c \
  "update team_config set action_batch_parallelism = 1;"
#    Then restart the API so the scheduler re-reads the config.

# B. Proxy degraded: check pool health and egress.
#    See proxy-budget-exceeded.md for the cost side.

# C. Bad target batch: cancel it (see action-success-rate-low.md step C).

# D. Do NOT bulk-resume quarantined accounts. Quarantine is the safety brake;
#    fix the cause first, then resume individually after verifying the account
#    posts successfully by hand. Bulk-resuming under a platform throttle is how
#    accounts get banned.
```

## Verification

```bash
# Average health recovering above 50 and rising:
curl -s localhost:24090/api/v1/query --data-urlencode 'query=avg(smm_worker_health_score)'
# Quarantine count no longer climbing:
curl -s localhost:24090/api/v1/query --data-urlencode \
  'query=increase(smm_accounts_total{status="quarantined"}[1h])'
```

## Post-mortem prompt

Was the slide symmetric across platforms? If yes, look at the shared layer
(proxy, scheduler, image) before looking at accounts. See
[README](README.md#post-mortem-prompt).
