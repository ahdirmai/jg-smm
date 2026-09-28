DROP INDEX IF EXISTS account_refresh_due_idx;

ALTER TABLE account DROP CONSTRAINT IF EXISTS account_refresh_failures_nonneg;

ALTER TABLE account DROP COLUMN IF EXISTS refresh_failures;