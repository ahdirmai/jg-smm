# ALERT: queue-depth-high

**Severity:** SEV2 — the action queue is backing up: either the workers stopped
consuming or they are consuming and failing.
**Alert source:** Prometheus `QueueDepthBackingUp` (infra/obs/alerts.yml):
`avg_over_time(smm_queue_depth[15m]) > 50`.
**Metric:** `smm_queue_depth`, set from `action_job` pending counts.

## Symptoms

- Pending `action_job` rows above 50 and flat or rising for 15 minutes.
- `make logs S=worker` shows either silence (not consuming) or a stream of
  failures (consuming and failing).
- Workers report `BUSY` but `jobs_done` in heartbeats is not advancing.

## Impact

Actions are not landing. Depth that rises while attempts fire is the ban-risk
signature: the account is being asked to post repeatedly while already failing,
which is exactly the pattern the platform's anti-spam heuristics score.

## Likely causes

1. **Workers down** — no consumption at all. See
   [worker-crashloop](worker-crashloop.md); check heartbeats first.
2. **Workers failing every attempt** — depth drains only on success; see
   [action-success-rate-low](action-success-rate-low.md).
3. **Action scheduler stopped** — `ACTION_INTERVAL_SECONDS=0` means the
   scheduler loop never started, so nothing claims pending rows even with
   healthy workers.
4. **A single hot account** — one account holds a large batch and its
   cooldown serialises everything behind it.

## Diagnosis

```bash
# 1. Depth by account: is it one account or many?
docker compose exec -T postgres psql -U smm -d smm -c \
  "select a.username, a.platform, count(*) as pending
   from action_job j join account a on a.id = j.account_id
   where j.status = 'PENDING' group by a.username, a.platform
   order by pending desc limit 10;"

# 2. Are workers consuming? Heartbeats with jobs_done advancing = yes.
docker compose exec -T postgres psql -U smm -d smm -c \
  "select name, status, browser_status, queue_depth, last_heartbeat,
          last_action_at, last_error from worker order by last_heartbeat desc;"

# 3. Is the scheduler running at all? (env on the API container)
docker compose exec -T api printenv ACTION_INTERVAL_SECONDS

# 4. Redis-side depth, the worker's own view:
docker compose exec -T redis redis-cli --scan --pattern 'queue:action:*' | while read -r k; do echo "$k = $(docker compose exec -T redis redis-cli llen "$k")"; done

# 5. Recent attempt outcomes: consuming-but-failing shows up here.
docker compose exec -T postgres psql -U smm -d smm -c \
  "select status, error_class, count(*) from action_log
   where created_at > now() - interval '15 minutes'
   group by status, error_class order by count desc;"
```

## Remediation

```bash
# A. Scheduler off (step 3 shows 0): enable it and restart the API.
#    It is opt-in by design (a local `make up` should not start posting).
ACTION_INTERVAL_SECONDS=30 make up

# B. Workers down: worker-crashloop.md — they will drain the queue themselves
#    once healthy.

# C. One hot account (step 1 shows one row dominating): its cooldown serialises
#    the batch. Split the batch across more accounts, or raise the account's
#    rate headroom if the platform allows it.

# D. Consuming but failing: action-success-rate-low.md. Do not add workers;
#    more consumers of a failing account is a ban, not throughput.
```

## Verification

```bash
# Depth falling (Grafana or raw):
curl -s localhost:24090/api/v1/query --data-urlencode 'query=avg_over_time(smm_queue_depth[15m])'
# Pending rows draining:
docker compose exec -T postgres psql -U smm -d smm -tAc \
  "select count(*) from action_job where status = 'PENDING';"
```

## Post-mortem prompt

Was depth rising with attempts firing, or in silence? Silence = worker problem;
firing = the account or target was failing. See
[README](README.md#post-mortem-prompt).
