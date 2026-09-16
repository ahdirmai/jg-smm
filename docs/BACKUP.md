# Backup and disaster recovery

The backup strategy for MVP-1-SMM. Scripts: [`infra/backup/`](../infra/backup).
Runbooks: [`infra/runbooks/`](../infra/runbooks/README.md). Severity contract:
[`SEVERITY.md`](SEVERITY.md).

All commands run against the `smm` compose project (colima, linux/arm64 —
`INFRA_ANALYST.md`). No host-level dependencies beyond `docker`.

## Data planes and how each is backed up

| Plane                          | Volume (compose)         | Backup method                              | Restore                              |
| ------------------------------ | ------------------------ | ------------------------------------------ | ------------------------------------ |
| Postgres / TimescaleDB         | `pgdata`                 | `pg_dump -Fc` + WAL archive snapshot       | `pg_restore` into a recreated DB     |
| MinIO object data              | `miniodata`              | `mc cp --recursive` per bucket             | `mc cp --recursive` back into buckets |
| Worker sessions (`storageState`)| `sessions`              | `tar` of the volume into the backup dir    | `tar` back onto the volume           |

`redisdata` (queues + cooldown/ratelimit keys) and `screenshots` (regenerable
evidence) are **not** backed up: a redis loss means re-enqueued jobs and
re-gated actions, which the reconciler and the schedulers already recover from.
`screenshots` is a debug trail, not state.

## Usage

```bash
make backup              # timestamped backup under infra/backup/backups/<ts>/
make restore             # restore the `latest` backup (confirms first)
make restore B=20260101T120000Z
make drill               # DR drill: wipe every app volume, restore, verify
```

Environment overrides: `SMM_BACKUP_DIR` (backup location, absolute),
`SMM_BACKUP_NAME` (name the directory instead of a timestamp),
`SMM_BACKUP_KEEP` (retention, default 7), `SMM_YES=1` (skip the confirmation
prompt), `SMM_SKIP_BACKUP=1` (drill only: reuse the existing backup).

A backup directory is:

```text
backups/20260101T120000Z/
  postgres.dump       # pg_dump -Fc, restored with pg_restore
  pg_version.txt      # server version, checked before restore
  wal/                # archived WAL segments at backup time
  minio/<bucket>/     # one subdir per bucket
  sessions.tar        # worker browser sessions
  manifest.txt        # the restore-time integrity contract
  latest -> 20260101T120000Z
```

## The manifest is the contract

`manifest.txt` records the row count, the user-table count, per-bucket object
counts and the session-file count. `restore.sh` re-measures those numbers after
restoring and fails the restore if any differ. A "successful" restore that does
not match the manifest is a failed restore.

## WAL archiving and PITR

`backup.sh` enables WAL archiving in the running postgres with `ALTER SYSTEM`:

```sql
ALTER SYSTEM SET archive_mode = 'on';
ALTER SYSTEM SET archive_command = 'test -f /var/lib/postgresql/data/pg_wal_archive/%f || cp %p /var/lib/postgresql/data/pg_wal_archive/%f';
ALTER SYSTEM SET archive_timeout = '300s';
```

`archive_timeout` bounds the RPO at ~5 minutes of WAL activity (INFRA_ANALYST
§6.3). Enabling `archive_mode` restarts postgres once, ~10 s of downtime; every
backup after the first is restart-free. The archive dir is created and chowned
to the postgres user before the restart, so the archiver never fails on a
missing directory.

**Where the WAL goes is deliberately not where the dump goes.** The archive
directory lives inside the `pgdata` volume because `archive_command` runs inside
the postgres container and the backup scripts cannot add a volume mount to an
already-running service. The backup copies the segments out to `wal/` — the
copy is the durable artifact, the in-volume dir is scratch.

> **ponytail:** PITR to an arbitrary timestamp needs a physical base backup
> (`pg_basebackup`) plus a recovery container, not a logical dump. The current
> pair (logical dump + archived WAL) covers the disaster scenario the AC names —
> total volume loss — inside the 30-minute RTO. Add `pg_basebackup` and a
> `recovery.signal` restore path when a sub-day recovery point is required.

## RTO / RPO

| Metric | Target | Enforced by                                   |
| ------ | ------ | --------------------------------------------- |
| RTO    | 30 m   | `make drill` prints elapsed restore time      |
| RPO    | 5 m    | `archive_timeout=300s` + backup frequency     |

The drill measures the AC. Its clock starts when the stack goes down and stops
when verification passes, and it exits non-zero if the total exceeds 1800 s.

## The drill

`make drill` simulates the disaster, not a restore dry-run:

1. `backup.sh` — a fresh backup, so the restore target is the live state.
2. `docker compose down` and `docker volume rm` of **every** app volume.
3. Data plane back up on empty volumes; `restore.sh` restores onto it.
4. Verify: `pg_isready`, a row-count sanity query against the manifest, a
   MinIO bucket listing, and the API health probe.
5. Print the elapsed restore time; exit non-zero if it exceeds 30 minutes.

`drill.sh` refuses to run without confirmation (or `SMM_YES=1`) and it destroys
data. Run it only on a stack you can lose — for a local colima dev stack that
means being sure `infra/backup/backups` holds a good backup first.

## Verification cadence

- **Every backup** — the manifest numbers are the check; a backup whose
  `pg_dump` exited non-zero never gets a manifest because the script aborts.
- **`make drill`** — the full restore path, scheduled quarterly
  (INFRA_ANALYST §6.3 / INFRA-13). A drill that restores nothing proves
  nothing; the wipe is the point.
- **`make restore B=<name>`** into an isolated project is the lighter-weight
  "did this backup work" check between drills.

## Retention

`SMM_BACKUP_KEEP=7` timestamped directories, newest wins, `latest` excluded
from pruning. Backups are plain directories: `rm -rf` is the retention policy,
and offloading the whole `infra/backup/backups` tree to object storage is a
`cp -a`, not a feature.

## Limitations (what this does not do)

- **No encrypted-at-rest backup target.** The backup dir is host filesystem.
  Until a KMS-backed target exists, treat `sessions.tar` (live platform
  cookies) as a secret: `chmod 700 infra/backup/backups`, and never copy a
  backup off the machine unencrypted.
- **No streaming replication.** Postgres is a single replica by design (an
  accepted MVP SPOF, INFRA_ANALYST §6.2). Backup + WAL is the mitigation.
- **No redis backup.** Intentional (see above); the loss mode is benign.
- **`mc cp --recursive` is not versioned.** A MinIO object overwritten between
  backups is overwritten in the backup too. MinIO bucket versioning is the
  INFRA_ANALYST §6.3 answer; it is one `mc version` call away when needed.
