-- Reverse 000013.
ALTER TABLE apify_run ALTER COLUMN scrape_job_id SET NOT NULL;
COMMENT ON COLUMN apify_run.scrape_job_id IS NULL;
