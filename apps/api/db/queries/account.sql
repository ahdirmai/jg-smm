-- Account (executor identity) queries. password_enc is WRITE-ONLY: it is
-- never selected into an Account row, with one narrow exception below
-- (GetAccountPasswordEnc) that exists solely for the auth-login dispatch.

-- name: GetAccountByID :one
SELECT
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, cookie_expiry_at, refresh_failures, created_at,
    location, latitude, longitude, timezone, user_agent
FROM account
WHERE id = $1;

-- name: GetAccountPasswordEnc :one
-- The single narrow read of the credential blob: the auth-login dispatch
-- unseals it once and rides it on the private per-worker control channel so
-- the worker can fill the login form. No read model, API response, or log
-- line ever carries the value.
SELECT password_enc FROM account
WHERE id = $1;

-- name: ListAccounts :many
SELECT
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, cookie_expiry_at, refresh_failures, created_at,
    location, latitude, longitude, timezone, user_agent
FROM account
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: CreateAccount :one
INSERT INTO account (
    platform, username, password_enc, auth_status, proxy_group_id,
    status, tags, worker_id,
    location, latitude, longitude, timezone, user_agent
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
)
RETURNING
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, cookie_expiry_at, refresh_failures, created_at,
    location, latitude, longitude, timezone, user_agent;

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
    refresh_failures = $14,
    -- The device identity is operator input, not derived state: a boxd worker
    -- can be any machine, so the account owns the city/GPS/timezone it reports.
    location         = $15,
    latitude         = $16,
    longitude        = $17,
    timezone         = $18,
    user_agent       = $19
WHERE id = $1
RETURNING
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, cookie_expiry_at, refresh_failures, created_at,
    location, latitude, longitude, timezone, user_agent;

-- name: AssignAccount :one
UPDATE account SET
    worker_id   = $2,
    last_used_at = COALESCE(last_used_at, now())
WHERE id = $1
RETURNING
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, cookie_expiry_at, refresh_failures, created_at,
    location, latitude, longitude, timezone, user_agent;

-- name: UnassignAccount :one
UPDATE account SET worker_id = NULL
WHERE id = $1
RETURNING
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, cookie_expiry_at, refresh_failures, created_at,
    location, latitude, longitude, timezone, user_agent;

-- name: DeleteAccount :exec
DELETE FROM account WHERE id = $1;

-- name: ListAccountsByWorker :many
SELECT
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, cookie_expiry_at, refresh_failures, created_at,
    location, latitude, longitude, timezone, user_agent
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
-- `account_refresh_due_idx` (000024, which replaced the 000023 one): Postgres
-- only uses a partial index whose predicate the query provably implies, so
-- widening one here without the other silently degrades this query to a seq
-- scan.
SELECT
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, cookie_expiry_at, refresh_failures, created_at,
    location, latitude, longitude, timezone, user_agent
FROM account
WHERE (
    -- A first login: never attempted, so there is no expiry to compare. PENDING
    -- is the only status that means this (see 000022); it is due immediately.
    (auth_status = 'PENDING' AND cookie_expiry_at IS NULL)
    OR (
      cookie_expiry_at IS NOT NULL
      AND cookie_expiry_at <= $1
      AND auth_status IN ('AUTHENTICATED', 'FAILED')
    )
  )
  AND status = 'ACTIVE'
  -- A leased account (worker_id set) is a login/CAPTCHA/live-view parked on a
  -- worker; re-dispatching would close that context under the operator.
  AND worker_id IS NULL
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

-- name: CountInFlightLogins :one
-- How many logins are unresolved right now, fleet-wide. AUTHENTICATING is the
-- state BeginSessionRefresh and the operator Login both set, and it is cleared
-- only by an outcome callback or the stale sweep; NEEDS_INPUT is a login parked
-- for the operator, which is still a live browser holding a platform session.
-- Both count toward the concurrency ceiling, because both are open logins from
-- this fleet's IP.
SELECT count(*) FROM account WHERE auth_status IN ('AUTHENTICATING', 'NEEDS_INPUT');

-- name: ListStaleLoginAttempts :many
-- Accounts stranded in AUTHENTICATING: a login was dispatched and no callback
-- ever came back, so the row sits in a state the due query deliberately
-- excludes (see ListAccountsDueForRefresh above) and nothing else would ever
-- move it. A paused worker that never wakes, a control message published
-- before the worker subscribed, or a container killed mid-login all land here,
-- and the account is invisible to the operator until a human notices.
--
-- `$1` is the cutoff: attempts older than it are considered abandoned. It is
-- deliberately generous relative to the worker's own 180s poll, because a
-- login through noVNC is the OPERATOR's turn — a human reading a 2FA prompt
-- holds the row legitimately for minutes. Only an attempt that has outlived
-- any plausible human interaction is reclaimed.
--
-- NEEDS_INPUT is NOT included: that state means the worker is parked and
-- waiting, which is a stable resting state, not a lost one.
SELECT
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, cookie_expiry_at, refresh_failures, created_at,
    location, latitude, longitude, timezone, user_agent
