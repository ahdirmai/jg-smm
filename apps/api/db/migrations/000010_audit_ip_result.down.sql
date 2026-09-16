DROP INDEX IF EXISTS audit_log_entity_ts_idx;
ALTER TABLE audit_log DROP COLUMN IF EXISTS result;
ALTER TABLE audit_log DROP COLUMN IF EXISTS ip;
