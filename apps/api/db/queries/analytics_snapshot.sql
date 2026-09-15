-- AnalyticsSnapshot: official-account metric time-series (Timescale hypertable).
-- Idempotent upsert on (official_account_id, ts, provider) so a retried ingest
-- run refreshes the same row instead of duplicating it.
--
-- `ts` is the hypertable partition column, so every query filters on it (a
-- filter on `ts >= X` alone is enough for chunk exclusion; the upper bound is
-- stated explicitly so `time_bucket_gapfill` can infer its range).

-- name: UpsertAnalyticsSnapshot :one
INSERT INTO analytics_snapshot (
    official_account_id, platform, ts, followers, reach, views, mentions,
    engagements, profile_views, metrics, provider, provider_run_id, fetched_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, now())
ON CONFLICT (official_account_id, ts, provider) DO UPDATE
SET followers       = EXCLUDED.followers,
    reach           = EXCLUDED.reach,
    views           = EXCLUDED.views,
    mentions        = EXCLUDED.mentions,
    engagements     = EXCLUDED.engagements,
    profile_views   = EXCLUDED.profile_views,
    metrics         = EXCLUDED.metrics,
    provider_run_id = EXCLUDED.provider_run_id,
    fetched_at      = now()
RETURNING id, official_account_id, platform, ts, followers, reach, views,
          mentions, engagements, profile_views, metrics, provider,
          provider_run_id, fetched_at;

-- name: ListAnalyticsSnapshotsByAccount :many
SELECT id, official_account_id, platform, ts, followers, reach, views, mentions,
       engagements, profile_views, metrics, provider, provider_run_id, fetched_at
FROM analytics_snapshot
WHERE official_account_id = $1
  AND ts >= $2
  AND ts <  $3
ORDER BY ts;

-- name: AnalyticsOverviewByPlatform :many
-- KPI strip per platform: latest value of each scalar metric per account on
-- that platform over the trailing window. `last(x, ts)` over the bucket gives
-- the most recent sample per account without a window-function round trip.
SELECT
    official_account_id,
    last(followers, ts)     AS followers,
    last(reach, ts)         AS reach,
    last(views, ts)         AS views,
    last(mentions, ts)      AS mentions,
    last(engagements, ts)   AS engagements,
    last(profile_views, ts) AS profile_views,
    max(ts)                 AS ts
FROM analytics_snapshot
WHERE platform = $1
  AND ts      >= now() - $2::interval
  AND ts      <  now()
GROUP BY official_account_id;

-- name: AnalyticsTrendByPlatform :many
-- Daily series of one metric summed across all accounts on a platform. The
-- metric is picked by name so one query serves every KPI card; every branch is
-- bigint so the CASE is type-homogeneous. gapfill needs both time bounds, which
-- the WHERE supplies.
SELECT
    time_bucket_gapfill('1 day', ts, now() - $3::interval, now()) AS bucket,
    COALESCE(sum(CASE $2::text
            WHEN 'followers'     THEN followers
            WHEN 'reach'         THEN COALESCE(reach, 0)
            WHEN 'views'         THEN COALESCE(views, 0)
            WHEN 'mentions'      THEN COALESCE(mentions, 0)
            WHEN 'engagements'   THEN COALESCE(engagements, 0)
            WHEN 'profile_views' THEN COALESCE(profile_views, 0)
            ELSE 0::bigint
        END), 0)::bigint AS value
FROM analytics_snapshot
WHERE platform = $1
  AND ts      >= now() - $3::interval
  AND ts      <  now()
GROUP BY bucket
ORDER BY bucket;

-- name: AnalyticsTrendByAccount :many
-- Same shape, scoped to one account (the per-account drill-down view).
SELECT
    time_bucket_gapfill('1 day', ts, now() - $3::interval, now()) AS bucket,
    COALESCE(sum(CASE $2::text
            WHEN 'followers'     THEN followers
            WHEN 'reach'         THEN COALESCE(reach, 0)
            WHEN 'views'         THEN COALESCE(views, 0)
            WHEN 'mentions'      THEN COALESCE(mentions, 0)
            WHEN 'engagements'   THEN COALESCE(engagements, 0)
            WHEN 'profile_views' THEN COALESCE(profile_views, 0)
            ELSE 0::bigint
        END), 0)::bigint AS value
FROM analytics_snapshot
WHERE official_account_id = $1
  AND ts      >= now() - $3::interval
  AND ts      <  now()
GROUP BY bucket
ORDER BY bucket;
