-- 000025 — retire the legacy container fleet without losing its history.
--
-- 000024 turned `worker` from "the container an account lives in" into "a slot
-- the pool leases". Production still carries 511 rows from the old model (476
-- AUTO + 35 MANUAL, all PAUSED, none reachable), and the dashboard shows every
-- one of them as if it were a live worker.
--
-- Deleting those rows is the right cleanup, but `heartbeat` and `provision_log`
-- reference workers with ON DELETE CASCADE: a plain DELETE would take 70k
-- telemetry samples and 1.6k provisioner audit rows with it. Those are HISTORY
-- — "this worker was busy at 14:03" stays true after the worker is gone — so
-- they switch to SET NULL, matching what account/action_log already do.
--
-- The columns are nullable today, so this is a rule change, not a data change.

ALTER TABLE heartbeat ALTER COLUMN worker_id DROP NOT NULL;
ALTER TABLE provision_log ALTER COLUMN worker_id DROP NOT NULL;

ALTER TABLE heartbeat
    DROP CONSTRAINT IF EXISTS heartbeat_worker_id_fkey;
ALTER TABLE heartbeat
    ADD CONSTRAINT heartbeat_worker_id_fkey
    FOREIGN KEY (worker_id) REFERENCES worker (id) ON DELETE SET NULL;

ALTER TABLE provision_log
    DROP CONSTRAINT IF EXISTS provision_log_worker_id_fkey;
ALTER TABLE provision_log
    ADD CONSTRAINT provision_log_worker_id_fkey
    FOREIGN KEY (worker_id) REFERENCES worker (id) ON DELETE SET NULL;
