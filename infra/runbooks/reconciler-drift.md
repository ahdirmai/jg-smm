# ALERT: reconciler-drift

**Severity:** SEV2 — desired worker state is not what is running. Workers idle
or orphaned; accounts not served.
**Alert source:** `apps/api/internal/service/reconciler.go` — no page of its own
today; surfaces as idle workers, a `restart_count` climbing without recovery, or
`provision_log` rows that repeat without effect.
**Gate:** `RECONCILE_INTERVAL_SECONDS` (default 0 = the loop is off — drift can
only accumulate while it is off).

## Symptoms

- `worker` rows say `desired_state = RUNNING` but the containers are absent or
  crashing (or the reverse: desired `STOPPED` with containers still alive).
- `observed_gen` lags `generation` on the row, forever.
- `provision_log` shows repeated `create`/`delete` for the same worker with no
  state change.
- Accounts assigned to the drifted workers are idle; queues back up for them.

## Impact

The accounts on the affected workers stop acting. Nothing is corrupted — the
reconciler is idempotent by design, so a converged state is always reachable —
but until it converges, those workers are dark.

## Likely causes

1. **The reconcile loop is off** (`RECONCILE_INTERVAL_SECONDS=0`). Locally this
   is the default, and drift is *expected*: `PROVISIONER_MODE=static` means
   `CreateWorker`/`DeleteWorker` record intent only and the operator scales
   containers by hand.
2. **Observe failure** — the driver cannot read the platform state, so every
   tick logs `reconcile: observe failed` and the diff never advances.
3. **Stale `observed_gen`** persisted on the row: `ActionRefresh` fires every
   tick without the row ever updating (`markObserved` failure is non-fatal and
   silent).
4. **Static mode confusion**: the Worker row exists, `Observe` reports it as
   present, but no container is running — the row *is* the platform state
   locally, and a deleted container is invisible to the reconciler.
5. **Orphan sweeper racing** with a mid-create worker (60 s grace window).

## Diagnosis

```bash
# 1. Is the loop even running? Off = drift is by design locally.
make logs S=api | grep -i 'reconciler started\|reconcile tick'
grep RECONCILE_INTERVAL_SECONDS .env 2>/dev/null || echo 'RECONCILE_INTERVAL_SECONDS unset (loop off)'

# 2. The diff inputs: desired vs observed for every worker.
docker compose exec -T postgres psql -U smm -d smm -c \
  "select name, desired_state, status, generation, observed_gen, restart_count, provision_err from worker;"

# 3. What has the reconciler actually tried to do?
docker compose exec -T postgres psql -U smm -d smm -c \
  "select worker_id, op, created_at from provision_log order by created_at desc limit 20;"

# 4. Reconciler errors (observe/create/delete failures are logged, not raised):
make logs S=api | grep -i 'reconcile:' | tail -30

# 5. Do the rows match reality? Local mode: row presence == "running".
docker compose ps worker --format '{{.Name}} {{.Status}}'
#    A worker row with no container IS the drift locally.

# 6. Provisioner mode in effect:
docker compose exec -T api printenv PROVISIONER_MODE RECONCILE_INTERVAL_SECONDS
```

## Remediation

```bash
# A. The loop is off and drift accumulated: turn it on (local static mode
#    makes it bookkeeping-only — it cannot create containers).
make down
RECONCILE_INTERVAL_SECONDS=30 make up
#    Then confirm a tick ran and nothing is pending:
make logs S=api | grep 'reconcile tick'

# B. Stale observed_gen that never persists: markObserved failed silently.
#    Force the row to reality so the diff becomes a no-op:
docker compose exec -T postgres psql -U smm -d smm -c \
  "update worker set observed_gen = generation where desired_state = 'RUNNING';"
#    (Safe: it only makes the next diff a no-op; the reconciler re-observes.)

# C. Container exists but desired STOPPED (delete keeps failing): locally
#    DeleteWorker is a no-op, so scale the containers by hand and let the row
#    settle.
docker compose up -d --scale worker=2 worker

# D. Observe failing on a real store error: the store, not the reconciler.
make logs S=api | grep 'reconcile: observe failed'
make ps            # postgres healthy? see postgres-unhealthy.md
```

## Verification

```bash
# Every RUNNING row has a matching container, every STOPPED row has none:
docker compose ps worker --format '{{.Name}}'
docker compose exec -T postgres psql -U smm -d smm -tAc \
  "select count(*) from worker where desired_state='RUNNING' and (observed_gen is null or observed_gen <> generation);"
# That count is 0 when converged. Then confirm the loop is quiet:
make logs S=api | grep 'reconcile' | tail -10      # no repeated create/delete
# Heartbeats flowing for the converged set:
docker compose exec -T postgres psql -U smm -d smm -c \
  "select name, status, last_heartbeat from worker;"
```

## Post-mortem prompt

Was the loop off when it should have been on, or was the observed state wrong?
Locally the reconciler cannot start containers — drift between rows and
containers is an operator procedure gap, not a code bug.
See [README](README.md#post-mortem-prompt).
