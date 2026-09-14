-- Auth queries (P0-06).

-- name: GetUserByID :one
SELECT id, email, name, password_hash, role, created_at
FROM app_user
WHERE id = $1;

-- name: CreateAuthSession :one
INSERT INTO auth_session (user_id, token_hash, user_agent, ip, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, user_id, expires_at, created_at;

-- name: GetActiveAuthSession :one
SELECT s.id, s.user_id, s.expires_at, s.revoked_at,
       u.email, u.name, u.role
FROM auth_session s
JOIN app_user u ON u.id = s.user_id
WHERE s.token_hash = $1
  AND s.revoked_at IS NULL
  AND s.expires_at > now();

-- name: TouchAuthSession :exec
UPDATE auth_session SET last_used_at = now() WHERE id = $1;

-- name: RevokeAuthSession :exec
UPDATE auth_session
SET revoked_at = now(), revoked_reason = $2
WHERE token_hash = $1 AND revoked_at IS NULL;

-- name: RevokeAllUserSessions :exec
UPDATE auth_session
SET revoked_at = now(), revoked_reason = $2
WHERE user_id = $1 AND revoked_at IS NULL;

-- name: DeleteExpiredAuthSessions :execrows
DELETE FROM auth_session
WHERE expires_at < now()
   OR (revoked_at IS NOT NULL AND revoked_at < now() - interval '7 days');
