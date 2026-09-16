# ALERT: rate-limit-exhausted

**Severity:** SEV3 — the per-platform rate budget is spent, so the action
scheduler gates every dispatch. Actions wait, nothing is broken.
**Alert source:** `apps/api/internal/adapter/ratelimit.go` — `Allow()` returns
`false` with the TTL until reset. Not paged today; surfaces as a flat action
throughput and `queue_depth` that does not drain.
**Gate:** only active when `ACTION_INTERVAL_SECONDS > 0`.

## Symptoms

- Actions stay pending for one platform while the other platform proceeds
  (budgets are per-platform, never global).
- The action scheduler logs nothing alarming — it is correctly _not_ enqueuing.
- `queue_depth` flat on the affected workers; `action_job` rows sit in a
  non-terminal status.
- The ratelimit key exists on redis with a short TTL.

## Impact

Slower actions on one platform. No data loss, no state change — this is the
gate working as designed. The platform account stays safe from its own
automation, which is the point of the budget.

## Likely causes

1. **Genuine platform throttling** — the configured hourly budget is too high
   for the account's standing. The correct outcome; tune the budget down.
2. **Budget too low** — the limit was configured below real need, so it is
   always exhausted.
3. **Boundary rollover** — a fixed window can overspend by at most `limit`
   once at a window boundary (documented in `ratelimit.go`).
4. **Window quantisation** — the key is quantised to the hour, so a budget set
   on a shorter window still consumes a full hour of key lifetime.
5. **Clock/key skew** — redis and the API disagreeing on TTL (rare; check redis
   health if suspected).

## Diagnosis

```bash
# 1. The keys are the whole story: which platform is gated, and until when?
docker compose exec -T redis redis-cli --scan --pattern 'smm:ratelimit:*'
#    Key shape: smm:ratelimit:<platform>:1h   (quantised to the hour)
docker compose exec -T redis redis-cli get smm:ratelimit:instagram:1h
docker compose exec -T redis redis-cli ttl smm:ratelimit:instagram:1h

# 2. Configured budget vs configured limit (the scheduler's config):
docker compose exec -T api printenv ACTION_INTERVAL_SECONDS ACTION_BATCH_PARALLELISM

# 3. Is dispatch actually happening for the other platform?
make logs S=api | grep -i 'scheduler\|enqueue' | tail -20

# 4. Job statuses: how many are waiting, and for which platform?
docker compose exec -T postgres psql -U smm -d smm -tAc \
  "select platform, status, count(*) from action_job group by platform, status;"

# 5. Queue depth per worker (flat = gated, draining = working):
#    see README "Common diagnosis commands".

# 6. Redis healthy? A sick redis makes the gate look exhausted (Allow errors
#    are not retried — the job is simply not dispatched this tick).
docker compose exec -T redis redis-cli ping
```

## Remediation

```bash
# A. Wait it out — the documented, correct fix when the budget is genuinely
#    spent. The TTL above is the exact countdown; actions resume by themselves.
docker compose exec -T redis redis-cli ttl smm:ratelimit:instagram:1h

# B. The budget is wrong: tune it in config and restart the scheduler loop
#    (the scheduler reads the limit at wiring time).
#    Edit the ACTION_RATE_LIMITS setting in your config source, then:
make down && make up

# C. Mis-set key blocking a platform after an incident: clear it and let the
#    window re-anchor on the next action. Use sparingly — this defeats the
#    platform-safety purpose of the gate.
docker compose exec -T redis redis-cli del smm:ratelimit:instagram:1h

# D. ACTION_INTERVAL_SECONDS=0: the scheduler is off, so nothing is gated and
#    nothing is dispatched either. This is the local default — not an alert.
grep ACTION_INTERVAL_SECONDS .env 2>/dev/null || echo 'action scheduler off (default)'
```

## Verification

```bash
# The key expired or dropped below the limit:
docker compose exec -T redis redis-cli get smm:ratelimit:instagram:1h
# Actions are flowing for the platform again:
docker compose exec -T postgres psql -U smm -d smm -tAc \
  "select platform, status, count(*) from action_job group by platform, status;"
docker compose exec -T postgres psql -U smm -d smm -c \
  "select name, last_action_at, queue_depth from worker;"
# Queue depth draining — see README "Common diagnosis commands".
```

## Post-mortem prompt

Was the budget tuned to the platform's tolerance, or to our impatience? A gate
that is always exhausted means the configured throughput is a fiction.
See [README](README.md#post-mortem-prompt).
