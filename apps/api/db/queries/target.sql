-- Target: the scrape/action join point. Deduped by (platform, external_id).
-- Upsert returns the canonical row so callers get an id whether it existed or
-- not; the resolution columns (post_id/comment_id) are filled in by the ingest
-- pipeline once the entity exists, not at target-create time.

-- name: GetTargetByID :one
SELECT id, kind, platform, external_id, url, meta, post_id, comment_id, created_at
FROM target
WHERE id = $1;

-- name: GetTargetByPlatformExternalID :one
SELECT id, kind, platform, external_id, url, meta, post_id, comment_id, created_at
FROM target
WHERE platform = $1 AND external_id = $2;

-- name: UpsertTarget :one
INSERT INTO target (kind, platform, external_id, url, meta)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (platform, external_id) DO UPDATE
SET url = EXCLUDED.url,
    meta = EXCLUDED.meta
RETURNING id, kind, platform, external_id, url, meta, post_id, comment_id, created_at;

-- name: LinkTargetPost :exec
UPDATE target SET post_id = $2 WHERE id = $1;

-- name: LinkTargetComment :exec
UPDATE target SET comment_id = $2 WHERE id = $1;

-- name: ListTargets :many
SELECT id, kind, platform, external_id, url, meta, post_id, comment_id, created_at
FROM target
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;
