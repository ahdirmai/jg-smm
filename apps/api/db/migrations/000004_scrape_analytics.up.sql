-- 000004 — P2: scrape pipeline + official-account analytics.
-- Scope: JobType/JobStatus/TargetKind/OfficialAccountStatus/AnalyticsProvider/
-- IngestStatus enums; Post, Comment, Target, ScrapeJob, ApifyRun, RawPayload,
-- MetricSnapshot (hypertable), OfficialAccount, AnalyticsSnapshot (hypertable),
-- AnalyticsMention, AnalyticsIngestRun.
--
-- Invariants (DEVELOPMENT_RULE.md §DB, ERD.md):
--   * Post / Comment / AnalyticsMention dedupe by (platform, external_id)
--     -> idempotent ingest: re-running a scrape never duplicates rows.
--   * Target is the join point between scrape and action; a Target points at a
--     Post XOR a Comment, never both.
--   * MetricSnapshot / AnalyticsSnapshot are Timescale hypertables. Timescale
--     requires the partition column (`ts`) in EVERY unique index, so the PK is
--     (id, ts) on both. Chunked by `ts`; retention drops whole chunks, so every
--     time-series query must filter on `ts`.
--   * OfficialAccount is NOT an `account` (executor). No credentials, no login,
--     no actions — read-only subject of analytics fed by a 3rd-party provider.

CREATE EXTENSION IF NOT EXISTS timescaledb;

-- ---------------------------------------------------------------------------
-- enums
-- ---------------------------------------------------------------------------
CREATE TYPE job_type AS ENUM (
    'SCRAPE_LIKE', 'SCRAPE_COMMENT', 'SCRAPE_METRIC', 'SESSION_REFRESH',
    'ACTION_LIKE', 'ACTION_COMMENT'
);

CREATE TYPE job_status AS ENUM (
    'PENDING', 'RUNNING', 'SUCCESS', 'FAILED', 'RETRY', 'CANCELLED'
);

CREATE TYPE target_kind AS ENUM ('POST', 'COMMENT');

CREATE TYPE official_account_status AS ENUM ('ACTIVE', 'PAUSED', 'ARCHIVED');

-- provider-agnostic; concrete values land when the real integration is wired.
CREATE TYPE analytics_provider AS ENUM ('THIRDPARTY_A', 'THIRDPARTY_B');

CREATE TYPE ingest_status AS ENUM (
    'PENDING', 'RUNNING', 'SUCCESS', 'FAILED', 'PARTIAL'
);

-- ---------------------------------------------------------------------------
-- post — a scraped post. Deduped by (platform, external_id) so re-scraping the
-- same URL refreshes `metrics` instead of inserting a duplicate.
-- ---------------------------------------------------------------------------
CREATE TABLE post (
    id                uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    platform          platform    NOT NULL,
    external_id       text        NOT NULL,
    author_handle     text        NOT NULL,
    author_id         text        NOT NULL,
    text              text,
    media_urls        text[]      NOT NULL DEFAULT '{}',
    metrics           jsonb       NOT NULL DEFAULT '{}',
    scraped_at        timestamptz NOT NULL DEFAULT now(),
    author_account_id uuid        REFERENCES account (id) ON DELETE SET NULL,
    CONSTRAINT post_platform_external_id_key UNIQUE (platform, external_id),
    CONSTRAINT post_author_handle_not_blank CHECK (length(btrim(author_handle)) > 0)
);

CREATE INDEX post_author_handle_idx ON post (author_handle);
CREATE INDEX post_scraped_at_idx ON post (scraped_at DESC);

-- ---------------------------------------------------------------------------
-- comment — a scraped comment, self-referential for threaded replies.
-- ---------------------------------------------------------------------------
CREATE TABLE comment (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id       uuid        NOT NULL REFERENCES post (id) ON DELETE CASCADE,
    platform      platform    NOT NULL,
    external_id   text        NOT NULL,
    author_handle text        NOT NULL,
    text          text        NOT NULL,
    metrics       jsonb       NOT NULL DEFAULT '{}',
    scraped_at    timestamptz NOT NULL DEFAULT now(),
    parent_id     uuid        REFERENCES comment (id) ON DELETE CASCADE,
    CONSTRAINT comment_platform_external_id_key UNIQUE (platform, external_id),
    CONSTRAINT comment_text_not_blank CHECK (length(btrim(text)) > 0),
    CONSTRAINT comment_parent_no_cycle CHECK (parent_id <> id)
);

