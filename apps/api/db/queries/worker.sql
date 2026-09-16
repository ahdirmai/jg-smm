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
    id, name, container_id, control_channel, action_queue, session_pvc,
    novnc_service, desired_state, source, region, status, generation,
    image_version
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
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
    restart_count  = $14
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
