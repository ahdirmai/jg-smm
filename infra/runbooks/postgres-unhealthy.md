# ALERT: postgres-unhealthy

**Severity:** SEV1 — total outage. No reads, no writes, no logins.
**Alert source:** compose healthcheck `pg_isready -U smm -d smm` (5 s interval).
**MTTR budget:** 5 minutes to mitigate. This is the fastest-failing page in the
stack.

## Symptoms

- `make ps` shows postgres health `unhealthy` or the service missing.
- API healthcheck failing (see [api-unhealthy](api-unhealthy.md)) — the API
  cannot start without postgres.
- Workers idle with nothing to consume; no action is dispatched anywhere.
- Every page that touches the DB returns 5xx; the dashboard never loads.

## Impact

The whole platform. Postgres is the accepted MVP SPOF (INFRA_ANALYST §6.2):
write capability exists only here. Every minute down is a minute of lost
scrapes, missed action windows and stalled logins.

## Likely causes

1. **Disk full** on the colima volume — the fastest and most common cause.
2. **Timescale chunk/hypertable corruption** after an unclean shutdown
   (colima killed without `make down`).
3. **Bad config applied by `ALTER SYSTEM`** — most likely from the WAL archive
   enable in `make backup` (see [docs/BACKUP.md](../../docs/BACKUP.md)).
4. **Failed migration** leaving the DB in a dirty state.
5. Volume loss (the DR case — go to [dr-drill](dr-drill.md) instead).

## Diagnosis

Run these in order; each is one fact.

```bash
# 1. Is it running at all? Health column is the compose healthcheck.
make ps

# 2. What does postgres itself say? This is the single most useful line.
make logs S=postgres

# 3. Disk: the #1 cause. Look at the mount holding /var/lib/postgresql/data.
docker compose exec -T postgres df -h /var/lib/postgresql/data

# 4. Is the WAL archive choking the pg_wal dir?
docker compose exec -T postgres du -sh /var/lib/postgresql/data/pg_wal /var/lib/postgresql/data/pg_wal_archive 2>/dev/null

# 5. Which recovery state is it in?
docker compose exec -T postgres psql -U smm -d smm -tAc "select pg_is_in_recovery();"

# 6. Is it accepting connections on the published port from the host?
pg_isready -h localhost -p 24543 -U smm -d smm     # or: nc -z localhost 24543

# 7. Migrations dirty? (migrate container is what failed, not postgres)
make migrate-status
```

## Remediation

**0. If data loss is even suspected: stop, page the escalation ladder, and go
to [docs/BACKUP.md](../../docs/BACKUP.md).** Do not run destructive commands
before a backup exists.

```bash
# A. Disk full on the data volume — free WAL, not data.
make logs S=postgres | grep -i 'no space left'
#   The safe first move is to drop old WAL archive copies that backup.sh has
#   already snapshotted into infra/backup/backups/*/wal/:
ls infra/backup/backups/latest/wal/ | wc -l
docker compose exec -T postgres sh -c 'rm -f /var/lib/postgresql/data/pg_wal_archive/00000001000000000000000*.partial'
#   Then restart; postgres replays its own WAL and comes back.
docker compose restart postgres
#   DO NOT delete anything under pg_wal itself unless you are restoring.

# B. Config poison from ALTER SYSTEM — reset and restart.
docker compose exec -T postgres psql -U smm -d smm -c "select name,setting from pg_settings where source='configuration file';"
#   If archive_mode/archive_command are the suspects:
docker compose exec -T postgres psql -U smm -d smm -c "alter system reset archive_command; alter system reset archive_timeout;"
docker compose restart postgres

# C. Dirty migration blocking the API from booting (postgres itself is fine).
make migrate-status
make migrate-down N=1     # roll back the culprit, then fix the SQL

# D. Total volume loss — the DR path.
make down
make drill                # destroys data; only on a stack you can lose
```

## Verification

The alert is closed only when **all** pass:

```bash
make ps                                          # postgres healthy
pg_isready -h localhost -p 24543 -U smm -d smm   # accepts host connections
curl -sf localhost:24080/healthz                 # API healthy
curl -s localhost:24080/readyz | grep '"ok"'     # readiness not degraded
```

Then spot-check business data, because a healthy postgres can still be an empty
postgres:

```bash
docker compose exec -T postgres psql -U smm -d smm -tAc \
  "select count(*) from worker; select count(*) from account; select count(*) from heartbeat;"
```

If those counts are zero on a stack that had data, you are in the DR scenario —
[dr-drill](dr-drill.md), not this runbook.

## Post-mortem prompt

When did the first signal arrive versus when did we act? If disk, why did
capacity monitoring not page first — the runbook found it, which means the
alert that should have fired did not. See [README](README.md#post-mortem-prompt).
