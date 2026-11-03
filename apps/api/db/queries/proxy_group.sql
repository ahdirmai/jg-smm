-- proxy_group queries. pool_key is encrypted at rest (AES-256-GCM, P1-07) and
-- is only ever written by the credential-aware service, never decrypted here.

-- name: GetProxyGroupByID :one
SELECT * FROM proxy_group WHERE id = $1;

-- name: ListProxyGroups :many
SELECT * FROM proxy_group ORDER BY created_at DESC;

-- name: GetProxyGroupForWorker :one
-- The proxy pool a worker's container should egress through, or no rows when
-- the worker's accounts carry no group.
--
-- Derived from the worker's FIRST account rather than stored on `worker`,
-- because the binding lives on `account` (P1-07: a pool is bound "at region
-- level" per account) and a container is a device, not a pool member. Several
-- accounts on one container may name different groups; the container can only
-- have one egress (Playwright fixes the proxy at launch), so the earliest
-- binding wins and is stable across reconciles, which is what keeps a
-- reconciled container on the same IP it had before.
SELECT pg.*
FROM proxy_group pg
JOIN account a ON a.proxy_group_id = pg.id
WHERE a.worker_id = $1
ORDER BY a.created_at ASC, a.id ASC
LIMIT 1;

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
