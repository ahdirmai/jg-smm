# ALERT: cooldown-stalled

**Severity:** SEV3 — the cooldown gate blocks action dispatch for a target, so
its actions wait. No data is lost; the account is paced, not broken.
**Alert source:** `apps/api/internal/adapter/cooldown.go` — `Acquire()` returns
`false` inside the window. Not paged today; surfaces as the same flat dispatch
as [rate-limit-exhausted](rate-limit-exhausted.md), and the two are easy to
confuse — the key pattern is how you tell them apart.
**Gate:** only active when `ACTION_INTERVAL_SECONDS > 0`.

## Symptoms

- Actions for one account/target sit pending while the _same platform's_ other
  accounts proceed (unlike rate-limit, which is per-platform).
- `queue_depth` flat for one worker; no error in the API logs — the scheduler
  correctly declines to enqueue.
- `smm:cooldown:*` keys present with long TTLs.

## Impact

Slowed actions for one account. The gate exists to keep one target from being
hammered; a stall means the pacing is longer than the action cadence needs.

## Likely causes

1. **Window too long** — the configured cooldown exceeds the real need, so
   every action waits.
2. **Target churn** — the key is `sha256(targetKey)`, so a different target URL
   is a different slot; a bug that re-targets the same content under new URLs
   defeats the gate and, conversely, a small target set means one slot gates
   everything.
3. **Key leak** — a cooldown written for a target whose action failed is never
   cleared early (there is no failure path that releases the slot).
4. **Redis sick** — `SetNX` errors are surfaced as gate failures to the caller;
   a persistent redis fault can look like a permanent stall.
5. **Empty account/target** — a malformed enqueue is refused by design (the
   guard returns an error, not a silent pass).

## Diagnosis

```bash
# 1. Distinguish from rate-limit FIRST: cooldown keys are per account+target.
docker compose exec -T redis redis-cli --scan --pattern 'smm:cooldown:*' | wc -l
docker compose exec -T redis redis-cli --scan --pattern 'smm:ratelimit:*' | wc -l

# 2. Key shape: smm:cooldown:comment:<accountId>:<sha256(target)>
docker compose exec -T redis redis-cli --scan --pattern 'smm:cooldown:*' | head -5

# 3. Which accounts are gated, and how much longer?
docker compose exec -T redis redis-cli --scan --pattern 'smm:cooldown:*' | while read -r k; do echo "$k ttl=$(docker compose exec -T redis redis-cli ttl "$k")"; done

# 4. Cross-reference the gated accounts to pending work:
docker compose exec -T postgres psql -U smm -d smm -tAc \
  "select account_id, status, count(*) from action_job group by account_id, status order by count(*) desc limit 10;"

# 5. The configured cooldown window:
docker compose exec -T api printenv ACTION_INTERVAL_SECONDS | head -1
#    (ACTION_COOLDOWN_SECONDS is the configured window — check your config
#     source; it is read by the action scheduler at wiring time)

# 6. Redis healthy? A failing SetNX reads as a permanent stall.
docker compose exec -T redis redis-cli ping
make logs S=api | grep -i 'cooldown' | tail -20
```

## Remediation

```bash
# A. Wait it out: the TTL is the countdown, and the action runs on the next
#    scheduler tick after expiry. This is the gate working.
docker compose exec -T redis redis-cli ttl smm:cooldown:comment:<accountId>:<hash>

# B. Window too long: shorten the configured cooldown and restart the loop.
#    Edit ACTION_COOLDOWN_SECONDS in the config source, then:
make down && make up

# C. A stuck key from a failed action that never released: delete it. The next
#    tick re-acquires it if the action genuinely needs cooling.
docker compose exec -T redis redis-cli del smm:cooldown:comment:<accountId>:<hash>

# D. Target churn bug (many hashes for the same logical target): the fix is in
#    the caller's target key construction, not here. Verify the hashes are
#    stable for a repeated target:
docker compose exec -T redis redis-cli --scan --pattern 'smm:cooldown:*' | sort | uniq -c | sort -rn | head

# E. Redis faults masquerading as a stall: fix redis first.
#    -> redis-unhealthy.md
```

## Verification

```bash
# The gated key expired or was removed:
docker compose exec -T redis redis-cli --scan --pattern 'smm:cooldown:*' | wc -l
# The account's actions complete:
docker compose exec -T postgres psql -U smm -d smm -tAc \
  "select account_id, status, count(*) from action_job group by account_id, status;"
# Verdicts are landing (action_log rows appear for completed attempts):
docker compose exec -T postgres psql -U smm -d smm -tAc \
  "select count(*) from action_log where created_at > now() - interval '10 minutes';"
```

## Post-mortem prompt

Was the cooldown protecting the target, or just hiding a dispatch problem? If
the same target needs its slot cleared repeatedly, the action cadence is the
bug, not the window. See [README](README.md#post-mortem-prompt).
