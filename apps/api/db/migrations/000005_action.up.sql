-- 000005 — P3: action engine.
-- Scope: ActionJob (one unit of action work) + ActionLog (one attempt's verdict).
--
-- Invariants (DEVELOPMENT_RULE.md §DB, ERD.md):
--   * ActionJob mirrors ScrapeJob's shape on purpose: both ride the same
--     queue-side JobType/JobStatus enums and the same per-account FIFO shape,
--     so a worker callback and the dashboard treat them uniformly.
--   * ActionLog is the source of truth for a verdict. ActionJob.status is only
--     a projection of the latest attempt — nothing is ever inferred from the
--     absence of a log row (ERD: "tidak ada SUCCESS tanpa verifikasi").
--   * UNIQUE (action_job_id, attempt) is the upsert heart: a worker's RUNNING
--     callback and its terminal callback write the SAME row, so an attempt is
--     never duplicated and a retry is always a new attempt number.
--   * screenshot_url is written with COALESCE on the upsert path so a RUNNING
--     callback that carries no screenshot never clobbers the shot an earlier
--     attempt captured.
--
-- Not in this migration: comment_template + action_job.template_id (P3-02).
-- The template FK is added there; the column is deliberately absent here so
-- this migration stands on its own and P3-02 is a schema- additive ALTER.

-- ---------------------------------------------------------------------------
-- enums
-- ---------------------------------------------------------------------------
-- attempt_status is the verdict of ONE attempt (domain.AttemptStatus), kept
-- separate from job_status: a job RETRYs, an attempt FAILS.
CREATE TYPE attempt_status AS ENUM (
    'RUNNING', 'SUCCESS', 'FAILED', 'RETRY', 'CANCELLED'
);

-- ---------------------------------------------------------------------------
-- action_job — one like/comment to execute on one target by one account.
-- Queue order is (account_id, status, scheduled_at), same as scrape_job: the
-- scheduler claims FIFO per account, never globally.
-- ---------------------------------------------------------------------------
CREATE TABLE action_job (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    type         job_type    NOT NULL,
    target_id    uuid        NOT NULL REFERENCES target (id) ON DELETE CASCADE,
    account_id   uuid        NOT NULL REFERENCES account (id) ON DELETE CASCADE,
    worker_id    uuid        REFERENCES worker (id) ON DELETE SET NULL,
    status       job_status  NOT NULL DEFAULT 'PENDING',
    scheduled_at timestamptz NOT NULL,
    started_at   timestamptz,
    finished_at  timestamptz,
    attempts     int         NOT NULL DEFAULT 0,
    error        text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT action_job_type_is_action CHECK (
        type IN ('ACTION_LIKE', 'ACTION_COMMENT')
    ),
    CONSTRAINT action_job_attempts_nonneg CHECK (attempts >= 0)
);

CREATE INDEX action_job_status_scheduled_idx ON action_job (status, scheduled_at);
CREATE INDEX action_job_account_status_scheduled_idx
    ON action_job (account_id, status, scheduled_at);
CREATE INDEX action_job_worker_status_idx ON action_job (worker_id, status);
CREATE INDEX action_job_target_idx ON action_job (target_id);

-- ---------------------------------------------------------------------------
-- action_log — one attempt of one action job. The audit + debug trail: input
-- (rendered text), output (response excerpt), error class, screenshot.
-- ---------------------------------------------------------------------------
CREATE TABLE action_log (
    id               uuid           PRIMARY KEY DEFAULT gen_random_uuid(),
    action_job_id    uuid           NOT NULL REFERENCES action_job (id) ON DELETE CASCADE,
    attempt          int            NOT NULL,
    status           attempt_status NOT NULL DEFAULT 'RUNNING',
    verified         boolean        NOT NULL DEFAULT FALSE,
    worker_id        uuid           REFERENCES worker (id) ON DELETE SET NULL,
    rendered_text    text           NOT NULL,
    response_excerpt text,
    error_class      text,
    screenshot_url   text,
    duration_ms      int            NOT NULL DEFAULT 0,
    ts               timestamptz    NOT NULL DEFAULT now(),
    CONSTRAINT action_log_job_attempt_key UNIQUE (action_job_id, attempt),
    CONSTRAINT action_log_attempt_pos     CHECK (attempt > 0),
    CONSTRAINT action_log_duration_nonneg CHECK (duration_ms >= 0),
    -- TRANSIENT|AUTH|RATE_LIMIT|BANNED|UNKNOWN (domain classification). Plain
    -- text + CHECK, not an enum: the classifier is a string switch and adding
    -- a class must not require a migration (see analytics_ingest_run).
    CONSTRAINT action_log_error_class_domain CHECK (
        error_class IS NULL OR error_class IN (
            'TRANSIENT', 'AUTH', 'RATE_LIMIT', 'BANNED', 'UNKNOWN'
        )
    )
);

CREATE INDEX action_log_job_idx ON action_log (action_job_id, attempt);
CREATE INDEX action_log_worker_ts_idx ON action_log (worker_id, ts DESC);
CREATE INDEX action_log_error_class_idx ON action_log (error_class);
