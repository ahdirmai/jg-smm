-- 000017 — per-content (per-post) metrics for monitored official accounts.
--
-- `analytics_snapshot` answers "how is the account doing". It cannot answer
-- "which post did that", which is the question a social media team actually
-- acts on: a reach total that doubles tells you nothing about whether the
-- second post or the eleventh one earned it.
--
-- Deliberately NOT a hypertable, unlike its sibling `analytics_snapshot`.
-- Instagram's media insights are period `lifetime` — a running total for that
-- post, not a per-day increment — so the honest shape is one current row per
-- post that refreshes, not a series to append to. Making it a hypertable would
-- buy a partition column this data does not have and cost a `ts` in every
-- unique index, including the one that makes ingest idempotent.
--
-- Invariants:
--   * Deduped by (official_account_id, external_id, provider). Re-running an
--     ingest refreshes a post in place instead of inserting it twice; the
--     unique index is the enforcement, the upsert query is the ergonomics.
--   * `external_id` is the provider's own media id. The handle is not unique
--     across accounts, and a permalink is mutable; the media id is neither.
--   * Every metric column is nullable and means "the provider did not report
--     this", never "zero". Instagram omits a metric per media type — a
--     carousel has no watch time, a static image has no views — and Instagram
--     has deprecated `impressions` outright, so a hard NOT NULL DEFAULT 0 here
--     would manufacture zeros for metrics that do not exist.
CREATE TABLE analytics_content_metric (
    id                  uuid                PRIMARY KEY DEFAULT gen_random_uuid(),
    official_account_id uuid                NOT NULL REFERENCES official_account (id) ON DELETE CASCADE,
    platform            platform            NOT NULL,
    -- provider's media id, e.g. an Instagram media id.
    external_id         text                NOT NULL,
    -- provider's own label for the format: CAROUSEL_ALBUM, IMAGE, VIDEO, REELS.
    content_type        text,
    caption             text,
    permalink           text,
    published_at        timestamptz,
    reach               bigint,
    views               bigint,
    interactions        bigint,
    likes               bigint,
    comments            bigint,
    saves               bigint,
    shares              bigint,
    profile_views       bigint,
    -- platform-specific values and the raw provider payload, same contract as
    -- analytics_snapshot.metrics.
    metrics             jsonb               NOT NULL DEFAULT '{}',
    provider            analytics_provider  NOT NULL,
    fetched_at          timestamptz         NOT NULL DEFAULT now(),
    CONSTRAINT analytics_content_metric_account_external_provider_key
        UNIQUE (official_account_id, external_id, provider),
    CONSTRAINT analytics_content_metric_external_id_not_blank
        CHECK (length(btrim(external_id)) > 0),
    CONSTRAINT analytics_content_metric_counts_nonneg CHECK (
        (reach         IS NULL OR reach         >= 0) AND
        (views         IS NULL OR views         >= 0) AND
        (interactions  IS NULL OR interactions  >= 0) AND
        (likes         IS NULL OR likes         >= 0) AND
        (comments      IS NULL OR comments      >= 0) AND
        (saves         IS NULL OR saves         >= 0) AND
        (shares        IS NULL OR shares        >= 0) AND
        (profile_views IS NULL OR profile_views >= 0)
    )
);
-- The per-platform page sorts by reach/views with a date filter; this is the
-- index that makes it an index scan instead of a sort over the whole table.
CREATE INDEX analytics_content_metric_platform_published_idx
    ON analytics_content_metric (platform, published_at DESC);
CREATE INDEX analytics_content_metric_account_reach_idx
    ON analytics_content_metric (official_account_id, reach DESC NULLS LAST);
