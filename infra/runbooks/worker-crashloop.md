# ALERT: worker-crashloop

**Severity:** SEV2 — workers are not consuming. Actions queue up and never run.
**Alert source:** no compose healthcheck exists for `worker`; this fires on the
heartbeat budget (`apps/worker/src/core/heartbeat.ts`) or a human noticing flat
queues. **This is the documented gap** — see
[README](README.md) and [../../docs/SEVERITY.md](../../docs/SEVERITY.md).

## Symptoms

- `make ps` shows `worker` as `Restarting` or repeatedly exiting/respawning.
- Worker logs show `heartbeat budget exhausted, exiting` — 3 consecutive
  heartbeat POST failures to `/internal/heartbeat`.
- Queue depth climbs on redis and never drains:
  `llen queue:action:worker-<n>`.
- `heartbeat` rows stop arriving in postgres for a worker that is nominally
  `RUNNING`.
- Accounts go idle: the reconciler respawns the worker (gen++), it crashes
  again, and `worker.restart_count` climbs.

## Impact

Actions stop for the accounts on the affected workers. Sessions stay logged in
(browser `storageState` is on the `sessions` volume, untouched by a crash) — so
this is recoverable, not destructive. `N` workers down = `N × ≤2` accounts idle
(INFRA_ANALYST §6.1).

## Likely causes

1. **API unreachable** — `API_URL: http://api:8080`; if the API is down the
   heartbeat 4xx/5xx and the worker self-exits. Check
   [api-unhealthy](api-unhealthy.md) first.
2. **Redis unreachable** — the worker needs redis for its queue; see
   [redis-unhealthy](redis-unhealthy.md).
3. **Browser/Xvfb stack failed to start** in the entrypoint (Xvfb, x11vnc,
   websockify) — the entrypoint's `trap cleanup` fires and the container exits.
4. **Login flow wedged** — a parked auth context leaking file descriptors or
   hanging on a platform page; see [worker-auth-failure](worker-auth-failure.md).
5. **OOM** — a headful browser per container is the memory-heavy component
   (INFRA_ANALYST §3.2: worker memory is the sizing bottleneck).

## Diagnosis

```bash
# 1. Which workers are up / looping, and how many replicas are there?
make ps
docker compose ps worker --format '{{.Name}} {{.Status}}'

# 2. Worker logs — the crash reason is on stderr of the last attempt.
make logs S=worker

# 3. Heartbeat rows: who stopped reporting, and when?
docker compose exec -T postgres psql -U smm -d smm -tAc \
  "select worker_id, ts from heartbeat order by ts desc limit 10;"
docker compose exec -T postgres psql -U smm -d smm -c \
  "select name, status, browser_status, restart_count, queue_depth, last_heartbeat, last_error from worker;"

# 4. Queue depth per worker: climbing = no consumption.
#    See README "Common diagnosis commands" for the canonical loop.

# 5. Can the worker reach the API and redis?
docker compose exec -T worker sh -c 'nc -z api 8080 && echo api_ok || echo api_DOWN; nc -z redis 6379 && echo redis_ok || echo redis_DOWN'

# 6. Entrypoint services (Xvfb/x11vnc/websockify) alive inside a running worker?
docker compose exec -T worker sh -c 'pgrep -a Xvfb; pgrep -a x11vnc; pgrep -a websockify; pgrep -a node' 2>/dev/null

# 7. Memory: is colima/the container under pressure?
docker stats --no-stream --format '{{.Name}} {{.MemUsage}}' | grep -E 'worker|smm'
colima ssh -- free -m 2>/dev/null || true
```

## Remediation

```bash
# A. The API or redis is the cause (step 3/5 above): fix that runbook, then the
#    workers recover on their own — no worker-side action needed.

# B. A single wedged replica: restart just that container.
docker compose restart worker
#    All replicas at once:
docker compose up -d --force-recreate --scale worker=3 worker

# C. Entrypoint/browser stack died inside a live container (noVNC black screen):
#    recreate the container; do not try to repair Xvfb in place.
docker compose up -d --force-recreate worker

# D. OOM: workers are the memory bottleneck. Reduce replicas first (cap 3 on
#    16 GB, INFRA_ANALYST §15.2) rather than letting the kernel pick victims.
make down
WORKERS=2 make up

# E. The worker lost its session and is auth-looping — the crash is a symptom.
#    Go to worker-auth-failure.md before restarting further.
```

## Verification

```bash
make ps                                                    # workers Up, not Restarting
# Heartbeats flowing again (this is the real signal):
docker compose exec -T postgres psql -U smm -d smm -tAc \
  "select count(*) from heartbeat where ts > now() - interval '2 minutes';"
# Queues draining:
docker compose exec -T redis redis-cli --scan --pattern 'queue:action:*' | while read -r k; do echo "$k = $(docker compose exec -T redis redis-cli llen "$k")"; done
# noVNC live screen reachable from inside the container:
docker compose exec -T worker sh -c 'nc -z localhost 6080 && echo novnc_ok'
```

## Post-mortem prompt

Which exit path: heartbeat budget, OOM, or browser stack? If heartbeat, the
worker _correctly_ self-exited — the question is why the API was unreachable
for 3 beats. See [README](README.md#post-mortem-prompt).
