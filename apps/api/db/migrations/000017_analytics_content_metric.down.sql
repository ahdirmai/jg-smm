-- 000017 — drops analytics_content_metric. See the .up.sql for why the table is
-- not a hypertable. Down is a plain DROP: per-post metrics are re-fetchable
-- from the provider on the next ingest, so unlike the published_at migration
-- this rollback loses no fact that cannot be reconstructed.
DROP TABLE IF EXISTS analytics_content_metric;
