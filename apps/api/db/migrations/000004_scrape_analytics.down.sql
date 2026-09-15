-- 000004 down — reverse order of the up migration.
-- Hypertable policies/indexes die with the tables; no separate drop needed.

DROP TABLE IF EXISTS analytics_ingest_run;
DROP TABLE IF EXISTS analytics_mention;
DROP TABLE IF EXISTS analytics_snapshot;
DROP TABLE IF EXISTS official_account;
DROP TABLE IF EXISTS metric_snapshot;
DROP TABLE IF EXISTS raw_payload;
DROP TABLE IF EXISTS apify_run;
DROP TABLE IF EXISTS scrape_job;
DROP TABLE IF EXISTS target;
DROP TABLE IF EXISTS comment;
DROP TABLE IF EXISTS post;

DROP TYPE IF EXISTS ingest_status;
DROP TYPE IF EXISTS analytics_provider;
DROP TYPE IF EXISTS official_account_status;
DROP TYPE IF EXISTS target_kind;
DROP TYPE IF EXISTS job_status;
DROP TYPE IF EXISTS job_type;

-- timescaledb is a shared extension; left installed because P5 still uses it.
