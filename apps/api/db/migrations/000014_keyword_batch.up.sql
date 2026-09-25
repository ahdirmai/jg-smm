-- 000014 — keyword batch (background scrape) vs single post scrape.
--
-- Post scrape (POST /api/scrape/target) stays sync: 1 URL → 1 post.
-- Keyword scrape (POST /api/scrape/keywords) becomes async batch: 1..5 keywords
-- + window → many posts. The batch row is returned 202 immediately; a goroutine
-- runs Apify + ingest then fills counters.
--
-- A batch owns the window/keywords and the result via keyword_batch_post join,
-- so the batch dashboard (GET /batches/:id/posts) is distinct from "recent posts".

CREATE TABLE keyword_batch (
    id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    platform       platform    NOT NULL,
    keywords       text[]      NOT NULL,
    window_from    timestamptz,
    window_to      timestamptz,
    max_posts      int         NOT NULL,
    actor_id       text        NOT NULL,
    status         text        NOT NULL DEFAULT 'PENDING',
    apify_run_id   uuid        REFERENCES apify_run (id) ON DELETE SET NULL,
    items_read     int         NOT NULL DEFAULT 0,
    posts_count    int         NOT NULL DEFAULT 0,
    comments_count int         NOT NULL DEFAULT 0,
    error          text,
    created_by     uuid        REFERENCES app_user (id) ON DELETE SET NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    finished_at    timestamptz,
    CONSTRAINT keyword_batch_keywords_len CHECK (array_length(keywords, 1) BETWEEN 1 AND 5),
    CONSTRAINT keyword_batch_max_posts CHECK (max_posts >= 1 AND max_posts <= 200),
    CONSTRAINT keyword_batch_status CHECK (status IN ('PENDING','RUNNING','SUCCEEDED','FAILED')),
    CONSTRAINT keyword_batch_counts_nonneg CHECK (items_read >= 0 AND posts_count >= 0 AND comments_count >= 0),
    CONSTRAINT keyword_batch_actor_not_blank CHECK (length(btrim(actor_id)) > 0)
);

CREATE INDEX keyword_batch_created_at_idx ON keyword_batch (created_at DESC);
CREATE INDEX keyword_batch_status_idx ON keyword_batch (status);

-- Join: which posts were ingested as part of a batch. Populated after ingest
-- from the fresh posts read-back; powers GET /batches/:id/posts.
CREATE TABLE keyword_batch_post (
    keyword_batch_id uuid NOT NULL REFERENCES keyword_batch (id) ON DELETE CASCADE,
    post_id          uuid NOT NULL REFERENCES post (id) ON DELETE CASCADE,
    PRIMARY KEY (keyword_batch_id, post_id)
);

CREATE INDEX keyword_batch_post_post_idx ON keyword_batch_post (post_id);
