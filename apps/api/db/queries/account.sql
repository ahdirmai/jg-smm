-- Account (executor identity) queries. password_enc is WRITE-ONLY: no query
-- here ever selects it. UNIQUE(platform, username) + UNIQUE(worker_id, platform).

-- name: GetAccountByID :one
SELECT
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, created_at
FROM account
WHERE id = $1;

-- name: ListAccounts :many
SELECT
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, created_at
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
    last_checked_at, last_error, created_at;

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
    last_error       = $12
WHERE id = $1
RETURNING
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, created_at;

-- name: AssignAccount :one
UPDATE account SET
    worker_id   = $2,
    last_used_at = COALESCE(last_used_at, now())
WHERE id = $1
RETURNING
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, created_at;

-- name: UnassignAccount :one
UPDATE account SET worker_id = NULL
WHERE id = $1
RETURNING
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, created_at;

-- name: DeleteAccount :exec
DELETE FROM account WHERE id = $1;

-- name: ListAccountsByWorker :many
SELECT
    id, platform, username, auth_status, handle, last_verified_at,
    proxy_group_id, health_score, status, tags, worker_id, last_used_at,
    last_checked_at, last_error, created_at
FROM account
WHERE worker_id = $1
ORDER BY platform;

-- name: CountAccountsByWorker :one
SELECT count(*) FROM account WHERE worker_id = $1;
