-- Rollback: restore the cascading rules the pre-pool model relied on.
--
-- Re-adding ON DELETE CASCADE is only safe while nothing references a deleted
-- worker; rows orphaned by 000025 (worker_id NULL) simply stay orphaned, which
-- is why this is data-preserving rather than data-restoring.

-- Orphans must go before NOT NULL can return; otherwise the ALTER aborts on
-- any row whose worker was deleted while the relaxed rule was in force.
DELETE FROM heartbeat WHERE worker_id IS NULL;
DELETE FROM provision_log WHERE worker_id IS NULL;

ALTER TABLE heartbeat ALTER COLUMN worker_id SET NOT NULL;
ALTER TABLE provision_log ALTER COLUMN worker_id SET NOT NULL;

ALTER TABLE heartbeat
    DROP CONSTRAINT IF EXISTS heartbeat_worker_id_fkey;
ALTER TABLE heartbeat
    ADD CONSTRAINT heartbeat_worker_id_fkey
    FOREIGN KEY (worker_id) REFERENCES worker (id) ON DELETE CASCADE;

ALTER TABLE provision_log
    DROP CONSTRAINT IF EXISTS provision_log_worker_id_fkey;
ALTER TABLE provision_log
    ADD CONSTRAINT provision_log_worker_id_fkey
    FOREIGN KEY (worker_id) REFERENCES worker (id) ON DELETE CASCADE;
