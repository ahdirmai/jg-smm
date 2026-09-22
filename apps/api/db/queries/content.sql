-- Post + Comment: scraped content. Both dedupe by (platform, external_id) so a
-- re-scrape refreshes metrics instead of duplicating rows (idempotent ingest).
-- The columns selected here mirror the Upsert RETURNING clause exactly.

-- name: GetPostByID :one
SELECT id, platform, external_id, author_handle, author_id, text, media_urls,
       metrics, scraped_at, author_account_id
FROM post
WHERE id = $1;

-- name: GetPostByPlatformExternalID :one
SELECT id, platform, external_id, author_handle, author_id, text, media_urls,
       metrics, scraped_at, author_account_id
FROM post
WHERE platform = $1 AND external_id = $2;

-- name: UpsertPost :one
INSERT INTO post (
    platform, external_id, author_handle, author_id, text, media_urls, metrics,
    author_account_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (platform, external_id) DO UPDATE
SET text               = EXCLUDED.text,
    media_urls         = EXCLUDED.media_urls,
    metrics            = EXCLUDED.metrics,
    scraped_at         = now(),
    author_account_id  = EXCLUDED.author_account_id
RETURNING id, platform, external_id, author_handle, author_id, text, media_urls,
          metrics, scraped_at, author_account_id;

-- name: ListPosts :many
SELECT id, platform, external_id, author_handle, author_id, text, media_urls,
       metrics, scraped_at, author_account_id
FROM post
ORDER BY scraped_at DESC
LIMIT $1 OFFSET $2;

-- name: ListTopPostsByPlatform :many
-- Top-N posts by a single metric on a platform, for the P2-05 aggregation and
-- the dashboard's "top posts" table. metric_key must be a jsonb text field;
-- `->>` keeps the bind a plain text parameter (no jsonb literal assembly).
SELECT id, platform, external_id, author_handle, author_id, text, media_urls,
       metrics, scraped_at, author_account_id
FROM post
WHERE platform = $1
ORDER BY (metrics->>$2::text)::bigint DESC NULLS LAST
LIMIT $3;

-- name: ListRecentPostsByPlatform :many
-- Posts on a platform, newest scrape first. The keyword-scrape read-back uses
-- it (this run's rows just got the freshest scraped_at), not a metric ranking.
SELECT id, platform, external_id, author_handle, author_id, text, media_urls,
       metrics, scraped_at, author_account_id
FROM post
WHERE platform = $1
ORDER BY scraped_at DESC
LIMIT $2;

-- name: GetCommentByID :one
SELECT id, post_id, platform, external_id, author_handle, text, metrics,
       scraped_at, parent_id
FROM comment
WHERE id = $1;

-- name: GetCommentByPlatformExternalID :one
SELECT id, post_id, platform, external_id, author_handle, text, metrics,
       scraped_at, parent_id
FROM comment
WHERE platform = $1 AND external_id = $2;

-- name: UpsertComment :one
INSERT INTO comment (
    post_id, platform, external_id, author_handle, text, metrics, parent_id
) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (platform, external_id) DO UPDATE
SET text       = EXCLUDED.text,
    metrics    = EXCLUDED.metrics,
    scraped_at = now(),
    parent_id  = EXCLUDED.parent_id
RETURNING id, post_id, platform, external_id, author_handle, text, metrics,
          scraped_at, parent_id;

-- name: ListCommentsByPost :many
SELECT id, post_id, platform, external_id, author_handle, text, metrics,
       scraped_at, parent_id
FROM comment
WHERE post_id = $1
ORDER BY scraped_at DESC
LIMIT $2 OFFSET $3;

-- name: CountCommentsByPost :one
SELECT count(*) FROM comment WHERE post_id = $1;
