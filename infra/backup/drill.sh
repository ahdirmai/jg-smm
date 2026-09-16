#!/usr/bin/env bash
# P5-05 disaster-recovery drill.
#
# Simulates a total data-plane loss: backs up, takes the stack down, deletes
# every app volume, restores from the backup, and verifies integrity. Prints the
# restore time so the "restore < 30 min" acceptance test is measurable.
#
# DESTROYS DATA. Never run it against a stack you cannot lose.
# Usage: make drill   (or: SMM_YES=1 bash infra/backup/drill.sh)
set -euo pipefail
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

STEP="preflight"
total_start="$(date +%s)"

require_confirmation "DR DRILL: this DESTROYS all app data volumes
(pgdata, miniodata, sessions, redisdata, screenshots) and rebuilds them
from the latest backup. Anything not backed up is gone."

on_fail() {
  printf '[%s] DRILL FAILED during: %s\n' "$(date -u +%H:%M:%SZ)" "${STEP:-unknown}" >&2
  printf '      elapsed: %ss. The stack may be down.\n' "$SECONDS" >&2
  printf '      recover: make up ; make restore\n' >&2
}
trap on_fail ERR

log "drill: preflight"
require_db_up
require_minio_up

# --- 1. A fresh backup, so we restore exactly what we had --------------------
if [ "${SMM_SKIP_BACKUP:-0}" != "1" ]; then
  STEP="backup"
  log "drill: backing up the current state"
  bash "$ROOT/infra/backup/backup.sh"
else
  log "drill: SMM_SKIP_BACKUP=1 — using the existing backup"
fi
SRC="$BACKUP_DIR/latest"
[ -L "$SRC" ] || die "no backup found — run 'make backup' first"
SRC="$(cd "$SRC" && pwd)"
log "drill: will restore from $SRC"

# The clock for the AC: "restore < 30 min". It starts when the disaster hits.
restore_start="$(date +%s)"

# --- 2. Simulate the disaster ------------------------------------------------
STEP="disaster: docker compose down"
log "drill: bringing the stack down"
"${COMPOSE[@]}" down --remove-orphans >/dev/null

STEP="disaster: wiping app volumes"
for v in "$VOL_PGDATA" "$VOL_REDIS" "$VOL_MINIO" "$VOL_SESSIONS" "$VOL_SCREENSHOTS"; do
  log "drill: removing volume $v"
  docker volume rm "$v" >/dev/null 2>&1 \
    || die "cannot remove $v (a container may still use it: run 'make down')"
done

# --- 3. Bring the data plane back empty, then restore onto it ----------------
STEP="recovery: data plane up (fresh volumes)"
log "drill: starting the data plane on empty volumes"
"${COMPOSE[@]}" up -d postgres redis minio >/dev/null
wait_for db_ready postgres 240
wait_for minio_ready minio 240

STEP="recovery: restore"
log "drill: restoring from the backup"
SMM_YES=1 bash "$ROOT/infra/backup/restore.sh" "$SRC"
# restore.sh restarts the app plane and verifies against the manifest.

# --- 4. Drill-level verification --------------------------------------------
STEP="verification"
log "drill: verifying the recovered stack"

require_db_up
log "  OK   pg_isready: postgres accepts connections"

tables="$(psql_of "select count(*) from information_schema.tables where table_schema not in ('pg_catalog','information_schema','_timescaledb_internal','_timescaledb_functions','timescaledb_catalog','timescaledb_information','timescaledb_experimental') and table_type = 'BASE TABLE'")"
rows="$(psql_of "select coalesce(sum(n_live_tup),0)::bigint from pg_stat_user_tables")"
log "  OK   row-count sanity: $tables tables, $rows rows in public"
[ "$tables" -gt 0 ] || die "row-count sanity failed: no user tables — the schema did not restore"
[ "$rows" -gt 0 ] || log "  WARN no rows in user tables (an empty DB restores empty; that can be correct)"

cat > "$SRC/.mc-drill-list.sh" <<'SH'
set -eu
mc alias set local http://minio:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" >/dev/null 2>&1
mc ls local
SH
log "  minio buckets:"
mc_job "$SRC" /backup/.mc-drill-list.sh | sed 's/^/       /'

# The app plane must be serving on top of the restored data.
api_ready || die "api is not healthy after the drill (check 'make logs S=api')"
log "  OK   api /healthz (http://localhost:${API_PORT}/healthz)"
"${COMPOSE[@]}" ps worker >/dev/null 2>&1 \
  && log "  OK   worker container running" \
  || log "  WARN no worker container is running (start one with 'make up')"

# --- 5. Report ---------------------------------------------------------------
restore_end="$(date +%s)"
rto=$((restore_end - restore_start))
total=$((restore_end - total_start))
rto_min="$(awk -v s="$rto" 'BEGIN{printf "%.1f", s/60}')"

log "drill: restore time (down -> verified): ${rto}s (${rto_min} min)"
log "drill: total drill time (incl. backup): ${total}s"

if [ "$rto" -gt 1800 ]; then
  die "DRILL FAILED the acceptance test: restore took ${rto}s > 1800s (30 min)"
fi
log "drill: PASS — restore within 1800s (AC: restore < 30 min)"
log "drill: the stack is running on restored data — spot-check the UI before trusting it."
