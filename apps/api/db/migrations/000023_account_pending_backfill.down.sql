-- 000023 — restore the old default and the old index predicate.
--
-- The backfill is NOT reversed: AUTHENTICATING was wrong for these rows (no
-- login was ever attempted), and returning them to it would put them back in
-- the unreachable state this migration exists to fix. They become FAILED
-- instead, which the due query still selects — so a rollback keeps them
-- schedulable rather than stranding them again.
ALTER TABLE account ALTER COLUMN auth_status SET DEFAULT 'AUTHENTICATING';

UPDATE account SET auth_status = 'FAILED' WHERE auth_status = 'PENDING';

DROP INDEX IF EXISTS account_refresh_due_idx;

CREATE INDEX account_refresh_due_idx
    ON account (cookie_expiry_at)
    WHERE auth_status IN ('AUTHENTICATED', 'FAILED')
      AND status = 'ACTIVE'
      AND worker_id IS NOT NULL;