CREATE INDEX comment_post_id_idx ON comment (post_id);
CREATE INDEX comment_platform_external_id_idx ON comment (platform, external_id);
CREATE INDEX comment_parent_idx ON comment (parent_id);

-- ---------------------------------------------------------------------------
-- target — the join point between scrape and action. One row per
-- URL/external_id; a scrape reads it, an action writes against it.
-- ---------------------------------------------------------------------------
CREATE TABLE target (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    kind        target_kind NOT NULL,
    platform    platform    NOT NULL,
    external_id text        NOT NULL,
    url         text        NOT NULL,
    meta        jsonb       NOT NULL DEFAULT '{}',
    post_id     uuid        REFERENCES post (id) ON DELETE SET NULL,
    comment_id  uuid        REFERENCES comment (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT target_platform_external_id_key UNIQUE (platform, external_id),
    CONSTRAINT target_url_not_blank CHECK (length(btrim(url)) > 0),
    -- a target points at a post XOR a comment once resolved. The unresolved
    -- state (both NULL, a scrape that has not run yet) is legal; what is not, is
    -- pointing at both. Scrape failure is reported on the job, not here.
    CONSTRAINT target_at_most_one_ref CHECK (
        NOT (post_id IS NOT NULL AND comment_id IS NOT NULL)
    )
);

CREATE INDEX target_platform_external_id_idx ON target (platform, external_id);
CREATE INDEX target_kind_idx ON target (kind);

-- ---------------------------------------------------------------------------
-- scrape_job — one unit of scrape work. FIFO per account is enforced by the
-- scheduler through (account_id, status, scheduled_at), not by a constraint.
-- ---------------------------------------------------------------------------
CREATE TABLE scrape_job (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    type         job_type    NOT NULL,
    target_id    uuid        NOT NULL REFERENCES target (id) ON DELETE CASCADE,
    account_id   uuid        REFERENCES account (id) ON DELETE SET NULL,
    worker_id    uuid        REFERENCES worker (id) ON DELETE SET NULL,
    status       job_status  NOT NULL DEFAULT 'PENDING',
    scheduled_at timestamptz NOT NULL,
    started_at   timestamptz,
    finished_at  timestamptz,
    attempts     int         NOT NULL DEFAULT 0,
    error        text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT scrape_job_attempts_nonneg CHECK (attempts >= 0)
);

CREATE INDEX scrape_job_status_scheduled_idx ON scrape_job (status, scheduled_at);
CREATE INDEX scrape_job_worker_status_idx ON scrape_job (worker_id, status);
CREATE INDEX scrape_job_account_status_scheduled_idx
    ON scrape_job (account_id, status, scheduled_at);

-- ---------------------------------------------------------------------------
-- apify_run — one Apify actor execution (P2-03). 1:1 with a scrape_job.
-- ---------------------------------------------------------------------------
CREATE TABLE apify_run (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    scrape_job_id uuid        NOT NULL UNIQUE REFERENCES scrape_job (id) ON DELETE CASCADE,
    actor_id      text        NOT NULL,
    run_id        text        NOT NULL,
    status        text        NOT NULL,
    started_at    timestamptz NOT NULL DEFAULT now(),
    finished_at   timestamptz,
    CONSTRAINT apify_run_status_not_blank CHECK (length(btrim(status)) > 0)
);

CREATE INDEX apify_run_status_idx ON apify_run (status);

-- ---------------------------------------------------------------------------
-- raw_payload — pointer to the raw Apify output stored in MinIO (S3 key only;
-- the payload itself never lands in the DB, see ERD notes).
-- ---------------------------------------------------------------------------
CREATE TABLE raw_payload (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    apify_run_id uuid        NOT NULL REFERENCES apify_run (id) ON DELETE CASCADE,
    s3_key       text        NOT NULL,
    bytes        bigint      NOT NULL DEFAULT 0,
    received_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT raw_payload_bytes_nonneg CHECK (bytes >= 0),
    CONSTRAINT raw_payload_s3_key_not_blank CHECK (length(btrim(s3_key)) > 0)
);

CREATE INDEX raw_payload_apify_run_idx ON raw_payload (apify_run_id);

-- ---------------------------------------------------------------------------
-- metric_snapshot — time-series of post metrics. Timescale hypertable.
-- `post.metrics` holds the latest value; this table holds the history.
-- PK includes `ts` because Timescale requires the partition column in every
-- unique index. Nothing FKs into this table, so (id, ts) costs nothing.
-- ---------------------------------------------------------------------------
CREATE TABLE metric_snapshot (
    id          uuid        NOT NULL DEFAULT gen_random_uuid(),
    post_id     uuid        NOT NULL REFERENCES post (id) ON DELETE CASCADE,
    ts          timestamptz NOT NULL DEFAULT now(),
    views       bigint      NOT NULL DEFAULT 0,
    likes       bigint      NOT NULL DEFAULT 0,
    comments    bigint      NOT NULL DEFAULT 0,
    shares      bigint      NOT NULL DEFAULT 0,
    reach       bigint,
    reels_views bigint,
    CONSTRAINT metric_snapshot_pkey PRIMARY KEY (id, ts),
    CONSTRAINT metric_snapshot_counts_nonneg CHECK (
        views >= 0 AND likes >= 0 AND comments >= 0 AND shares >= 0 AND
        (reach IS NULL OR reach >= 0) AND (reels_views IS NULL OR reels_views >= 0)
    )
);

SELECT create_hypertable(
    'metric_snapshot', 'ts',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists       => TRUE
);

CREATE INDEX metric_snapshot_post_ts_idx ON metric_snapshot (post_id, ts DESC);

-- Keep 90 days of per-post metric history. Retention drops whole chunks, so it
-- is cheap; the dashboard never renders beyond 90 days anyway.
SELECT add_retention_policy(
    'metric_snapshot',
    drop_after    => INTERVAL '90 days',
    if_not_exists => TRUE
);

-- Compress old chunks so the hypertable stays cheap to scan.
ALTER TABLE metric_snapshot SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'post_id'
);
SELECT add_compression_policy(
    'metric_snapshot',
    compress_after => INTERVAL '7 days',
    if_not_exists  => TRUE
);

