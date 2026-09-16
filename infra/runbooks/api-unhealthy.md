# ALERT: api-unhealthy

**Severity:** SEV1 — the API is not serving. Dashboard, workers, everything.
**Alert source:** compose healthcheck `CMD /app/server healthcheck` (10 s
interval, start_period 10 s), which GETs `/healthz` on localhost:8080.

## Symptoms

- `make ps` shows api health `unhealthy` or the service in `Restarting`.
- `curl localhost:24080/healthz` fails or returns non-200.
- Workers cannot register or heartbeat — they fail with connection errors to
  `http://api:8080` and self-exit after their heartbeat budget.
- The web UI cannot reach the API (`NEXT_PUBLIC_API_URL`), so the dashboard is
  blank.

## Impact

Total platform outage from the user's seat. Nothing is on fire inside the data
planes — postgres, redis and minio are likely fine — but nothing reaches them.

## Likely causes

1. **Migration gate**: the `migrate` one-shot service failed, so the API never
   starts (it depends on `service_completed_successfully`).
2. **Config/env**: bad `DATABASE_URL`, a changed `MINIO_*` secret, or a missing
   required env var panics during wiring.
3. **Build/image**: the image is stale or the binary is missing `/app/server`.
4. **Port/bind**: something else holds 8080 inside the container.
5. **Dependency wait**: redis or minio unhealthy makes the API's readiness
   degrade — that is [api-readiness-degraded](api-readiness-degraded.md), not
   this alert; `/healthz` does not depend on them.

## Diagnosis

```bash
# 1. Status: is it unhealthy, or restarting, or gone?
make ps

# 2. The last 40 lines tell you which cause it is. Run this first.
make logs S=api

# 3. Liveness from the host: does /healthz answer at all?
curl -sv localhost:24080/healthz

# 4. Readiness: which dependency is down? (degraded != unhealthy)
curl -s localhost:24080/readyz

# 5. Did migrations complete? The API will not start otherwise.
make migrate-status
make ps | grep migrate

# 6. Can the API even reach its dependencies?
docker compose exec -T api sh -c 'nc -z postgres 5432 && echo pg_ok; nc -z redis 6379 && echo redis_ok; nc -z minio 9000 && echo minio_ok'

# 7. Env as the running container sees it (not as .env says):
docker compose exec -T api printenv DATABASE_URL REDIS_URL S3_ENDPOINT LOG_LEVEL
```

## Remediation

Order matters: the migration gate is the most common cause and the only one
where a restart alone does nothing.

```bash
# A. Migration failed -> the API waits forever on a completed migrate service.
make migrate-status
#    Dirty: roll the bad migration back, fix the SQL, re-run.
make migrate-down N=1
make migrate
#    Then bring the API up; it was never broken.
make up

# B. Env/config wiring panic: the log line names the exact var. Fix .env, then:
make down && make env && make up      # `env` recreates .env if missing

# C. Stale image after a code change:
make build && make up

# D. Nothing in the logs and /healthz 200s from inside but not from the host:
#    the published port mapping is the suspect, not the app.
docker compose port api 8080
make down && make up
```

## Verification

```bash
make ps                                              # api healthy
curl -sf localhost:24080/healthz                     # 200 {"status":"ok"}
curl -s localhost:24080/readyz | grep '"ok"'         # readiness healthy too
# Workers reconnected and heartbeating:
docker compose exec -T postgres psql -U smm -d smm -tAc \
  "select name, status, last_heartbeat from worker order by last_heartbeat desc nulls last limit 5;"
```

## Post-mortem prompt

Did the deployment gate catch this, or did we learn it from a healthcheck page?
A migration that blocks the API from booting is a CI problem as much as an ops
one. See [README](README.md#post-mortem-prompt).
