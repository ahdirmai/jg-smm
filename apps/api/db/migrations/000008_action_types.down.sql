-- Reverse 000008: remove the report/reply enum values.
--
-- Postgres has `ALTER TYPE ... ADD VALUE` but no `REMOVE VALUE`, so the only
-- way to drop enum members is to rebuild the type. This rebuild lands a
-- 6-value type matching the pre-000008 schema. Order matters: child → parent
-- (constraint, column, type).
--
-- Two tables carry the type: `action_job` (000005) and `scrape_job` (000004,
-- where the type is created). Both columns must be repointed before the type
-- can be dropped, or `DROP TYPE` fails on the remaining dependent.
--
-- The action_job CHECK must go first: its literals were coerced to the old
-- `job_type` at creation time, so while the column still carries it, PG resolves
-- the type swap as `job_type_new = job_type` and there is no such operator
-- (distinct enums are not implicitly comparable). Dropping it is safe because
-- this down re-adds the original version and 000009 up widens it again.
--
-- This is the rollback path only; it never runs in normal operation.

-- 0. Drop the 2-value CHECK on action_job (re-added in step 6).
ALTER TABLE action_job DROP CONSTRAINT IF EXISTS action_job_type_is_action;

-- 1. Rows typed ACTION_REPORT/ACTION_REPLY_COMMENT must go first — the new
--    type has no label for them, and the column casts below would otherwise
--    fail on a value they cannot represent. scrape_job only ever holds
--    SCRAPE_*/SESSION_REFRESH rows in practice, but its column permits the
--    ACTION_* labels, so sweep it too.
DELETE FROM action_job WHERE type IN ('ACTION_REPORT', 'ACTION_REPLY_COMMENT');
DELETE FROM scrape_job WHERE type IN ('ACTION_REPORT', 'ACTION_REPLY_COMMENT');

-- 2. Rebuild the type with the original six members.
CREATE TYPE job_type_new AS ENUM (
    'SCRAPE_LIKE',
    'SCRAPE_COMMENT',
    'SCRAPE_METRIC',
    'SESSION_REFRESH',
    'ACTION_LIKE',
    'ACTION_COMMENT'
);

-- 3. Repoint both columns through the new type. The USING cast is a plain
--    text coercion: every surviving label exists in both types.
ALTER TABLE action_job
    ALTER COLUMN type TYPE job_type_new
    USING type::text::job_type_new;

ALTER TABLE scrape_job
    ALTER COLUMN type TYPE job_type_new
    USING type::text::job_type_new;

-- 4. Swap the type and drop the old one. Now nothing depends on it.
DROP TYPE job_type;
ALTER TYPE job_type_new RENAME TO job_type;

-- 5. Restore the original 2-value CHECK from 000005, now against the rebuilt
--    type. 000009 up will widen it again on the way back up.
ALTER TABLE action_job
    ADD CONSTRAINT action_job_type_is_action CHECK (
        type IN ('ACTION_LIKE', 'ACTION_COMMENT')
    );
