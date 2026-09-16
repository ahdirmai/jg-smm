#!/usr/bin/env bash
# P5-05 restore. Rebuilds Postgres + MinIO + worker sessions from a backup
# directory and verifies the result against the backup's manifest.
#
# Destructive: drops the live database and overwrites MinIO objects.
# Usage: make restore            (latest backup)
#        make restore B=20260101T120000Z
#        SMM_YES=1 bash infra/backup/restore.sh latest
set -euo pipefail
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

name="${1:-latest}"
SRC="$BACKUP_DIR/$name"
# Follow the `latest` symlink to a concrete directory.
[ -L "$SRC" ] && SRC="$(cd "$SRC" && pwd)"
[ -d "$SRC" ] || die "backup not found: $SRC (run 'make backup' first)"
[ -f "$SRC/postgres.dump" ] || die "missing postgres.dump in $SRC"
[ -f "$SRC/manifest.txt" ] || die "missing manifest.txt in $SRC"

require_confirmation "RESTORE will DROP the live database and overwrite MinIO objects.
Source: $SRC"
log "restore: $SRC"
SECONDS=0

log "restore: preflight"
require_db_up
require_minio_up

manifest_get() { grep -m1 "^$1=" "$SRC/manifest.txt" | cut -d= -f2-; }
TABLES_SQL="select count(*) from information_schema.tables
  where table_schema not in ('pg_catalog','information_schema',
    '_timescaledb_internal','_timescaledb_functions','timescaledb_catalog',
    'timescaledb_information','timescaledb_experimental')
  and table_type = 'BASE TABLE'"

# --- 1. Stop the app plane (it holds the database we are about to drop) ------
log "restore: stopping app services (postgres/minio/redis stay up)"
"${COMPOSE[@]}" stop api worker web migrate 2>/dev/null || log "restore: (some app services were not running)"

# --- 2. Postgres -------------------------------------------------------------
log "restore: postgres (drop + create + pg_restore)"
"${COMPOSE[@]}" exec -T "$SVC_DB" dropdb -U "$(env_of "$SVC_DB" POSTGRES_USER)" --if-exists "$(env_of "$SVC_DB" POSTGRES_DB)"
"${COMPOSE[@]}" exec -T "$SVC_DB" createdb -U "$(env_of "$SVC_DB" POSTGRES_USER)" "$(env_of "$SVC_DB" POSTGRES_DB)"
# The timescale extension is only created if the image supports it; a dump that
# does not use hypertables restores either way.
psql_of "create extension if not exists timescaledb" >/dev/null 2>&1 \
  || log "restore: timescaledb extension unavailable (fine if the dump does not use it)"
"${COMPOSE[@]}" exec -T "$SVC_DB" pg_restore \
  -U "$(env_of "$SVC_DB" POSTGRES_USER)" -d "$(env_of "$SVC_DB" POSTGRES_DB)" \
  --no-owner -Fc - < "$SRC/postgres.dump"
log "restore: pg_restore done"

# --- 3. MinIO ----------------------------------------------------------------
log "restore: minio (ensure buckets, then restore objects)"
# Runs the minio-init service's own idempotent bucket-creation command.
"${COMPOSE[@]}" run --rm --no-deps "$SVC_MC" 2>/dev/null || log "restore: bucket-create step warned (continuing)"
cat > "$SRC/.mc-restore.sh" <<'SH'
set -eu
mc alias set local http://minio:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" >/dev/null 2>&1
for b in $(ls -1 /backup/minio 2>/dev/null); do
  n=$(find "/backup/minio/$b" -type f 2>/dev/null | wc -l | tr -d ' ')
  [ "$n" -gt 0 ] || continue
  mc cp --recursive "/backup/minio/$b" "local/$b"
done
SH
mc_job "$SRC" /backup/.mc-restore.sh

# --- 4. Worker sessions PVC --------------------------------------------------
if [ -f "$SRC/sessions.tar" ]; then
  log "restore: worker sessions PVC ($VOL_SESSIONS)"
  "${COMPOSE[@]}" run --rm --no-deps \
    -v "$VOL_SESSIONS:/sessions" -v "$SRC:/src:ro" \
    --entrypoint /bin/sh "$SVC_DB" -c 'mkdir -p /sessions && tar -C /sessions -xf /src/sessions.tar'
else
  log "restore: no sessions.tar in this backup — session PVC untouched"
fi

# --- 5. Verify against the manifest ------------------------------------------
log "restore: verifying against the manifest"
fail=0
check() { # check <label> <expected> <actual>
  if [ "$2" = "$3" ]; then
    log "  OK   $1: $3"
  else
    log "  FAIL $1: expected $2, got $3"
    fail=1
  fi
}

psql_of "analyze" >/dev/null
check "postgres tables" "$(manifest_get postgres_tables)" "$(psql_of "$TABLES_SQL")"
check "postgres rows" "$(manifest_get postgres_rows)" "$(psql_of "select coalesce(sum(n_live_tup),0)::bigint from pg_stat_user_tables")"

cat > "$SRC/.mc-verify.sh" <<'SH'
set -eu
mc alias set local http://minio:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" >/dev/null 2>&1
for b in $(mc ls local | awk '{print $NF}' | sed 's:/$::'); do
  echo "$b=$(mc ls --recursive "local/$b" 2>/dev/null | grep -c . | tr -d ' ')"
done
SH
# Capture to a file: the loop must run in this shell so `fail` is not lost to a
# subshell.
mc_job "$SRC" /backup/.mc-verify.sh > "$SRC/.mc-verify.out"
while read -r line; do
  b="${line%%=*}"
  got="${line#*=}"
  exp="$(manifest_get "minio_objects_$b")"
  [ -n "$exp" ] || exp=0
  check "minio:$b" "$exp" "$got"
done < "$SRC/.mc-verify.out"

if [ -f "$SRC/sessions.tar" ]; then
  got_sessions="$("${COMPOSE[@]}" run --rm --no-deps -v "$VOL_SESSIONS:/sessions:ro" \
    --entrypoint /bin/sh "$SVC_DB" -c 'find /sessions -type f 2>/dev/null | wc -l | tr -d " ')"
  check "sessions files" "$(manifest_get sessions_files)" "$got_sessions"
fi

# --- 6. Restart the app plane on the restored data ---------------------------
log "restore: restarting the app plane (migrate is a no-op on a restored schema)"
"${COMPOSE[@]}" up -d api worker web 2>/dev/null
wait_for api_ready api 90

[ "$fail" -eq 0 ] || die "restore verification FAILED — data planes do not match the manifest; do not route traffic here"
log "restore: verified against the manifest ($SRC)"
log "restore: done in ${SECONDS}s"
