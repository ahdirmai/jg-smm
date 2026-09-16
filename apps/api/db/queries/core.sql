-- Example queries exercising the sqlc pipeline (P0-05).
-- Real per-domain query files land with their tables in later phases.

-- name: GetTeamConfig :one
SELECT id, name, created_at
FROM team_config
LIMIT 1;

-- name: UpsertTeamConfig :one
INSERT INTO team_config (name)
VALUES ($1)
ON CONFLICT ((true)) DO UPDATE SET name = EXCLUDED.name
RETURNING id, name, created_at;

-- name: CreateUser :one
INSERT INTO app_user (email, name, password_hash, role)
VALUES (lower($1), $2, $3, $4)
RETURNING id, email, name, role, created_at;

-- name: GetUserByEmail :one
SELECT id, email, name, password_hash, role, created_at
FROM app_user
WHERE email = lower($1);

-- name: ListUsers :many
SELECT id, email, name, role, created_at
FROM app_user
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountUsers :one
SELECT count(*) FROM app_user;

-- name: UpdateUser :one
UPDATE app_user
SET name = $2, role = $3
WHERE id = $1
RETURNING id, email, name, role, created_at;

-- name: DeleteUser :exec
DELETE FROM app_user WHERE id = $1;

-- name: CountOwnersExcept :one
SELECT count(*) FROM app_user WHERE role = 'OWNER' AND id <> $1;

-- Audit queries live in audit.sql (they carry the ip/result columns added in
-- migration 000010 and join app_user for the actor's display name).
