# ALERT: proxy-budget-exceeded

**Severity:** SEV2 — residential proxy egress passed 90% of the monthly byte
ceiling. Actions keep working until the pool cuts the account off.
**Alert source:** Prometheus `ProxyBudgetNearCeiling` (infra/obs/alerts.yml):
`increase(smm_proxy_bytes_used_total[30d]) > 0.9 * smm_budget_ceiling{budget="proxy"}`.
**Metric:** `smm_proxy_bytes_used_total{proxy_group}` — a counter for the same
reason as the Apify one: cumulative spend must survive a restart.

## Symptoms

- The alert fires while actions still succeed; the pool's own over-quota
  response is usually a hard failure later, with no warning in between.
- Once actually cut off, `error_class` flips to `TRANSIENT` with
  connection-style messages across every account on that group.

## Impact

Actions stop for accounts on the affected group. This one is expensive:
residential proxies bill per byte, and a headful browser loads full pages
(images, video preloads), so egress is dominated by content the action never
needed.

## Likely causes

1. **Page weight** — a Playwright action loads the whole post page; video and
   image assets are the bulk of the bytes, not the action itself.
2. **A navigation loop** — a login redirect or an interstitial reloading the
   page inflates egress per action many times over.
3. **Group concentration** — all accounts on one group means one group carries
   all traffic; its ceiling is hit first.
4. **Retry amplification** — retried attempts re-load the page; a flaky
   account's retries cost bytes each time.

## Diagnosis

```bash
# 1. Egress by group:
curl -s localhost:24090/api/v1/query --data-urlencode \
  'query=sum by (proxy_group)(increase(smm_proxy_bytes_used_total[30d]))'

# 2. Egress rate per hour: is it climbing or steady?
curl -s localhost:24090/api/v1/query --data-urlencode \
  'query=sum by (proxy_group)(rate(smm_proxy_bytes_used_total[1h]))'

# 3. Which accounts sit on the hottest group?
docker compose exec -T postgres psql -U smm -d smm -c \
  "select coalesce(proxy_group_id,'(none)') as grp, count(*), sum(health_score)/count(*) as avg_health
   from account where status in ('ACTIVE','QUARANTINED') group by grp;"

# 4. Are retried actions inflating it? Retries re-load the full page.
docker compose exec -T postgres psql -U smm -d smm -c \
  "select error_class, count(*) as retries from action_log
   where status = 'RETRY' and created_at > now() - interval '24 hours'
   group by error_class order by retries desc;"
```

## Remediation

```bash
# A. Page weight: block the heavy asset classes the action does not need.
#    This is the single largest win — a comment does not need the video.
#    Route blocklist at the worker (Playwright request interception) for
#    media/* on action pages, keeping login flows untouched.

# B. Navigation loop: the duration metric shows it — a normal action is a few
#    seconds; a loop runs to timeout.
curl -s localhost:24090/api/v1/query --data-urlencode \
  'query=histogram_quantile(0.95, sum by (le)(rate(smm_action_duration_seconds_bucket[10m])))'

# C. Group concentration: rebalance accounts across groups so no single group
#    carries the whole fleet.
docker compose exec -T postgres psql -U smm -d smm -c \
  "update account set proxy_group_id = '<OTHER_GROUP>'
   where proxy_group_id = '<HOT_GROUP>' and status = 'ACTIVE';"

# D. Ceiling reached and the pool cut off: actions will fail with TRANSIENT
#    connection errors. Pause the affected accounts until the window rolls
#    over rather than burning health score on a proxy that is down.
```

## Verification

```bash
# Egress rate falling:
curl -s localhost:24090/api/v1/query --data-urlencode \
  'query=sum by (proxy_group)(rate(smm_proxy_bytes_used_total[1h]))'
# Affected accounts still healthy (did not burn score on a dead proxy):
curl -s localhost:24090/api/v1/query --data-urlencode 'query=smm_worker_health_score'
```

## Post-mortem prompt

Were the bytes page weight, or the same page loaded many times? The fix is
different: asset blocking versus fixing the navigation loop. See
[README](README.md#post-mortem-prompt).
