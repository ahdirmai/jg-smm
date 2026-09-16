-- Audit trail gains the columns the dashboard audit page needs (P6-10):
-- ip (where the actor acted from) and result (ok / the failure reason).
-- Both are nullable-tolerant: ip is NULL for system-initiated rows, result
-- defaults to 'ok' so backfill + system rows stay valid.

ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS ip inet;
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS result text NOT NULL DEFAULT 'ok';

-- Drop the old plain list index coverage; the filtered read path orders by ts
-- DESC anyway and the existing audit_log_ts_idx already covers it.
CREATE INDEX IF NOT EXISTS audit_log_entity_ts_idx ON audit_log (entity, entity_id, ts DESC);
