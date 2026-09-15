-- ScrapeJob + ApifyRun + RawPayload: the scrape pipeline bookkeeping.
-- ScrapeJob is claimed by a scheduler tick (FIFO per account via the index on
-- (account_id, status, scheduled_at)); no unique constraint enqueues order.

-- name: CreateScrapeJob :one
INSERT INTO scrape_job (type, target_id, account_id, worker_id, status, scheduled_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, type, target_id, account_id, worker_id, status, scheduled_at,
          started_at, finished_at, attempts, error, created_at;

-- name: GetScrapeJobByID :one
SELECT id, type, target_id, account_id, worker_id, status, scheduled_at,
       started_at, finished_at, attempts, error, created_at
FROM scrape_job
WHERE id = $1;

-- name: ClaimNextScrapeJob :one
-- Atomic FIFO claim for one account: PENDING + scheduled_at <= now() ordered by
-- scheduled_at, bumped to RUNNING. The account index makes this cheap.
UPDATE scrape_job
SET status     = 'RUNNING',
    started_at = now(),
    attempts   = attempts + 1
WHERE id = (
    SELECT sj.id
    FROM scrape_job AS sj
    WHERE sj.account_id = $1
      AND sj.status     = 'PENDING'
      AND sj.scheduled_at <= now()
    ORDER BY sj.scheduled_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING id, type, target_id, account_id, worker_id, status, scheduled_at,
          started_at, finished_at, attempts, error, created_at;

-- name: ListScrapeJobs :many
SELECT id, type, target_id, account_id, worker_id, status, scheduled_at,
       started_at, finished_at, attempts, error, created_at
FROM scrape_job
ORDER BY scheduled_at DESC
LIMIT $1 OFFSET $2;

-- name: ListScrapeJobsByStatus :many
SELECT id, type, target_id, account_id, worker_id, status, scheduled_at,
       started_at, finished_at, attempts, error, created_at
FROM scrape_job
WHERE status = $1
ORDER BY scheduled_at
LIMIT $2 OFFSET $3;

-- name: ListPendingScrapeJobsByAccount :many
SELECT id, type, target_id, account_id, worker_id, status, scheduled_at,
       started_at, finished_at, attempts, error, created_at
FROM scrape_job
WHERE account_id = $1
  AND status     = 'PENDING'
ORDER BY scheduled_at
LIMIT $2;

-- name: RescheduleScrapeJob :one
-- Moves a job back into the queue at a later time. Used by the scheduler's
-- backoff/jitter path: the job is set PENDING and its scheduled_at pushed out,
-- so the next eligible tick claims it again.
UPDATE scrape_job
SET status       = 'PENDING',
    scheduled_at = $2,
    finished_at  = NULL
WHERE id = $1
RETURNING id, type, target_id, account_id, worker_id, status, scheduled_at,
          started_at, finished_at, attempts, error, created_at;

-- name: CompleteScrapeJob :one
UPDATE scrape_job
SET status      = $2,
    finished_at = now(),
    error       = $3
WHERE id = $1
RETURNING id, type, target_id, account_id, worker_id, status, scheduled_at,
          started_at, finished_at, attempts, error, created_at;

-- name: CreateApifyRun :one
INSERT INTO apify_run (scrape_job_id, actor_id, run_id, status)
VALUES ($1, $2, $3, $4)
RETURNING id, scrape_job_id, actor_id, run_id, status, started_at, finished_at;

-- name: UpdateApifyRun :one
-- finished_at is passed in (NULL while the run is still in flight) so the caller
-- controls the terminal stamp without a CASE in SQL.
UPDATE apify_run
SET status      = $2,
    finished_at = $3
WHERE id = $1
RETURNING id, scrape_job_id, actor_id, run_id, status, started_at, finished_at;

-- name: CreateRawPayload :one
INSERT INTO raw_payload (apify_run_id, s3_key, bytes)
VALUES ($1, $2, $3)
RETURNING id, apify_run_id, s3_key, bytes, received_at;

-- name: ListRawPayloadsByRun :many
SELECT s3_key
FROM raw_payload
WHERE apify_run_id = $1
ORDER BY received_at;
