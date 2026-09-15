-- proxy_group queries. pool_key is encrypted at rest (AES-256-GCM, P1-07) and
-- is only ever written by the credential-aware service, never decrypted here.

-- name: GetProxyGroupByID :one
SELECT * FROM proxy_group WHERE id = $1;

-- name: ListProxyGroups :many
SELECT * FROM proxy_group ORDER BY created_at DESC;

-- name: CreateProxyGroup :one
INSERT INTO proxy_group (
    id, name, region, provider, pool_key, max_concurrency, daily_budget_mb
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateProxyGroup :one
UPDATE proxy_group SET
    name            = $2,
    region          = $3,
    provider        = $4,
    max_concurrency = $5,
    daily_budget_mb = $6
WHERE id = $1
RETURNING *;

-- name: DeleteProxyGroup :exec
DELETE FROM proxy_group WHERE id = $1;
