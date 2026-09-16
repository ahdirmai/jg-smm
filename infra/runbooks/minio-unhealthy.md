# ALERT: minio-unhealthy

**Severity:** SEV2 — object storage down. Scrapes stop landing, evidence
screenshots cannot be written, but live actions keep running.
**Alert source:** compose healthcheck `mc ready local` (5 s interval).

## Symptoms

- `make ps` shows minio health `unhealthy`.
- API logs show S3/MinIO put errors; scrape jobs fail to persist raw payloads.
- Workers fail to upload evidence screenshots, but the action itself
  succeeds (the upload is best-effort, not blocking).
- The MinIO console at `http://localhost:24901` does not load; the API port
  `24900` refuses connections.

## Impact

No data loss of *state* — postgres and redis are unaffected, accounts stay
logged in, and actions still execute. What breaks is the evidence trail: raw
payloads, screenshots, and any object the platform writes for audit. Prolonged
outage means silently missing audit data, which for a compliance-facing feature
is worse than an error.

## Likely causes

1. **Disk full** on the colima volume holding `miniodata` — the same single
   host disk as everything else, so this usually follows a postgres/redis
   page.
2. **Bad credentials** — `.env` changed `MINIO_ROOT_USER/PASSWORD` but the
   running containers still hold the old values.
3. **`minio-init` failing or never run** — buckets the API expects do not
   exist, so every put fails.
4. **Volume loss** — the DR case: [dr-drill](dr-drill.md).

## Diagnosis

```bash
# 1. Status + health.
make ps

# 2. Minio's own logs — the error is usually explicit.
make logs S=minio

# 3. Disk on the data dir (the #1 cause).
docker compose exec -T minio df -h /data

# 4. Server reachable on its port, from the host?
curl -sf http://localhost:24900/minio/health/live && echo live || echo DOWN

# 5. Credentials match the ones the API actually holds?
docker compose exec -T minio printenv MINIO_ROOT_USER
docker compose exec -T api printenv S3_ACCESS_KEY

# 6. Do the buckets the API expects exist?
docker compose run --rm --no-deps \
  -e MINIO_ROOT_USER="$(docker compose exec -T minio printenv MINIO_ROOT_USER)" \
  -e MINIO_ROOT_PASSWORD="$(docker compose exec -T minio printenv MINIO_ROOT_PASSWORD)" \
  --entrypoint /bin/sh minio-init -c 'mc alias set local http://minio:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD"; mc ls local'
```

## Remediation

```bash
# A. Disk full: MinIO is the safest plane to reclaim from — raw payloads and
#    screenshots are debug/audit data, not live state.
docker compose exec -T minio du -sh /data/* | sort -h | tail
#    Delete only the oldest objects from the buckets you can afford to lose;
#    do NOT touch /data/.minio.sys.

# B. Credential drift: reconcile .env with what the containers see, then
#    recreate the affected containers so they pick up the values.
make down
make up      # re-reads .env; compose interpolates into both services

# C. Missing buckets: minio-init is idempotent — just rerun it.
docker compose up minio-init
#    Buckets the stack expects: raw-payload, screenshots, sessions
#    (created by the minio-init service in compose.yaml).

# D. Total volume loss — restore from backup, see ../../docs/BACKUP.md:
#    make restore B=latest      (restores minio/<bucket>/ back into MinIO)
```

## Verification

```bash
make ps                                            # minio healthy
curl -sf http://localhost:24900/minio/health/live  # live
# Bucket listing round-trips:
docker compose run --rm --no-deps \
  -e MINIO_ROOT_USER="$(docker compose exec -T minio printenv MINIO_ROOT_USER)" \
  -e MINIO_ROOT_PASSWORD="$(docker compose exec -T minio printenv MINIO_ROOT_PASSWORD)" \
  --entrypoint /bin/sh minio-init -c 'mc alias set local http://minio:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD"; mc ls local/raw-payload'
# The API can put an object end-to-end:
curl -sf localhost:24080/healthz && curl -sf localhost:24080/readyz | grep '"ok"'
```

## Post-mortem prompt

If disk: which plane filled it, and did postgres or redis page first? Three
data planes on one colima disk is a shared fate the runbook should name.
See [README](README.md#post-mortem-prompt).
