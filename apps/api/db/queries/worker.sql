-- Worker (container) lifecycle queries. One worker = one container hosting
-- at most one account per platform (UNIQUE(worker_id, platform) on account).
-- generation guards idempotent reconcile (pod label smm.generation).

-- name: GetWorkerByID :one
SELECT * FROM worker WHERE id = $1;

-- name: GetWorkerByName :one
SELECT * FROM worker WHERE name = $1;

-- name: ListWorkers :many
SELECT * FROM worker
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: CreateWorker :one
INSERT INTO worker (
    name, container_id, control_channel, action_queue, session_pvc,
    novnc_service, desired_state, source, region, status, generation,
    image_version, location, latitude, longitude, endpoint_url
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16
)
RETURNING *;

-- name: UpdateWorker :one
UPDATE worker SET
    container_id   = $2,
    desired_state  = $3,
    status         = $4,
    generation     = $5,
    observed_gen   = $6,
    provision_err  = $7,
    browser_status = $8,
    current_job_id = $9,
    last_heartbeat = $10,
    last_action_at = $11,
    last_error     = $12,
    queue_depth    = $13,
    restart_count  = $14,
    endpoint_url   = $15
WHERE id = $1
RETURNING *;

-- name: DeleteWorker :exec
DELETE FROM worker WHERE id = $1;

-- name: InsertHeartbeat :exec
INSERT INTO heartbeat (worker_id, ts, cpu, mem, jobs_done)
VALUES ($1, $2, $3, $4, $5);

-- name: TouchWorkerHeartbeat :one
UPDATE worker SET
    last_heartbeat = $2,
    status         = $3,
    browser_status = $4,
    queue_depth    = $5,
    current_job_id = $6,
    novnc_service  = $7
WHERE id = $1
RETURNING *;

-- name: UpsertPoolWorkerByName :one
-- Idempotent seed of one pool worker from configuration. Keyed on name (the
-- stable identity smm-worker-01..10), so a restart re-points the row at the
-- current endpoint without duplicating it, and a worker the operator stopped by
-- hand is brought back only by this explicit RUNNING intent.
INSERT INTO worker (
    name, desired_state, source, region, status, endpoint_url, novnc_service
) VALUES (
    -- IDLE, not PENDING: a pool worker is provisioned OUTSIDE this fleet (boxd
    -- snapshot), so nothing here is waiting to be created. PENDING made the
    -- dashboard claim a worker was mid-provisioning forever, and invited the
    -- container reconciler to try creating it.
    $1, 'RUNNING', 'MANUAL', $2, 'IDLE', $3, $4
)
ON CONFLICT (name) DO UPDATE SET
    endpoint_url  = EXCLUDED.endpoint_url,
    novnc_service = COALESCE(EXCLUDED.novnc_service, worker.novnc_service),
    desired_state = 'RUNNING',
    source        = 'MANUAL'
RETURNING *;

-- name: ListPoolWorkers :many
-- The workers a slot may be handed to: configured, running, and actually
-- reachable (endpoint_url set). Deliberately NOT a filtered ListWorkers: that
-- one pages in SQL and filters in Go, so a fleet with hundreds of legacy rows
-- would lose real candidates past the page. A stale PAUSED container row with
-- no endpoint must never be a candidate — the affinity hash would land an
-- account on a machine that cannot answer.
SELECT * FROM worker
WHERE desired_state = 'RUNNING'
  AND endpoint_url IS NOT NULL
  AND source = 'MANUAL'
ORDER BY name;
