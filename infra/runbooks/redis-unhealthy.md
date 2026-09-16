# ALERT: redis-unhealthy

**Severity:** SEV1 — action dispatch stops. Sessions stay logged in, but no
action can be queued, gated, or run.
**Alert source:** compose healthcheck `redis-cli ping` (5 s interval).

## Symptoms

- `make ps` shows redis health `unhealthy` or the service missing.
- Workers are up and heartbeating, but every `BLPOP` times out: queue depth
  flat, no actions executed.
- The API action scheduler logs `ratelimit` / `cooldown` errors, or the
  publisher fails to enqueue.
- `worker` containers restart or exit with a heartbeat failure (see
  [worker-crashloop](worker-crashloop.md)) — the heartbeat is how this surfaces
  first, because redis is what the worker talks to.

## Impact

The control plane is dead while the data plane is fine: accounts stay logged in
(session cookies live on the `sessions` volume, not redis), analytics scrape
history is intact, but **zero actions run**. Jobs stay durable in redis lists —
if redis survives, nothing is lost; if redis is rebuilt empty, pending jobs must
be re-enqueued (see below).

## Likely causes

1. **OOM** — redis has no `maxmemory` cap configured in compose, so it grows
   until the container's limit or the host kills it.
2. **AOF corruption** (`--appendonly yes`) after an unclean colima shutdown —
   redis refuses to boot with a torn appendonly file.
3. **Disk full** on the colima volume holding `redisdata`.
4. **Blocked client** — a `KEYS`/long `LUA` against a big keyspace stalls the
   single-threaded server; `PING` then times out.

## Diagnosis

```bash
# 1. Status and health.
make ps

# 2. The error is usually the last line before the crash loop.
make logs S=redis

# 3. Can we talk to it at all?
docker compose exec -T redis redis-cli ping

# 4. Memory: is it at the wall?
docker compose exec -T redis redis-cli info memory | grep -E 'used_memory_human|maxmemory_human|maxmemory_policy'

# 5. What is filling it? Cooldown + ratelimit keys should be small and TTL'd.
docker compose exec -T redis redis-cli dbsize
docker compose exec -T redis redis-cli --scan --pattern 'smm:ratelimit:*' | wc -l
docker compose exec -T redis redis-cli --scan --pattern 'smm:cooldown:*' | wc -l
docker compose exec -T redis redis-cli --scan --pattern 'queue:action:*' | wc -l

# 6. Slow / blocking commands in flight?
docker compose exec -T redis redis-cli slowlog get 10

# 7. Disk on the data dir.
docker compose exec -T redis df -h /data
```

## Remediation

```bash
# A. If the appendonly file is torn (logs say 'Bad file format' / 'AOF'):
#    truncate the tail and let it replay — losing the last partial write is
#    far better than losing redis entirely.
docker compose exec -T redis sh -c 'cd /data && redis-check-aof --fix appendonly.aof 2>/dev/null; redis-check-rdb dump.rdb 2>/dev/null'
docker compose restart redis
#    If it still will not boot, the AOF is unsalvageable: drop it, accept the
#    in-flight job loss, and re-enqueue (below).
docker compose stop redis
docker volume rm smm_redisdata
make up

# B. Memory pressure (used_memory_human climbing toward the host limit):
#    the queues are the safe thing to drop, not the gates.
docker compose exec -T redis redis-cli info memory | grep used_memory_human
#    ratelimit keys expire on their own window; cooldown keys expire on their
#    window. If a runaway producer is filling queues, drain ONE worker's queue:
#    docker compose exec -T redis redis-cli del queue:action:worker-<id>
#    Then fix the producer (see rate-limit-exhausted) before restoring scale.

# C. Blocked-client stall: restart clears it; find the client first.
docker compose exec -T redis redis-cli client list | grep -E 'age=|idle=' | sort -t= -k2 -rn | head
docker compose restart redis
```

### Re-enqueueing after an empty rebuild

Redis is rebuilt with no queues and no gate state — which is safe: the gates
re-fill on their own as the scheduler runs, and pending `action_job` rows are
the source of truth, not the queues.

```bash
# The action scheduler re-claims due jobs on its next tick; nothing to do
# manually unless it is off. Verify it is running:
make logs S=api | grep 'action scheduler'
# Confirmed dispatch resumes (queues refill from the scheduler):
docker compose exec -T redis redis-cli --scan --pattern 'queue:action:*' | wc -l
```

## Verification

```bash
make ps                                              # redis healthy
docker compose exec -T redis redis-cli ping          # PONG
docker compose exec -T redis redis-cli dbsize        # non-zero once jobs flow
curl -sf localhost:24080/healthz                     # API healthy
docker compose exec -T redis redis-cli llen queue:action:worker-1   # drains as workers consume
```

## Post-mortem prompt

Was redis the cause or the victim? An OOM here usually means an upstream
producer run away — the runbook restarted redis and the page came back, so the
producer is still out there. See [README](README.md#post-mortem-prompt).
