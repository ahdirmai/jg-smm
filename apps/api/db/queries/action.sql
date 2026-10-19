-- ActionJob + ActionLog (P3-01): the action engine's bookkeeping.
--
-- ActionJob mirrors ScrapeJob's claim shape on purpose (same FIFO per-account
-- claim via (account_id, status, scheduled_at)); the worker callback and the
-- dashboard treat both uniformly. ActionLog is the verdict source of truth;
-- ActionJob.status is only a projection of the latest attempt.

-- name: CreateActionJob :one
INSERT INTO action_job (type, target_id, account_id, worker_id, template_id, status, scheduled_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetActionJobByID :one
SELECT *
FROM action_job
WHERE id = $1;

-- name: ClaimNextActionJob :one
-- Atomic FIFO claim for one account: PENDING + scheduled_at <= now() ordered by
-- scheduled_at, bumped to RUNNING, and stamped with the claiming worker. The
-- worker id is written here (not at enqueue time) because a job is assigned to
-- whichever worker owns the account's queue at claim time.
UPDATE action_job
SET status     = 'RUNNING',
    started_at = now(),
    attempts   = attempts + 1,
    worker_id  = $2
WHERE id = (
    SELECT aj.id
    FROM action_job AS aj
    WHERE aj.account_id = $1
      AND aj.status     = 'PENDING'
      AND aj.scheduled_at <= now()
    ORDER BY aj.scheduled_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING *;

-- name: SweepStaleRunningJobs :execrows
-- One lost callback (OOM-killed worker, container evicted mid-action) leaves a
-- job RUNNING forever: ClaimNextActionJob only selects PENDING, and Retry only
-- accepts FAILED, so the queue shows it stuck in "Verifying" with no way out.
-- Requeue it; the attempt counter is already bumped, so the existing
-- max-attempts ceiling in the scheduler bounds how often this can fire.
UPDATE action_job
SET status       = 'PENDING',
    scheduled_at = now()
WHERE status     = 'RUNNING'
  AND started_at < now() - make_interval(mins => $1::int);

-- name: CompleteActionJob :one
-- Terminal transition. The caller has already written the ActionLog row; this
-- only projects the verdict onto the job so the queue can be listed without
-- joining the log.
--
-- The status guard is load-bearing: /internal/* is machine ingress, so a
-- replayed or forged callback must not be able to re-open or re-grade a job
-- that already reached a terminal state.
UPDATE action_job
SET status      = $2,
    finished_at = now(),
    error       = $3
WHERE id = $1
  AND status IN ('PENDING', 'RUNNING')
RETURNING *;

-- name: RescheduleActionJob :one
-- Backoff/jitter path (P3-11): the attempt failed with a retryable class, so
-- the job goes back to PENDING at a future scheduled_at for the next tick.
-- Also serves "run pending now", where the row is already PENDING.
--
-- Same guard as CompleteActionJob: a SUCCESS/FAILED/CANCELLED job is terminal
-- and must never be pushed back into the queue.
UPDATE action_job
SET status       = 'PENDING',
    scheduled_at = $2,
    finished_at  = NULL
WHERE id = $1
  AND status IN ('PENDING', 'RUNNING')
RETURNING *;

-- name: SetActionJobTemplate :exec
-- Records the pool variant a job's text came from. Written at dispatch, after
-- the composer picks: the job row is created before any text exists (an
-- operator body, or a variant drawn at dispatch time), so this cannot be an
-- INSERT-time field. action_log.template_id is written separately from the
-- worker's callback echo; this is the queue-side projection of the same fact.
UPDATE action_job
SET template_id = $2
WHERE id = $1;

-- name: SetPendingActionJobsScheduledAt :execrows
-- "Run pending now": pull every PENDING row forward to one instant. One
-- statement, not a paged LIMIT/OFFSET walk — the walk ordered by scheduled_at
-- and then rewrote scheduled_at, re-sorting the set it was walking, so the
-- second page re-read rows the first page had already moved and the rows behind
-- them were never reached. Terminal and RUNNING rows are untouched; a PENDING
-- row already at $1 is rewritten to the same value.
UPDATE action_job
SET scheduled_at = $1
WHERE status = 'PENDING';

-- name: CancelActionJob :one
-- Operator cancel (dashboard). Same guard as CompleteActionJob: only a job
-- that could still be claimed/completed is cancellable, so a replayed cancel
-- cannot overwrite a verdict the worker already wrote. A RUNNING job is
-- cancellable too: the update does not stop the worker mid-action, but it
-- makes the job terminal, so the callback is a no-op (the guard above) and
-- the stale-running sweep cannot requeue it.
UPDATE action_job
SET status      = 'CANCELLED',
    finished_at = now(),
    error       = 'Cancelled by operator'
WHERE id = $1
  AND status IN ('PENDING', 'RUNNING')
RETURNING *;

-- name: CancelPendingActionJobs :execrows
-- "Cancel pending": one statement over the whole queued set, mirroring
-- SetPendingActionJobsScheduledAt. RUNNING and terminal rows are untouched —
-- cancelling a job after its worker claimed it would not stop the action, and
-- the per-row cancel exists for the RUNNING case.
UPDATE action_job
SET status      = 'CANCELLED',
    finished_at = now(),
    error       = 'Cancelled by operator'
WHERE status = 'PENDING';

-- name: ListActionJobs :many
SELECT *
FROM action_job
ORDER BY scheduled_at DESC
LIMIT $1 OFFSET $2;

-- name: ListActionJobsByStatus :many
SELECT *
FROM action_job
WHERE status = $1
ORDER BY scheduled_at
LIMIT $2 OFFSET $3;

-- name: ListActionJobsByAccount :many
SELECT *
FROM action_job
WHERE account_id = $1
ORDER BY scheduled_at DESC
LIMIT $2 OFFSET $3;

-- name: UpsertActionLog :one
-- The upsert heart of the callback path. A worker first reports RUNNING (no
-- screenshot yet, no verdict) and later the terminal verdict on the SAME row:
-- UNIQUE (action_job_id, attempt) makes the pair one row.
-- screenshot_url and response_excerpt use COALESCE so a terminal callback that
-- omits them never erases what the RUNNING callback or an earlier attempt
-- captured; rendered_text is the commanded input and is always authoritative.
INSERT INTO action_log (
    action_job_id, attempt, status, verified, worker_id, template_id, rendered_text,
    response_excerpt, error_class, screenshot_url, duration_ms
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (action_job_id, attempt) DO UPDATE
SET status           = EXCLUDED.status,
    verified         = EXCLUDED.verified,
    worker_id        = COALESCE(EXCLUDED.worker_id, action_log.worker_id),
    template_id      = COALESCE(EXCLUDED.template_id, action_log.template_id),
    response_excerpt = COALESCE(EXCLUDED.response_excerpt, action_log.response_excerpt),
    error_class      = COALESCE(EXCLUDED.error_class, action_log.error_class),
    screenshot_url   = COALESCE(EXCLUDED.screenshot_url, action_log.screenshot_url),
    duration_ms      = EXCLUDED.duration_ms,
    ts               = now()
RETURNING *;

-- name: GetActionLog :one
-- The latest attempt of a job, by (job, attempt).
SELECT *
FROM action_log
WHERE action_job_id = $1 AND attempt = $2;

-- name: ListActionLogsByJob :many
-- Every attempt of a job, newest first: the dashboard drawer that debugs one
-- action (P4-02) reads this.
SELECT *
FROM action_log
WHERE action_job_id = $1
ORDER BY attempt DESC;

-- name: ListActionLogsByErrorClass :many
-- The failure-class view (P3-12): counts and samples per class come from here.
SELECT *
FROM action_log
WHERE error_class = $1
ORDER BY ts DESC
LIMIT $2 OFFSET $3;

-- name: LatestActionLogsByJobs :many
-- The newest attempt of each of a set of jobs in one round trip (P3-13): the
-- queue view shows live status (job) plus the verdict that explains it (log),
-- and this keeps a 50-row page at one query rather than one per row.
-- DISTINCT ON is the per-group max: one row per job, the highest attempt.
SELECT DISTINCT ON (action_job_id) *
FROM action_log
WHERE action_job_id = ANY($1::uuid[])
ORDER BY action_job_id, attempt DESC;
