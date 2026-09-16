# ALERT: api-readiness-degraded

**Severity:** SEV2 — the API is up (`/healthz` 200) but `/readyz` reports
`degraded`: at least one dependency check is `down`.
**Alert source:** `apps/api/internal/service/health.go` via the `/readyz`
endpoint. Liveness and readiness are deliberately separate — `/healthz` never
depends on downstreams, so this alert can fire while the process is perfectly
healthy.

## Symptoms

- `curl -s localhost:24080/readyz` returns `503` with
  `"status":"degraded"` and a `checks` map naming the failing dependency.
- `curl localhost:24080/healthz` still returns `200` — the API process is up.
- Some endpoints work, others fail with the dependency's error.
- `make ps` may show the API itself as **healthy** (the compose healthcheck
  probes `/healthz`, not `/readyz`) — which is exactly why this needs its own
  alert: compose will not page you for it.

## Impact

Partial. Whatever the failing dependency serves is broken; the rest works. The
danger is silent degradation: a UI that mostly loads while one plane is dark.

## Likely causes

The `checks` map names the culprit directly — there is no guessing here:

1. `postgres` down → see [postgres-unhealthy](postgres-unhealthy.md).
2. `redis` down → see [redis-unhealthy](redis-unhealthy.md).
3. `minio` / S3 down → see [minio-unhealthy](minio-unhealthy.md).
4. **Network/DNS** inside the compose network (rare; colima restart).
5. A check whose client was **never wired**: `health.go` skips nil checkers
   silently, so a missing dependency registration looks like "all green" —
   if a check is absent from the map, that is a wiring bug, not health.

## Diagnosis

```bash
# 1. The map IS the diagnosis. Run this one command first.
curl -s localhost:24080/readyz | jq .      # no jq? read the JSON directly

# 2. Confirm liveness is fine — this distinguishes degraded from unhealthy.
curl -s localhost:24080/healthz

# 3. Cross-check with compose health (postgres/redis/minio rows).
make ps

# 4. Whichever check said down — verify it independently from the API's side:
docker compose exec -T api sh -c 'nc -z postgres 5432 && echo pg_ok || echo pg_DOWN'
docker compose exec -T api sh -c 'nc -z redis 6379 && echo redis_ok || echo redis_DOWN'
docker compose exec -T api sh -c 'nc -z minio 9000 && echo minio_ok || echo minio_DOWN'

# 5. Is the checker actually registered? (a missing key is a wiring bug)
make logs S=api | grep -i 'redis init\|health'
```

## Remediation

Treat the named dependency as the incident; this runbook is only the triage:

```bash
# The map said postgres: -> postgres-unhealthy.md
# The map said redis:    -> redis-unhealthy.md   (note: redis is registered
#                          only when ACTION_INTERVAL_SECONDS > 0 — if the
#                          action scheduler is off, redis is not checked)
# The map said minio:    -> minio-unhealthy.md

# A dependency that is genuinely fine but unreachable from the API container
# is a networking fault — recreate the network:
make down && make up

# If a check key is MISSING from the map entirely (not "down", absent):
# the checker was never registered. That is a code bug in main.go wiring,
# not an ops fix — file it and move this alert to SEV4 noise in the meantime.
```

## Verification

```bash
# Ready again: 200 and every check "up".
curl -sf localhost:24080/readyz
curl -s localhost:24080/readyz | grep -c '"up"'      # == number of registered checks
make ps                                              # all data planes healthy
# Business path end-to-end:
docker compose exec -T postgres psql -U smm -d smm -tAc "select count(*) from heartbeat;"
```

## Post-mortem prompt

Which check fired, and how long did the degraded state last before someone saw
it? If compose never paged for it, the alerting gap — not the dependency — is
the finding. See [README](README.md#post-mortem-prompt).
