-- name: UpsertContentMetric :one
-- Per-content metrics are a lifetime gauge, so a re-ingest refreshes the row
-- for a media id in place. The unique index on
-- (official_account_id, external_id, provider) is what makes this an upsert
-- rather than an append: without it every 30-minute run would insert another
-- copy of the same post and the "top posts" table would rank duplicates.
--
-- COALESCE on the scalar columns: a metric this run did not report must not
-- blank out a value a previous run did. A provider that omits `replies` today
-- should not erase yesterday's reply count.
INSERT INTO analytics_content_metric (
    official_account_id, platform, external_id, content_type, caption,
    permalink, published_at, reach, views, interactions, likes, comments,
    saves, shares, profile_views, metrics, provider, fetched_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18
)
ON CONFLICT (official_account_id, external_id, provider) DO UPDATE SET
    content_type  = COALESCE(EXCLUDED.content_type, analytics_content_metric.content_type),
    caption       = COALESCE(EXCLUDED.caption, analytics_content_metric.caption),
    permalink     = COALESCE(EXCLUDED.permalink, analytics_content_metric.permalink),
    published_at  = COALESCE(EXCLUDED.published_at, analytics_content_metric.published_at),
    reach         = COALESCE(EXCLUDED.reach, analytics_content_metric.reach),
    views         = COALESCE(EXCLUDED.views, analytics_content_metric.views),
    interactions  = COALESCE(EXCLUDED.interactions, analytics_content_metric.interactions),
    likes         = COALESCE(EXCLUDED.likes, analytics_content_metric.likes),
    comments      = COALESCE(EXCLUDED.comments, analytics_content_metric.comments),
    saves         = COALESCE(EXCLUDED.saves, analytics_content_metric.saves),
    shares        = COALESCE(EXCLUDED.shares, analytics_content_metric.shares),
    profile_views = COALESCE(EXCLUDED.profile_views, analytics_content_metric.profile_views),
    metrics       = EXCLUDED.metrics,
    fetched_at    = EXCLUDED.fetched_at
RETURNING *;

-- name: ListContentMetrics :many
-- The per-platform "top content" table. Ordered by reach because that is the
-- headline number, with interactions as the tie-break so two posts with the
-- same reach do not swap places between renders. NULLS LAST keeps a post the
-- provider has no reach for at the bottom instead of pinning it to the top.
SELECT c.id, c.official_account_id, c.platform, c.external_id, c.content_type,
       c.caption, c.permalink, c.published_at, c.reach, c.views,
       c.interactions, c.likes, c.comments, c.saves, c.shares,
       c.profile_views, c.metrics, c.provider, c.fetched_at,
       a.handle AS account_handle
FROM analytics_content_metric c
JOIN official_account a ON a.id = c.official_account_id
WHERE c.platform = $1
  AND ($2::uuid[] IS NULL OR c.official_account_id = ANY($2::uuid[]))
  AND ($3::interval IS NULL OR c.published_at >= now() - $3::interval)
ORDER BY c.reach DESC NULLS LAST, c.interactions DESC NULLS LAST, c.published_at DESC
LIMIT $4;
