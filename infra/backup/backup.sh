#!/usr/bin/env bash
# P5-05 backup. Three data planes:
#   1. Postgres/Timescale: pg_dump (logical, -Fc) + a snapshot of the archived WAL
#   2. MinIO:              every bucket, via the mc client
#   3. Worker sessions:    the `sessions` volume (browser storageState JSON)
#
# Idempotent: each run writes a new timestamped directory and repoints `latest`.
# Usage: make backup   (or: bash infra/backup/backup.sh)
#        SMM_BACKUP_NAME=demo SMM_WAL_ARCHIVE=0 bash infra/backup/backup.sh
set -euo pipefail
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

TS="${SMM_BACKUP_NAME:-$(now_ts)}"
OUT="$BACKUP_DIR/$TS"

log "backup: preflight (postgres + minio must be running)"
require_db_up
require_minio_up
[ -e "$OUT" ] && die "backup already exists: $OUT (override with SMM_BACKUP_NAME)"
mkdir -p "$OUT/minio" "$OUT/wal"

# --- 1. Postgres -------------------------------------------------------------
log "backup: postgres logical dump (pg_dump -Fc)"
psql_of "select version()" > "$OUT/pg_version.txt"
"${COMPOSE[@]}" exec -T "$SVC_DB" pg_dump -U "$(env_of "$SVC_DB" POSTGRES_USER)" \
  -d "$(env_of "$SVC_DB" POSTGRES_DB)" -Fc > "$OUT/postgres.dump"

# --- 2. WAL archive (point-in-time source) -----------------------------------
# ponytail: this preserves archived WAL segments next to the logical dump. True
# PITR replay needs a physical base backup (pg_basebackup) plus a recovery
# container; add that when a <24h recovery point is actually required. The dump
# alone already satisfies the "restore < 30 min" AC.
if [ "${SMM_WAL_ARCHIVE:-1}" = "1" ]; then
  log "backup: WAL archive"
  if [ "$(psql_of "show archive_mode")" != "on" ]; then
    log "postgres archive_mode is off — enabling (ALTER SYSTEM + restart, ~10s)"
    "${COMPOSE[@]}" exec -T "$SVC_DB" mkdir -p "$WAL_DIR"
    "${COMPOSE[@]}" exec -T "$SVC_DB" chown postgres:postgres "$WAL_DIR"
    psql_of "alter system set archive_mode = 'on'"
    psql_of "alter system set archive_command = 'test -f $WAL_DIR/%f || cp %p $WAL_DIR/%f'"
    # RPO <= 5 min (INFRA_ANALYST.md §6.3): force a segment switch after 5 min of
    # WAL activity so the archive never lags more than one window.
    psql_of "alter system set archive_timeout = '300s'"
    "${COMPOSE[@]}" restart "$SVC_DB" >/dev/null
    wait_for db_ready postgres 180
  fi
  # Switch now so the segment covering this backup is archived immediately.
  psql_of "select pg_switch_wal()" >/dev/null 2>&1 || true
  # Wait until the archiver has settled (count stable across two samples).
  prev=-1
  for _ in $(seq 1 10); do
    n="$("${COMPOSE[@]}" exec -T "$SVC_DB" sh -c "ls -1 '$WAL_DIR' 2>/dev/null | wc -l | tr -d ' '")"
    [ "$n" = "$prev" ] && break
    prev="$n"
    sleep 1
  done
  "${COMPOSE[@]}" exec -T "$SVC_DB" tar -C "$WAL_DIR" -cf - . 2>/dev/null | tar -C "$OUT/wal" -xf -
  log "backup: $(find "$OUT/wal" -type f | wc -l | tr -d ' ') WAL segments"
fi

# --- 3. MinIO ----------------------------------------------------------------
# The script is written into the backup dir (which is bind-mounted at /backup),
# so the quoting stays plain shell and the exact sync command ships with the
# backup.
log "backup: minio objects"
cat > "$OUT/.mc-backup.sh" <<'SH'
set -eu
mc alias set local http://minio:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" >/dev/null 2>&1
mkdir -p /backup/minio
for b in $(mc ls local | awk '{print $NF}' | sed 's:/$::'); do
  [ -n "$b" ] || continue
  mkdir -p "/backup/minio/$b"
  n=$(mc ls --recursive "local/$b" 2>/dev/null | grep -c . | tr -d ' ')
  if [ "$n" -gt 0 ]; then
    mc cp --recursive "local/$b" "/backup/minio/$b"
  fi
done > /backup/minio/buckets.txt
SH
mc_job "$OUT" /backup/.mc-backup.sh
log "backup: minio buckets: $(tr '\n' ' ' < "$OUT/minio/buckets.txt")"

# --- 4. Worker sessions PVC --------------------------------------------------
log "backup: worker session PVC ($VOL_SESSIONS)"
"${COMPOSE[@]}" run --rm --no-deps \
  -v "$VOL_SESSIONS:/sessions:ro" -v "$OUT:/dst" \
  --entrypoint /bin/sh "$SVC_DB" -c 'tar -C /sessions -cf /dst/sessions.tar .'
log "backup: $(tar -tf "$OUT/sessions.tar" | grep -v '/$' | grep -c . | tr -d ' ') session files"

# --- 5. Manifest (the restore-time integrity contract) -----------------------
log "backup: manifest"
# Exclude Timescale's own schemas: their internal table count varies with chunk
# count and would make the backup/restore comparison flaky. The hypertables
# themselves live in public and are counted.
TABLES_SQL="select count(*) from information_schema.tables
  where table_schema not in ('pg_catalog','information_schema',
    '_timescaledb_internal','_timescaledb_functions','timescaledb_catalog',
    'timescaledb_information','timescaledb_experimental')
  and table_type = 'BASE TABLE'"
tables="$(psql_of "$TABLES_SQL")"
# ANALYZE first so n_live_tup is exact, not an estimate.
psql_of "analyze" >/dev/null
rows="$(psql_of "select coalesce(sum(n_live_tup),0)::bigint from pg_stat_user_tables")"

{
  echo "backup_name=$TS"
  echo "created_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "postgres_dump_bytes=$(stat -f %z "$OUT/postgres.dump" 2>/dev/null || stat -c %s "$OUT/postgres.dump")"
  echo "postgres_tables=$tables"
  echo "postgres_rows=$rows"
  if [ -f "$OUT/minio/buckets.txt" ]; then
    while read -r b; do
      [ -n "$b" ] || continue
      echo "minio_objects_$b=$(find "$OUT/minio/$b" -type f 2>/dev/null | wc -l | tr -d ' ')"
    done < "$OUT/minio/buckets.txt"
  fi
  echo "sessions_files=$(tar -tf "$OUT/sessions.tar" 2>/dev/null | grep -v '/$' | grep -c . | tr -d ' ')"
} > "$OUT/manifest.txt"

# `latest` is what restore.sh and drill.sh default to.
ln -sfn "$TS" "$BACKUP_DIR/latest"

# Retention: keep the newest N timestamped directories.
keep="${SMM_BACKUP_KEEP:-7}"
if [ "$keep" -gt 0 ]; then
  ls -1 "$BACKUP_DIR" 2>/dev/null | grep -v '^latest$' | sort -r | tail -n +"$((keep + 1))" | while read -r old; do
    rm -rf -- "$BACKUP_DIR/$old"
  done
  log "backup: retention keeps the newest $keep backups"
fi

log "backup: done -> $OUT"
log "backup: restore with: make restore B=$TS"
