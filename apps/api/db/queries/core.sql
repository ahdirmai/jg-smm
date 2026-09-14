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

-- name: InsertAuditLog :one
INSERT INTO audit_log (actor_id, action, entity, entity_id, diff)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, actor_id, action, entity, entity_id, diff, ts;

-- name: ListAuditLogs :many
SELECT id, actor_id, action, entity, entity_id, diff, ts
FROM audit_log
ORDER BY ts DESC
LIMIT $1;
