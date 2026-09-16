# PROCEDURE: dr-drill

**Severity:** SEV3 when overdue — the restore path is unproven until a drill
passes.
**Purpose:** prove the RTO/RPO contract from [../../docs/BACKUP.md](../../docs/BACKUP.md)
by destroying data and bringing it back. Scheduled quarterly (INFRA-13).

> **This procedure destroys data by design.** `make drill` wipes every app
> volume. Run it only against a stack you can lose. On a local colima dev
> stack that means the backup it takes at step 1 must succeed — verify the
> manifest before the wipe, not after.

## When to run

- Quarterly, per the capacity plan (INFRA-13).
- After any change to `infra/backup/*.sh`, `compose.yaml` volumes, or the
  postgres/minio/sessions data planes.
- After any postgres major-version bump (the drill catches `pg_restore`
  version drift before a real disaster does).

## Prerequisites

```bash
# 1. The stack must be up — the drill backs up the live state first.
make ps
# 2. Disk: the drill needs room for a backup AND the restore.
df -h .
# 3. Read the script you are about to trust your data to.
less infra/backup/drill.sh
```

## Running it

```bash
make drill                 # prompts; SMM_YES=1 skips the prompt
# Or name the backup to restore from (default: latest):
# SMM_SKIP_BACKUP=1 make drill     # reuse the existing latest backup
```

The drill measures the AC: the clock starts when the stack goes down and stops
when verification passes. It exits non-zero if the total exceeds 1800 s, so the
"restore < 30 min" acceptance test is a machine check, not a belief.

## What it does, step by step

1. **Backup** — `backup.sh` runs first so the restore target is the live state.
2. **Disaster** — `docker compose down`, then `docker volume rm` of
   `smm_pgdata`, `smm_redisdata`, `smm_miniodata`, `smm_sessions`,
   `smm_screenshots`. Every app volume, so the restore cannot accidentally read
   leftover state.
3. **Data plane up on empty volumes** — postgres, redis, minio started fresh.
4. **Restore** — `restore.sh` recreates the DB, `pg_restore`s the dump, copies
   buckets back into MinIO, unpacks `sessions.tar`, and verifies against the
   manifest.
5. **Verify** — `pg_isready`, a row-count sanity query, a MinIO bucket listing,
   and the API health probe.
6. **Report** — prints the elapsed restore time and the pass/fail against
   1800 s.

## Verification (the drill's own checks; re-run by hand if needed)

```bash
make ps                                                  # all services healthy
docker compose exec -T postgres pg_isready -U smm -d smm
# Row-count sanity: user tables and rows match the manifest exactly.
diff <(grep -E '^postgres_(tables|rows)=' infra/backup/backups/latest/manifest.txt) \
     <(docker compose exec -T postgres psql -U smm -d smm -tAc \
        "select 'postgres_tables=' || count(*) from information_schema.tables
          where table_schema not in ('pg_catalog','information_schema',
            '_timescaledb_internal','_timescaledb_functions','timescaledb_catalog',
            'timescaledb_information','timescaledb_experimental')
          and table_type = 'BASE TABLE';
         select 'postgres_rows=' || coalesce(sum(n_live_tup),0)::bigint from pg_stat_user_tables;")
# MinIO buckets present and non-empty:
docker compose run --rm --no-deps \
  -e MINIO_ROOT_USER="$(docker compose exec -T minio printenv MINIO_ROOT_USER)" \
  -e MINIO_ROOT_PASSWORD="$(docker compose exec -T minio printenv MINIO_ROOT_PASSWORD)" \
  --entrypoint /bin/sh minio-init -c 'mc alias set local http://minio:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD"; mc ls local'
# Sessions restored onto the volume:
docker compose exec -T worker sh -c 'ls -la /data/sessions/'
# API serving on top of the restored data:
curl -sf localhost:24080/healthz && curl -s localhost:24080/readyz | grep '"ok"'
```

## If the drill fails

The drill exits non-zero with the failing step named. The stack is likely down
at that point — recover in this order:

```bash
# 1. Data plane up (empty but healthy).
make up
# 2. Migrations (fresh DB) so the API can boot.
make migrate
# 3. Restore from the backup the drill took — this is the path being tested,
#    so use it.
make restore
# 4. If restore itself is the failure, do NOT retry blindly: the backup is
#    still on disk. Diagnose:
#       -> postgres-unhealthy.md   (DB will not accept the restore)
#       -> backup-failure.md       (manifest missing / dump bad)
#       -> minio-unhealthy.md      (bucket restore failed)
make ps
```

Common failures, in order of likelihood:

- **Images missing after `make down`** — `down` removes containers, and a fresh
  `make up` needs the images built. If the stack was never `make build`-ed on
  this host, do that first.
- **`pg_restore` version mismatch** — the dump's `pg_version.txt` versus the
  running image. Both come from the same image, so this means the image was
  bumped without a drill.
- **Timescale hypertables restore out of order** — the restore recreates
  `timescaledb` extension objects; if the dump order changed, the manifest
  table count still matches but the hypertable definition can fail. Check
  `restore.sh` stderr, not just the manifest.
- **Disk** — a backup plus a restore needs roughly 2x the data size.

## Post-mortem prompt

Did the restore complete inside 30 minutes, and did the manifest match? If the
time budget blew, which step owned it — the dump, the MinIO copy, or the
verification? The drill's own elapsed-time output answers this; quote it in the
write-up. See [README](README.md#post-mortem-prompt).
