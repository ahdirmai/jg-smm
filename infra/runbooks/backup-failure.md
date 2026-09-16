# ALERT: backup-failure

**Severity:** SEV2 — the RPO is at risk. No data is lost *yet*, but the restore
path is now unproven.
**Alert source:** `make backup` (or the scheduled `infra/backup/backup.sh`)
exited non-zero. There is no healthcheck for this; the failing cron/ci job *is*
the alert.

## Symptoms

- `infra/backup/backups/` has no new directory since the last successful run.
- The most recent directory exists but has no `manifest.txt` — the backup died
  partway.
- `latest` points at a directory older than the backup interval.

## Impact

Nothing user-visible. That is the trap: the platform is healthy while the
safety net quietly rots. A backup that has not succeeded in N hours means the
worst-case RPO is now N hours, not 5 minutes.

## Likely causes

1. **Stack not up** — `backup.sh` requires postgres and minio running.
2. **`pg_dump` version drift** — the dump client and server are the same image
   in this setup, so this only happens if the timescale image was bumped
   without a drill.
3. **WAL archive enablement failed** — `ALTER SYSTEM` + restart did not come
   back (see [postgres-unhealthy](postgres-unhealthy.md)).
4. **Disk full** — the backup target and the data volumes share one colima
   disk.
5. **Permission** — the archive dir is not owned by postgres, so
   `archive_command` fails forever and WAL piles up in `pg_wal`.

## Diagnosis

```bash
# 1. What does the last backup dir look like? Missing manifest = partial.
ls -la infra/backup/backups/latest/
cat infra/backup/backups/latest/manifest.txt 2>/dev/null

# 2. Rerun in the foreground — the error is on stderr.
bash infra/backup/backup.sh

# 3. Are the two things the backup needs even up?
make ps
docker compose exec -T postgres pg_isready -U smm -d smm
docker compose exec -T minio mc ready local

# 4. Disk for the backup target.
df -h infra/backup/backups

# 5. WAL archive state (the step most likely to have failed).
docker compose exec -T postgres psql -U smm -d smm -tAc "show archive_mode; show archive_command;"
docker compose exec -T postgres ls -la /var/lib/postgresql/data/pg_wal_archive 2>/dev/null
docker compose exec -T postgres psql -U smm -d smm -tAc "select * from pg_stat_archiver;"

# 6. MinIO reachable from the mc job container?
docker compose run --rm --no-deps --entrypoint /bin/sh minio-init \
  -c 'mc alias set local http://minio:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" && mc ls local'
```

## Remediation

```bash
# A. Stack down — bring it up, then back up again.
make up
bash infra/backup/backup.sh

# B. Partial directory from the failed run: delete it (it has no manifest, so
#    it is not a valid restore source) and rerun.
rm -rf infra/backup/backups/<partial-timestamp>
bash infra/backup/backup.sh

# C. Archive dir ownership (the archive_command cannot write):
docker compose exec -T postgres mkdir -p /var/lib/postgresql/data/pg_wal_archive
docker compose exec -T postgres chown postgres:postgres /var/lib/postgresql/data/pg_wal_archive
#    archive_mode is on but the command is wrong? Reset and let backup.sh
#    re-apply it:
docker compose exec -T postgres psql -U smm -d smm -c "alter system reset archive_command;"
docker compose restart postgres
bash infra/backup/backup.sh

# D. Disk full on the backup target: prune old backups (newest 7 are kept by
#    default), then rerun.
ls -1 infra/backup/backups | grep -v '^latest$' | sort | head -5
#    rm -rf the oldest entries by hand if SMM_BACKUP_KEEP pruning was disabled.
```

## Verification

A backup is good only when the next two both pass:

```bash
# 1. The manifest contract is complete and recent.
cat infra/backup/backups/latest/manifest.txt
ls -la infra/backup/backups/latest/postgres.dump infra/backup/backups/latest/sessions.tar

# 2. The dump actually restores — the only real test.
make drill                 # full wipe + restore + verify (destroys data)
#    or the lighter path in an isolated project:
#    SMM_BACKUP_DIR=/tmp/drill make restore B=latest
```

> No successful backup in 24 h = treat it as SEV1. The RPO contract in
> [docs/BACKUP.md](../../docs/BACKUP.md) is the reason this alert exists.

## Post-mortem prompt

How long was the platform running with an unproven restore path before someone
noticed? If the answer is "until the drill", the failure detection is the bug —
a backup that fails silently is worse than no backup.
See [README](README.md#post-mortem-prompt).
