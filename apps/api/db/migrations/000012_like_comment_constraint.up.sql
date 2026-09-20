-- 000012_like_comment_constraint — allow ACTION_LIKE_COMMENT in action_job.
--
-- The value was added to the job_type enum in 000011; this is the other half
-- of the same change, split only because Postgres will not let one transaction
-- both add an enum value and reference it.

ALTER TABLE action_job DROP CONSTRAINT IF EXISTS action_job_type_is_action;

ALTER TABLE action_job
    ADD CONSTRAINT action_job_type_is_action CHECK (
        type IN ('ACTION_LIKE', 'ACTION_COMMENT', 'ACTION_REPORT', 'ACTION_REPLY_COMMENT', 'ACTION_LIKE_COMMENT')
    );
