-- 000006 — P3-02: comment template engine.
-- Scope: comment_template (the pool) + the template_id FK on action_job and
-- action_log (the link from an executed comment back to the variant it used).
--
-- Invariants (ERD.md CommentTemplate, DEVELOPMENT_RULE.md §DB):
--   * A template is platform-scoped: IG and Threads have different tone and
--     length limits, so a pool is per platform and the pick query filters on it.
--   * vars is the DECLARED variable set (["topic","product"]) the text may
--     reference as {topic}. It is stored, not parsed out of the text, so the
--     composer can validate "every placeholder has a value" without a regex
--     over user content.
--   * weight drives the random pick; banned_words is the per-template denylist
--     screened by P3-03 before a rendered comment is ever enqueued.
--   * is_active is a soft switch: a template flagged by review is excluded from
--     the pool without deleting the rows that explain past action_logs.
--
-- Dedupe (7 days per target) is a QUERY, not a constraint: it is "which
-- templates have not been used against THIS target recently", and a CHECK
-- constraint cannot express a temporal join. See template.sql PickForTarget.

CREATE TABLE comment_template (
    id           uuid       PRIMARY KEY DEFAULT gen_random_uuid(),
    platform     platform   NOT NULL,
    text         text       NOT NULL,
    vars         text[]     NOT NULL DEFAULT '{}',
    weight       int        NOT NULL DEFAULT 1,
    banned_words text[]     NOT NULL DEFAULT '{}',
    is_active    boolean    NOT NULL DEFAULT TRUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT comment_template_weight_pos CHECK (weight > 0),
    -- A template with no body cannot render a comment; the pool would offer an
    -- empty string to a worker that must post something.
    CONSTRAINT comment_template_text_not_blank CHECK (length(btrim(text)) > 0)
);

CREATE INDEX comment_template_platform_active_idx
    ON comment_template (platform, is_active);

-- The FK lands here, not in 000005, so that migration stands alone and this one
-- is purely additive: an action_job may be created without a template (likes,
-- and comments enqueued before the pool existed), and the column stays nullable.
ALTER TABLE action_job
    ADD COLUMN template_id uuid REFERENCES comment_template (id) ON DELETE SET NULL;
CREATE INDEX action_job_template_idx ON action_job (template_id);

ALTER TABLE action_log
    ADD COLUMN template_id uuid REFERENCES comment_template (id) ON DELETE SET NULL;
CREATE INDEX action_log_template_idx ON action_log (template_id);
