-- Post + Comment: scraped content. Both dedupe by (platform, external_id) so a
-- re-scrape refreshes metrics instead of duplicating rows (idempotent ingest).
-- The columns selected here mirror the Upsert RETURNING clause exactly.

-- name: GetPostByID :one
SELECT id, platform, external_id, author_handle, author_id, text, media_urls,
       metrics, scraped_at, author_account_id, published_at, comments_fetched
FROM post
WHERE id = $1;

-- name: GetPostByPlatformExternalID :one
SELECT id, platform, external_id, author_handle, author_id, text, media_urls,
       metrics, scraped_at, author_account_id, published_at, comments_fetched
FROM post
WHERE platform = $1 AND external_id = $2;

-- name: UpsertPost :one
INSERT INTO post (
    platform, external_id, author_handle, author_id, text, media_urls, metrics,
    author_account_id, published_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $9, $8)
ON CONFLICT (platform, external_id) DO UPDATE
SET text               = EXCLUDED.text,
    media_urls         = EXCLUDED.media_urls,
    metrics            = EXCLUDED.metrics,
    scraped_at         = now(),
    author_account_id  = EXCLUDED.author_account_id,
    published_at        = COALESCE(EXCLUDED.published_at, post.published_at),
    -- author_handle used to be insert-only, so a rename (or a bad first write)
    -- was permanent. It is not cosmetic: postPermalink builds TikTok and X URLs
    -- out of it, so a stale handle is a permalink that 404s forever. The
    -- CHECK (author_handle <> '') already keeps an empty value out.
    author_handle       = EXCLUDED.author_handle
RETURNING id, platform, external_id, author_handle, author_id, text, media_urls,
          metrics, scraped_at, author_account_id, published_at, comments_fetched;

-- name: ListPosts :many
SELECT id, platform, external_id, author_handle, author_id, text, media_urls,
       metrics, scraped_at, author_account_id, published_at, comments_fetched
FROM post
ORDER BY published_at DESC NULLS LAST, scraped_at DESC
LIMIT $1 OFFSET $2;

-- name: ListTopPostsByPlatform :many
-- Top-N posts by a single metric on a platform, for the P2-05 aggregation and
-- the dashboard's "top posts" table. metric_key must be a jsonb text field;
-- `->>` keeps the bind a plain text parameter (no jsonb literal assembly).
SELECT id, platform, external_id, author_handle, author_id, text, media_urls,
       metrics, scraped_at, author_account_id, published_at, comments_fetched
FROM post
WHERE platform = $1
ORDER BY (metrics->>$2::text)::bigint DESC NULLS LAST
LIMIT $3;

-- name: ListRecentPostsByPlatform :many
-- Posts on a platform, newest PUBLICATION first, falling back to scrape time for
-- rows ingested before 000016 (which has no recoverable publish date). The
-- keyword-scrape read-back uses it to show this run's rows in the order they
-- were posted, not the order we happened to fetch them.
SELECT id, platform, external_id, author_handle, author_id, text, media_urls,
       metrics, scraped_at, author_account_id, published_at, comments_fetched
FROM post
WHERE platform = $1
ORDER BY published_at DESC NULLS LAST, scraped_at DESC
LIMIT $2;

-- name: MarkPostCommentsFetched :exec
-- A permalink scrape always fetches the comment bodies, so the post row may be
-- served from the 6h freshness cache afterwards. Keyword-batch ingests never
-- call this (their actor returns no comment bodies), so their rows stay
-- unfetched and the cache keeps paying for them until a real scrape lands.
UPDATE post
SET comments_fetched = true
WHERE id = $1;

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
    -- Same reason as UpsertPost: a handle correction must not be frozen at the
    -- value the first scrape happened to see.
    author_handle = EXCLUDED.author_handle
-- parent_id is deliberately NOT overwritten. The Instagram actor does not
-- return a parent reference, so EXCLUDED.parent_id is always NULL: keeping
-- `parent_id = EXCLUDED.parent_id` made every re-scrape erase a link that some
-- other writer had established. Drop the assignment once the parser can supply
-- one; until then, absence of data must not mean "erase what exists".
RETURNING id, post_id, platform, external_id, author_handle, text, metrics,
          scraped_at, parent_id;

-- name: ListCommentsByPost :many
SELECT id, post_id, platform, external_id, author_handle, text, metrics,
       scraped_at, parent_id
FROM comment
WHERE post_id = $1
ORDER BY scraped_at DESC, id
LIMIT $2 OFFSET $3;

-- name: CountCommentsByPost :one
SELECT count(*) FROM comment WHERE post_id = $1;
