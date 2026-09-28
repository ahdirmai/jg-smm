-- Account (executor identity) queries. password_enc is WRITE-ONLY: no query
-- here ever selects it. UNIQUE(platform, username) + UNIQUE(worker_id, platform).

-- name: GetAccountByID :one
SELECT
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, cookie_expiry_at, refresh_failures, created_at
FROM account
WHERE id = $1;

-- name: ListAccounts :many
SELECT
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, cookie_expiry_at, refresh_failures, created_at
FROM account
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: CreateAccount :one
INSERT INTO account (
    platform, username, password_enc, auth_status, proxy_group_id,
    status, tags, worker_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, cookie_expiry_at, refresh_failures, created_at;

-- name: UpdateAccount :one
UPDATE account SET
    auth_status      = $2,
    handle           = $3,
    last_verified_at = $4,
    proxy_group_id   = $5,
    health_score     = $6,
    status           = $7,
    tags             = $8,
    worker_id        = $9,
    last_used_at     = $10,
    last_checked_at  = $11,
    last_error       = $12,
    cookie_expiry_at = $13,
    refresh_failures = $14
WHERE id = $1
RETURNING
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, cookie_expiry_at, refresh_failures, created_at;

-- name: AssignAccount :one
UPDATE account SET
    worker_id   = $2,
    last_used_at = COALESCE(last_used_at, now())
WHERE id = $1
RETURNING
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, cookie_expiry_at, refresh_failures, created_at;

-- name: UnassignAccount :one
UPDATE account SET worker_id = NULL
WHERE id = $1
RETURNING
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, cookie_expiry_at, refresh_failures, created_at;

-- name: DeleteAccount :exec
DELETE FROM account WHERE id = $1;

-- name: ListAccountsByWorker :many
SELECT
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, cookie_expiry_at, refresh_failures, created_at
FROM account
WHERE worker_id = $1
ORDER BY platform;

-- name: CountAccountsByWorker :one
SELECT count(*) FROM account WHERE worker_id = $1;

-- --- P2-06 session refresh -------------------------------------------------

-- name: ListAccountsDueForRefresh :many
-- Accounts whose session cookie is about to die and that have a container to
-- re-login on. `$1` is the caller's lead-time deadline (now + 7 days), so the
-- scheduler owns the window and SQL only answers "due yet".
--
-- Three gates, each excluding a state where dispatching would be wrong:
--   cookie_expiry_at <= deadline — only for accounts a login actually dated.
--   auth_status AUTHENTICATED|FAILED — a refresh is due, OR a previous one
--     failed. FAILED must stay eligible or the AC ("gagal 3x -> quarantined")
--     is unreachable: a failed refresh kills the session, the account never
--     returns to AUTHENTICATED by itself, and the streak would stall at 1.
--     AUTHENTICATING/NEEDS_INPUT are excluded: a login is already in flight
--     or parked for a human, and re-dispatching would close that context.
--   status ACTIVE — a QUARANTINED account stays out until an operator resumes
--     it, which is also what resets the failure streak.
--
-- `$3` spaces retries: a FAILED account waits out the backoff measured from
-- last_checked_at (stamped at dispatch) instead of re-firing on every tick.
--
-- These gates MUST stay in sync with the partial predicate of
-- `account_refresh_due_idx` (000015): Postgres only uses a partial index whose
-- predicate the query provably implies, so widening one here without the other
-- silently degrades this query to a seq scan.
SELECT
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, cookie_expiry_at, refresh_failures, created_at
FROM account
WHERE cookie_expiry_at IS NOT NULL
  AND cookie_expiry_at <= $1
  AND auth_status IN ('AUTHENTICATED', 'FAILED')
  AND status = 'ACTIVE'
  AND worker_id IS NOT NULL
  AND (last_checked_at IS NULL OR last_checked_at <= $2)
ORDER BY cookie_expiry_at
LIMIT $3;

-- name: BeginSessionRefresh :exec
-- Mark a refresh in flight. AUTHENTICATING is the same state an operator login
-- sets, so the dashboard shows the live view while it runs; the worker reports
-- the outcome over /internal/account-callback either way.
--
-- last_checked_at is stamped on the way IN, not on the outcome: that is what
-- the retry backoff above measures from, and it also means a dispatch whose
-- worker never reports back still ages out instead of pinning the account.
UPDATE account SET
    auth_status     = 'AUTHENTICATING',
    last_checked_at = now()
WHERE id = $1;
