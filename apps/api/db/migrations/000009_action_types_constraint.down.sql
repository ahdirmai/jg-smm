-- Reverse 000009: forbid the report/reply types again.
ALTER TABLE action_job DROP CONSTRAINT IF EXISTS action_job_type_is_action;

ALTER TABLE action_job
    ADD CONSTRAINT action_job_type_is_action CHECK (
        type IN ('ACTION_LIKE', 'ACTION_COMMENT')
    );