-- ---------------------------------------------------------------------------
-- official_account — a monitored brand/client account (read-only). NOT an
-- `account` (executor): no credentials, no login, no actions.
-- ---------------------------------------------------------------------------
CREATE TABLE official_account (
    id              uuid                    PRIMARY KEY DEFAULT gen_random_uuid(),
    platform        platform                NOT NULL,
    handle          text                    NOT NULL,
    display_name    text,
    profile_url     text,
    avatar_url      text,
    status          official_account_status NOT NULL DEFAULT 'ACTIVE',
    provider        analytics_provider      NOT NULL DEFAULT 'THIRDPARTY_A',
    provider_ref    text,
    tags            text[]                  NOT NULL DEFAULT '{}',
    last_fetched_at timestamptz,
    created_at      timestamptz             NOT NULL DEFAULT now(),
    CONSTRAINT official_account_platform_handle_key UNIQUE (platform, handle),
    CONSTRAINT official_account_handle_not_blank CHECK (length(btrim(handle)) > 0)
);

CREATE INDEX official_account_status_platform_idx
    ON official_account (status, platform);
CREATE INDEX official_account_tags_idx ON official_account USING gin (tags);

-- ---------------------------------------------------------------------------
-- analytics_snapshot — time-series of official-account metrics. Hypertable.
-- `metrics` JSONB carries platform-specific values + the raw provider payload;
-- the scalar columns are the cross-platform ones that get indexed/queried.
-- PK includes `ts` (Timescale partition-column requirement, same as above).
-- ---------------------------------------------------------------------------
CREATE TABLE analytics_snapshot (
    id                  uuid              NOT NULL DEFAULT gen_random_uuid(),
    official_account_id uuid              NOT NULL REFERENCES official_account (id) ON DELETE CASCADE,
    platform            platform          NOT NULL,
    ts                  timestamptz       NOT NULL DEFAULT now(),
    followers           bigint,
    reach               bigint,
    views               bigint,  -- reels / video / live views
    mentions            bigint,
    engagements         bigint,
    profile_views       bigint,
    metrics             jsonb             NOT NULL DEFAULT '{}',
    provider            analytics_provider NOT NULL,
    provider_run_id     text,
    fetched_at          timestamptz       NOT NULL DEFAULT now(),
    CONSTRAINT analytics_snapshot_pkey PRIMARY KEY (id, ts),
    CONSTRAINT analytics_snapshot_counts_nonneg CHECK (
        (followers IS NULL OR followers >= 0) AND
        (reach IS NULL OR reach >= 0) AND
        (views IS NULL OR views >= 0) AND
        (mentions IS NULL OR mentions >= 0) AND
        (engagements IS NULL OR engagements >= 0) AND
        (profile_views IS NULL OR profile_views >= 0)
    )
);

