# ALERT: action-success-rate-low

**Severity:** SEV2 — actions are failing systematically; the fleet is burning
its own cooldown budget without producing results.
**Alert source:** Prometheus `ActionSuccessRateLow` (infra/obs/alerts.yml):
`SUCCESS / total < 0.85` over 10m, computed from `smm_action_total`.
**Metric:** `smm_action_total{status="SUCCESS"}` vs all statuses.

## Symptoms

- Grafana action success rate below 85% and flat, not a single dip.
- `action_log` rows accumulate with `error_class` set to something other than
  empty; the dashboard's failure grouping shows one class dominating.
- Queue depth steady or rising while attempts keep firing (the fleet is busy
  and unproductive at the same time).
- Health scores are falling (`smm_worker_health_score`); if they were not, the
  failures are recent enough that quarantine has not bitten yet.

## Impact

No actions land. Every failed attempt still costs a cooldown slot and proxy
egress, so a low success rate is money spent for zero output — and if the
dominant class is `RATE_LIMIT`, the fleet is one step from a platform throttle
that will make it worse.

## Likely causes

1. **Targets are gone** — a batch of URLs 404'd or the post was deleted. The
   class is usually `UNKNOWN`/`TRANSIENT` and it is batch-specific, not
   fleet-wide.
2. **Platform rate limiting** — class `RATE_LIMIT` dominating. See
   [rate-limit-exhausted](rate-limit-exhausted.md); this alert is its early
   warning.
3. **Auth decay** — class `AUTH` dominating means sessions are expiring faster
   than they are being re-logged. See [worker-auth-failure](worker-auth-failure.md).
4. **Template/ban-word rejections** — the platform accepts the request but
   rejects the content; `error_class` is `UNKNOWN` with a "try again later"-
   style message. Check the template pool for banned phrases.
5. **Proxy degradation** — residential pool failing; `TRANSIENT` with
   connection-style messages. See [proxy-budget-exceeded](proxy-budget-exceeded.md)
   if egress is also high.

## Diagnosis

```bash
# 1. Which error class is dragging the rate down?
#    In Grafana: sum by (error_class)(rate(smm_action_total{status!="SUCCESS"}[10m]))
#    Or from the DB, which carries the message the metric drops:
docker compose exec -T postgres psql -U smm -d smm -c \
  "select error_class, count(*) as n from action_log
   where status in ('FAILED','RETRY') and created_at > now() - interval '30 minutes'
   group by error_class order by n desc;"

# 2. See the actual messages behind the top class (the classifier is coarse;
#    the free text says which page the action died on).
docker compose exec -T postgres psql -U smm -d smm -c \
  "select a.type, al.error, al.target_url from action_log al
   join action_job a on a.id = al.action_job_id
   where al.status = 'FAILED' and al.error_class = '<TOP_CLASS>'
   order by al.created_at desc limit 10;"

# 3. Is it one account or many? One account = session; many = platform/proxy.
docker compose exec -T postgres psql -U smm -d smm -c \
  "select a.username, a.platform, count(*) as failures
   from action_log al join action_job j on j.id = al.action_job_id
   join account a on a.id = j.account_id
   where al.status in ('FAILED','RETRY') and al.created_at > now() - interval '30 minutes'
   group by a.username, a.platform order by failures desc limit 10;"

# 4. Live success rate from the metric itself:
curl -s localhost:24090/api/v1/query --data-urlencode \
  'query=sum(rate(smm_action_total{status="SUCCESS"}[10m]))/clamp_min(sum(rate(smm_action_total[10m])),1)'
```

## Remediation

```bash
# A. RATE_LIMIT dominating: slow the fleet. Cooldown is per-account; reduce
#    parallelism so each account's rate stays under the platform cap.
docker compose exec -T postgres psql -U smm -d smm -c \
  "update team_config set action_batch_parallelism = 1;" # then restart the API

# B. AUTH dominating: re-login the affected accounts rather than retrying.
#    See worker-auth-failure.md; the sessions volume is per-worker.

# C. Batch of dead targets: the jobs will keep failing. Cancel the batch
#    instead of letting it consume the cooldown budget.
docker compose exec -T postgres psql -U smm -d smm -c \
  "update action_job set status = 'CANCELLED', finished_at = now()
   where status in ('PENDING','RUNNING') and error like '%not found%';"

# D. Content rejections: pull the template pool and audit it against the
#    platform's current filter, then re-enqueue. Templates are the only
#    payload the worker does not generate on the fly.
```

## Verification

```bash
# Success rate back above 85% and holding:
curl -s localhost:24090/api/v1/query --data-urlencode \
  'query=sum(rate(smm_action_total{status="SUCCESS"}[10m]))/clamp_min(sum(rate(smm_action_total[10m])),1)'
# No accounts newly quarantined in the window:
docker compose exec -T postgres psql -U smm -d smm -tAc \
  "select count(*) from account where status = 'QUARANTINED';"
```

## Post-mortem prompt

Which class, and was it one account or many? A fleet-wide single-class failure
is a platform or proxy problem; a scattered one is targets or content. See
[README](README.md#post-mortem-prompt).
