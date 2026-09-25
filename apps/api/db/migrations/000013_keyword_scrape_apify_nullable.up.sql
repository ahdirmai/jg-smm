-- 000013 — keyword search has no single scrape_job.
--
-- apify_run was 1:1 with scrape_job (000004). The on-demand path creates a
-- scrape_job then the apify_run; the keyword search (batulicin, etc.) is an
-- ad-hoc multi-post search with no single target, so it inserts an apify_run
-- with no job. That insert hit `scrape_job_id NOT NULL` and surfaced as
-- `keyword scrape: create run: ... null value` → HTTP 502 in ~10ms without ever
-- reaching Apify (no "keyword scrape: run failed" WARN, no Apify latency).
--
-- Make scrape_job_id nullable. UNIQUE stays — Postgres allows multiple NULLs, so
-- many keyword runs coexist while scheduled/on-demand runs remain 1:1. FK stays
-- for non-null references (ON DELETE CASCADE).
ALTER TABLE apify_run ALTER COLUMN scrape_job_id DROP NOT NULL;

-- Preserve documentation of the original 1:1 intent for job-backed runs.
COMMENT ON COLUMN apify_run.scrape_job_id IS 'nullable: keyword search inserts no scrape_job (ad-hoc multi-post search); scheduled/on-demand runs remain 1:1 via UNIQUE where non-null';
