-- Post + Comment: scraped content. Both dedupe by (platform, external_id) so a
-- re-scrape refreshes metrics instead of duplicating rows (idempotent ingest).
-- The columns selected here mirror the Upsert RETURNING clause exactly.

-- name: GetPostByID :one
SELECT id, platform, external_id, author_handle, author_id, text, media_urls,
       metrics, scraped_at, author_account_id, published_at, comments_fetched,
       sentiment, sentiment_score, sentiment_at
FROM post
WHERE id = $1;

-- name: GetPostByPlatformExternalID :one
SELECT id, platform, external_id, author_handle, author_id, text, media_urls,
       metrics, scraped_at, author_account_id, published_at, comments_fetched,
       sentiment, sentiment_score, sentiment_at
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
          metrics, scraped_at, author_account_id, published_at, comments_fetched,
          sentiment, sentiment_score, sentiment_at;

-- name: ListPosts :many
SELECT id, platform, external_id, author_handle, author_id, text, media_urls,
       metrics, scraped_at, author_account_id, published_at, comments_fetched,
       sentiment, sentiment_score, sentiment_at
FROM post
ORDER BY published_at DESC NULLS LAST, scraped_at DESC
LIMIT $1 OFFSET $2;

-- name: ListTopPostsByPlatform :many
-- Top-N posts by a single metric on a platform, for the P2-05 aggregation and
-- the dashboard's "top posts" table. metric_key must be a jsonb text field;
-- `->>` keeps the bind a plain text parameter (no jsonb literal assembly).
SELECT id, platform, external_id, author_handle, author_id, text, media_urls,
       metrics, scraped_at, author_account_id, published_at, comments_fetched,
       sentiment, sentiment_score, sentiment_at
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
       metrics, scraped_at, author_account_id, published_at, comments_fetched,
       sentiment, sentiment_score, sentiment_at
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
       scraped_at, parent_id, sentiment, sentiment_score, sentiment_at
FROM comment
WHERE id = $1;

-- name: GetCommentByPlatformExternalID :one
SELECT id, post_id, platform, external_id, author_handle, text, metrics,
       scraped_at, parent_id, sentiment, sentiment_score, sentiment_at
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
    author_handle = EXCLUDED.author_handle,
    -- parent_id is COALESCEd, not assigned. A reply parser supplies one; a
    -- top-level comment supplies NULL (its actor row has no parent), and the
    -- previous plain assignment let the second case ERASE a link the first case
    -- had written. COALESCE keeps an existing link when the new payload has
    -- none, and still lets a real parent ref correct a wrong one.
    parent_id  = COALESCE(EXCLUDED.parent_id, comment.parent_id)
RETURNING id, post_id, platform, external_id, author_handle, text, metrics,
          scraped_at, parent_id, sentiment, sentiment_score, sentiment_at;

-- name: ListCommentsByPost :many
SELECT id, post_id, platform, external_id, author_handle, text, metrics,
       scraped_at, parent_id, sentiment, sentiment_score, sentiment_at
FROM comment
WHERE post_id = $1
ORDER BY scraped_at DESC, id
LIMIT $2 OFFSET $3;

-- name: CountCommentsByPost :one
SELECT count(*) FROM comment WHERE post_id = $1;

-- name: SetCommentSentiment :exec
-- Writes one analysed verdict. sentiment_at is set from the caller's clock so
-- the row records WHEN analysis happened, which is what makes "already
-- analysed" decidable on a later scrape.
UPDATE comment
SET sentiment = $2, sentiment_score = $3, sentiment_at = now()
WHERE id = $1;

-- name: ListCommentsNeedingSentiment :many
-- Comments with no verdict yet, for the backfill path. Ordered oldest-first so
-- a bounded backfill drains a post in insertion order and a rerun picks up
-- where the budget ran out.
SELECT id, post_id, platform, external_id, author_handle, text, metrics,
       scraped_at, parent_id, sentiment, sentiment_score, sentiment_at
FROM comment
WHERE post_id = $1 AND sentiment IS NULL
ORDER BY scraped_at, id
LIMIT $2;

-- name: CountCommentsBySentiment :many
-- The per-post distribution the dashboard renders. Unanalysed rows are counted
-- by the caller (count - analysed), so this only returns labels that exist.
SELECT sentiment, count(*) AS n
FROM comment
WHERE post_id = $1 AND sentiment IS NOT NULL
GROUP BY sentiment;
