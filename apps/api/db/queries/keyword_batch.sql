-- Keyword batch (background scrape): one async run per keyword search.
-- POST /api/scrape/keywords inserts PENDING then a goroutine drives RUNNING→SUCCEEDED/FAILED.

-- name: CreateKeywordBatch :one
INSERT INTO keyword_batch (platform, keywords, window_from, window_to, max_posts, actor_id, status, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, platform, keywords, window_from, window_to, max_posts, actor_id, status, apify_run_id, items_read, posts_count, comments_count, error, created_by, created_at, finished_at;

-- name: GetKeywordBatchByID :one
SELECT id, platform, keywords, window_from, window_to, max_posts, actor_id, status, apify_run_id, items_read, posts_count, comments_count, error, created_by, created_at, finished_at
FROM keyword_batch
WHERE id = $1;

-- name: ListKeywordBatches :many
SELECT id, platform, keywords, window_from, window_to, max_posts, actor_id, status, apify_run_id, items_read, posts_count, comments_count, error, created_by, created_at, finished_at
FROM keyword_batch
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: UpdateKeywordBatchStatus :one
-- Drives PENDING→RUNNING→SUCCEEDED/FAILED. finished_at set only on terminal.
UPDATE keyword_batch
SET status = $2, apify_run_id = COALESCE($3, apify_run_id), items_read = $4, posts_count = $5, comments_count = $6, error = $7, finished_at = $8
WHERE id = $1
RETURNING id, platform, keywords, window_from, window_to, max_posts, actor_id, status, apify_run_id, items_read, posts_count, comments_count, error, created_by, created_at, finished_at;

-- name: CreateKeywordBatchPost :exec
INSERT INTO keyword_batch_post (keyword_batch_id, post_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: ListKeywordBatchPosts :many
SELECT p.id, p.platform, p.external_id, p.author_handle, p.author_id, p.text, p.media_urls, p.metrics, p.scraped_at, p.author_account_id
FROM post p
JOIN keyword_batch_post kbp ON kbp.post_id = p.id
WHERE kbp.keyword_batch_id = $1
ORDER BY p.scraped_at DESC
LIMIT $2 OFFSET $3;
