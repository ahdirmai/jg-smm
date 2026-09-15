-- AnalyticsMention + AnalyticsIngestRun: provider-sourced mentions and the
-- audit trail for the analytics ingest path (mirrors provision_log for workers).

-- name: UpsertAnalyticsMention :one
INSERT INTO analytics_mention (
    official_account_id, platform, external_id, author_handle, text, url,
    posted_at, sentiment, fetched_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
ON CONFLICT (platform, external_id) DO UPDATE
SET author_handle = EXCLUDED.author_handle,
    text          = EXCLUDED.text,
    url           = EXCLUDED.url,
    posted_at     = EXCLUDED.posted_at,
    sentiment     = EXCLUDED.sentiment,
    fetched_at    = now()
RETURNING id, official_account_id, platform, external_id, author_handle, text,
          url, posted_at, sentiment, fetched_at;

-- name: ListAnalyticsMentionsByAccount :many
SELECT id, official_account_id, platform, external_id, author_handle, text, url,
       posted_at, sentiment, fetched_at
FROM analytics_mention
WHERE official_account_id = $1
ORDER BY posted_at DESC
LIMIT $2 OFFSET $3;

-- name: ListAnalyticsMentionsByPlatform :many
SELECT id, official_account_id, platform, external_id, author_handle, text, url,
       posted_at, sentiment, fetched_at
FROM analytics_mention
WHERE platform = $1
ORDER BY posted_at DESC
LIMIT $2 OFFSET $3;

-- name: CreateAnalyticsIngestRun :one
INSERT INTO analytics_ingest_run (provider, scope, status)
VALUES ($1, $2, 'RUNNING')
RETURNING id, provider, scope, status, started_at, finished_at, accounts_ok,
          accounts_err, error_class, error;

-- name: UpdateAnalyticsIngestRun :one
UPDATE analytics_ingest_run
SET status       = $2,
    accounts_ok  = $3,
    accounts_err = $4,
    error_class  = $5,
    error        = $6,
    finished_at  = now()
WHERE id = $1
RETURNING id, provider, scope, status, started_at, finished_at, accounts_ok,
          accounts_err, error_class, error;

-- name: ListAnalyticsIngestRuns :many
SELECT id, provider, scope, status, started_at, finished_at, accounts_ok,
       accounts_err, error_class, error
FROM analytics_ingest_run
ORDER BY started_at DESC
LIMIT $1 OFFSET $2;

-- name: GetLatestAnalyticsIngestRun :one
-- backs the dashboard's freshness badge: the most recent terminal run tells the
-- operator whether the analytics view they are reading is current or stale.
SELECT id, provider, scope, status, started_at, finished_at, accounts_ok,
       accounts_err, error_class, error
FROM analytics_ingest_run
WHERE status IN ('SUCCESS', 'PARTIAL', 'FAILED')
ORDER BY started_at DESC
LIMIT 1;