FROM account
WHERE auth_status = 'AUTHENTICATING'
  AND last_checked_at IS NOT NULL
  AND last_checked_at <= $1
ORDER BY last_checked_at
LIMIT $2;

-- name: ReclaimStaleLoginAttempt :exec
-- Return one abandoned AUTHENTICATING row to a state the scheduler will pick
-- up again. FAILED (not AUTHENTICATED) because the truthful summary of "we
-- never learned the outcome" is that the session is unproven: claiming
-- AUTHENTICATED would put the dashboard and the action path on a cookie we
-- have no evidence for, and AUTHENTICATED rows are only re-checked near their
-- expiry, so the account would stay wrong for days.
--
-- cookie_expiry_at is cleared with it. Leaving a stale future date in place
-- would make the next dispatch look like a REFRESH of a live session, which
-- counts toward the P2-06 quarantine streak (RecordAuthOutcome only increments
-- when an expiry exists) — so reclaiming must not manufacture that history.
-- The timestamp guard keeps this idempotent: if the worker reported back
-- between the list and this write, the row is no longer AUTHENTICATING and the
-- update is a no-op rather than a stomp over a real outcome.
UPDATE account SET
    auth_status      = 'FAILED',
    last_error       = 'login attempt abandoned: no callback from the worker',
    last_checked_at  = now(),
    cookie_expiry_at = NULL,
    -- A worker lease left by an abandoned login must not survive it: the row
    -- would stay invisible to the due query and the worker would look busy to
    -- the slot coordinator forever.
    worker_id        = NULL
WHERE id = $1
  AND auth_status = 'AUTHENTICATING'
  AND last_checked_at = $2;

-- --- stateless pool: worker lease & location bootstrap (000024) ------------

-- name: AcquireAccountWorkerLease :one
-- Pin one account to one free pool worker for an interactive session (login,
-- CAPTCHA, live view). Two conditions, both in the WHERE so the check and the
-- write cannot race another request:
--   * the account is unleased or already leased to this same worker
--     (idempotent re-dispatch),
--   * no OTHER account holds this worker — the Xvfb display shows one browser,
--     and the slot coordinator's Redis key is a cache in front of this rule,
--     not the authority for it.
-- Returns no row (-> domain.ErrConflict) when either holds.
UPDATE account AS target SET
    worker_id       = $2,
    last_checked_at = now()
WHERE target.id = $1
  AND (target.worker_id IS NULL OR target.worker_id = $2)
  AND NOT EXISTS (
      SELECT 1 FROM account other
      WHERE other.id <> target.id AND other.worker_id = $2
  )
RETURNING
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, cookie_expiry_at, refresh_failures, created_at,
    location, latitude, longitude, timezone, user_agent;

-- name: ReleaseAccountWorkerLease :exec
-- Return an account to the schedulable pool. Guarded on the worker id so a
-- late release from a previous session cannot free a lease a newer one holds.
--
-- last_checked_at is cleared with the lease. Acquiring stamps it, and the due
-- query treats it as "last dispatch, respect the retry backoff" — so leaving
-- it behind would hide the account for RetryAfter (6h) after every interactive
-- login, which is the opposite of what releasing means.
UPDATE account SET worker_id = NULL, last_checked_at = NULL
WHERE id = $1 AND worker_id = $2;

-- name: ListUnlocatedAccounts :many
-- Accounts still missing a city/GPS identity, for the startup backfill. Bounded
-- so one tick cannot pin the whole table in memory.
SELECT
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, cookie_expiry_at, refresh_failures, created_at,
    location, latitude, longitude, timezone, user_agent
FROM account
WHERE location IS NULL
ORDER BY created_at ASC
LIMIT $1;

-- name: ListAllAccounts :many
-- Every account, ordered deterministically, for grouping that cannot be done in
-- SQL (region rollup). Replaces paginated reads that silently truncated a
-- 486-account fleet at 200.
SELECT
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, cookie_expiry_at, refresh_failures, created_at,
    location, latitude, longitude, timezone, user_agent
FROM account
ORDER BY created_at DESC;
