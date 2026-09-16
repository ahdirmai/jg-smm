-- 000009_action_types_constraint — allow the new types in action_job.
--
-- The values were added to the job_type enum in 000008; this migration is the
-- other half of the same change and exists only because Postgres will not let
-- a single transaction both add an enum value and reference it.

ALTER TABLE action_job DROP CONSTRAINT IF EXISTS action_job_type_is_action;

ALTER TABLE action_job
    ADD CONSTRAINT action_job_type_is_action CHECK (
        type IN ('ACTION_LIKE', 'ACTION_COMMENT', 'ACTION_REPORT', 'ACTION_REPLY_COMMENT')
    );
