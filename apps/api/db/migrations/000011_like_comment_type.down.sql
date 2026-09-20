-- Reverse 000011: remove the ACTION_LIKE_COMMENT enum value.
--
-- Postgres has ADD VALUE but no REMOVE VALUE, so dropping an enum member means
-- rebuilding the type. This lands the 8-value type that existed after 000008
-- (the pre-000011 schema). Order matters: constraint → columns → type. Mirrors
-- the 000008 down rebuild; see it for the detailed rationale.
--
-- Rollback path only; never runs in normal operation.

-- 0. Drop the CHECK on action_job (re-added at the end against the rebuilt type).
ALTER TABLE action_job DROP CONSTRAINT IF EXISTS action_job_type_is_action;

-- 1. Remove rows the rebuilt type cannot represent.
DELETE FROM action_job WHERE type = 'ACTION_LIKE_COMMENT';
DELETE FROM scrape_job WHERE type = 'ACTION_LIKE_COMMENT';

-- 2. Rebuild the type with the eight members present after 000008.
CREATE TYPE job_type_new AS ENUM (
    'SCRAPE_LIKE',
    'SCRAPE_COMMENT',
    'SCRAPE_METRIC',
    'SESSION_REFRESH',
    'ACTION_LIKE',
    'ACTION_COMMENT',
    'ACTION_REPORT',
    'ACTION_REPLY_COMMENT'
);

-- 3. Repoint both columns through the new type (plain text coercion).
ALTER TABLE action_job
    ALTER COLUMN type TYPE job_type_new
    USING type::text::job_type_new;

ALTER TABLE scrape_job
    ALTER COLUMN type TYPE job_type_new
    USING type::text::job_type_new;

-- 4. Swap the type and drop the old one.
DROP TYPE job_type;
ALTER TYPE job_type_new RENAME TO job_type;

-- 5. Restore the 4-value CHECK that 000009 left in place (000012 up widens it
--    again on the way back up).
ALTER TABLE action_job
    ADD CONSTRAINT action_job_type_is_action CHECK (
        type IN ('ACTION_LIKE', 'ACTION_COMMENT', 'ACTION_REPORT', 'ACTION_REPLY_COMMENT')
    );
