#!/usr/bin/env bash
# Shared config + helpers for backup.sh / restore.sh / drill.sh (P5-05).
#
# Every service, volume, port and credential default below is pinned to
# compose.yaml (project `smm`) and infra/docker/.env.example. If a name changes
# there, change it here too.
set -euo pipefail

# Repo root = two levels up from this file (infra/backup -> repo).
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

# Always run compose against the repo's compose.yaml, whatever the CWD.
COMPOSE=(docker compose -f "$ROOT/compose.yaml" --project-directory "$ROOT")

# Backups land here by default. Override with an absolute path (SMM_BACKUP_DIR).
BACKUP_DIR="${SMM_BACKUP_DIR:-$ROOT/infra/backup/backups}"

# Named volumes in compose.yaml, prefixed with the project name `smm`.
VOL_PGDATA=smm_pgdata
VOL_REDIS=smm_redisdata
VOL_MINIO=smm_miniodata
VOL_SESSIONS=smm_sessions
VOL_SCREENSHOTS=smm_screenshots

# Services in compose.yaml.
SVC_DB=postgres
SVC_MINIO=minio
# minio-init ships the `mc` client; reused as a one-off job container.
SVC_MC=minio-init

# Ports published by compose.yaml (used for the API health probes).
API_PORT="${API_PORT:-24080}"

# WAL archive dir. It lives inside the pgdata volume because postgres must be
# able to write it and this script cannot add a volume mount to the running
# service. See docs/BACKUP.md "Limitations" for the upgrade path.
WAL_DIR=/var/lib/postgresql/data/pg_wal_archive

log() { printf '[%s] %s\n' "$(date -u +%H:%M:%SZ)" "$*"; }
die() { printf '[%s] ERROR: %s\n' "$(date -u +%H:%M:%SZ)" "$*" >&2; exit 1; }

now_ts() { date -u +%Y%m%dT%H%M%SZ; }

# Read an env var from a running service container. This is authoritative — it
# honours .env overrides without re-implementing compose interpolation here.
env_of() { "${COMPOSE[@]}" exec -T "$1" printenv "$2"; }

# psql as the container's own DB user/database (no host-side credentials).
psql_of() { "${COMPOSE[@]}" exec -T "$SVC_DB" psql -U "$(env_of "$SVC_DB" POSTGRES_USER)" -d "$(env_of "$SVC_DB" POSTGRES_DB)" -tAc "$1"; }

# --- readiness probes -------------------------------------------------------
# These never die: they return non-zero so callers can poll.

db_ready()    { "${COMPOSE[@]}" exec -T "$SVC_DB" pg_isready -U "$(env_of "$SVC_DB" POSTGRES_USER)" -d "$(env_of "$SVC_DB" POSTGRES_DB)" >/dev/null 2>&1; }
minio_ready() { "${COMPOSE[@]}" exec -T "$SVC_MINIO" mc ready local >/dev/null 2>&1; }
api_ready()   { curl -sf "http://localhost:${API_PORT}/healthz" >/dev/null 2>&1; }

require_db_up()   { db_ready   || die "postgres is not healthy — run 'make up' first"; }
require_minio_up() { minio_ready || die "minio is not healthy — run 'make up' first"; }

# Poll a probe function until it succeeds or the timeout expires.
wait_for() { # wait_for <probe-fn> <label> [timeout_seconds]
  local probe="$1" label="$2" timeout="${3:-180}" i
  log "waiting for $label (up to ${timeout}s)"
  for ((i = 0; i < timeout; i += 3)); do "$probe" && return 0; sleep 3; done
  die "$label did not become ready in ${timeout}s"
}

# --- one-off job containers -------------------------------------------------
# The postgres image is debian: it has tar/sh. The minio-init image is the `mc`
# client. Both are already pulled by the compose project, so no new images.

# Run a script from a host directory inside the mc image, with the host dir
# bind-mounted at /backup and MinIO credentials from the live minio container.
mc_job() { # mc_job <host-dir> <script-path-inside-container>
  local dir="$1" script="$2"
  "${COMPOSE[@]}" run --rm --no-deps \
    -e MINIO_ROOT_USER="$(env_of "$SVC_MINIO" MINIO_ROOT_USER)" \
    -e MINIO_ROOT_PASSWORD="$(env_of "$SVC_MINIO" MINIO_ROOT_PASSWORD)" \
    -v "$dir:/backup" \
    --entrypoint /bin/sh "$SVC_MC" "$script"
}

# Confirm a destructive action unless SMM_YES=1 (drill.sh sets it).
require_confirmation() {
  [ "${SMM_YES:-}" = "1" ] && return 0
  printf '%s\n\nType "yes" to continue: ' "$1" >&2
  local reply
  read -r reply
  [ "$reply" = "yes" ] || die "aborted by user"
}