-- Idempotent ingest: the same (account, ts, provider) refreshes in place rather
-- than inserting a duplicate when a run is retried. Includes `ts` because
-- Timescale requires it in every unique index.
CREATE UNIQUE INDEX analytics_snapshot_account_ts_provider_idx
    ON analytics_snapshot (official_account_id, ts, provider);

SELECT create_hypertable(
    'analytics_snapshot', 'ts',
    chunk_time_interval => INTERVAL '7 days',
    if_not_exists       => TRUE
);

CREATE INDEX analytics_snapshot_platform_ts_idx
    ON analytics_snapshot (platform, ts DESC);

SELECT add_retention_policy(
    'analytics_snapshot',
    drop_after    => INTERVAL '365 days',
    if_not_exists => TRUE
);

ALTER TABLE analytics_snapshot SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'official_account_id'
);
SELECT add_compression_policy(
    'analytics_snapshot',
    compress_after => INTERVAL '30 days',
    if_not_exists  => TRUE
);

-- ---------------------------------------------------------------------------
-- analytics_mention — a mention of an official account, sourced from the
-- provider. Deduped by (platform, external_id).
-- ---------------------------------------------------------------------------
CREATE TABLE analytics_mention (
    id                  uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    official_account_id uuid        NOT NULL REFERENCES official_account (id) ON DELETE CASCADE,
    platform            platform    NOT NULL,
    external_id         text        NOT NULL,
    author_handle       text,
    text                text        NOT NULL,
    url                 text        NOT NULL,
    posted_at           timestamptz NOT NULL,
    sentiment           text,
    fetched_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT analytics_mention_platform_external_id_key UNIQUE (platform, external_id),
    CONSTRAINT analytics_mention_text_not_blank CHECK (length(btrim(text)) > 0),
    CONSTRAINT analytics_mention_sentiment CHECK (
        sentiment IS NULL OR sentiment IN ('positive', 'neutral', 'negative')
    )
);

CREATE INDEX analytics_mention_account_posted_idx
    ON analytics_mention (official_account_id, posted_at DESC);

-- ---------------------------------------------------------------------------
-- analytics_ingest_run — one attempt to pull metrics from a provider (audit +
-- observability for the analytics path; mirrors provision_log for workers).
-- ---------------------------------------------------------------------------
CREATE TABLE analytics_ingest_run (
    id           uuid                PRIMARY KEY DEFAULT gen_random_uuid(),
    provider     analytics_provider  NOT NULL,
    scope        text                NOT NULL,  -- 'platform:INSTAGRAM' | 'account:<id>' | 'all'
    status       ingest_status       NOT NULL DEFAULT 'PENDING',
    started_at   timestamptz         NOT NULL DEFAULT now(),
    finished_at  timestamptz,
    accounts_ok  int                 NOT NULL DEFAULT 0,
    accounts_err int                 NOT NULL DEFAULT 0,
    error_class  text,
    error        text,
    CONSTRAINT analytics_ingest_run_counts_nonneg CHECK (accounts_ok >= 0 AND accounts_err >= 0)
);

CREATE INDEX analytics_ingest_run_provider_started_idx
    ON analytics_ingest_run (provider, started_at DESC);
CREATE INDEX analytics_ingest_run_status_started_idx
    ON analytics_ingest_run (status, started_at DESC);
