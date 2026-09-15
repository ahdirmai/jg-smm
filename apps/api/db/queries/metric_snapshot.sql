-- MetricSnapshot: per-post metric time-series (Timescale hypertable).
-- PK is (id, ts) because Timescale requires the partition column in every
-- unique index, so every query filters on `ts` to hit a chunk range.

-- name: CreateMetricSnapshot :exec
INSERT INTO metric_snapshot (post_id, views, likes, comments, shares, reach, reels_views)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListMetricSnapshotsByPost :many
SELECT id, post_id, ts, views, likes, comments, shares, reach, reels_views
FROM metric_snapshot
WHERE post_id = $1
  AND ts      >= $2
  AND ts      <  $3
ORDER BY ts;

-- name: LatestMetricSnapshotsByPost :many
-- `last()` picks the value of the final row in the bucket; with a 1-day bucket
-- over a hypertable this collapses to "the most recent sample per post".
SELECT
    post_id,
    last(views, ts)        AS views,
    last(likes, ts)        AS likes,
    last(comments, ts)     AS comments,
    last(shares, ts)       AS shares,
    last(reach, ts)        AS reach,
    last(reels_views, ts)  AS reels_views,
    max(ts)                AS ts
FROM metric_snapshot
WHERE ts >= now() - INTERVAL '90 days'
GROUP BY post_id;

-- name: TopPostsByMetric :many
-- P2-05 aggregation: top-100 posts by a metric over a trailing window, via a
-- continuous-look-alike query on the latest snapshot per post.
SELECT
    post_id,
    last(views, ts)       AS views,
    last(likes, ts)       AS likes,
    last(comments, ts)    AS comments,
    last(shares, ts)      AS shares,
    last(reach, ts)       AS reach,
    last(reels_views, ts) AS reels_views,
    max(ts)               AS ts
FROM metric_snapshot
WHERE ts >= now() - INTERVAL '90 days'
GROUP BY post_id
ORDER BY (CASE $1::text
            WHEN 'views'       THEN last(views, ts)
            WHEN 'likes'       THEN last(likes, ts)
            WHEN 'comments'    THEN last(comments, ts)
            WHEN 'shares'      THEN last(shares, ts)
            WHEN 'reach'       THEN COALESCE(last(reach, ts), 0)
            WHEN 'reels_views' THEN COALESCE(last(reels_views, ts), 0)
          END) DESC NULLS LAST
LIMIT $2;
