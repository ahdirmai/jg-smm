-- 000015 — P2-06 session refresh.
--
-- `cookie_expiry_at` already exists (000003) but is in no query: never written,
-- never read. This ticket is what makes it load-bearing, so it needs the writer
-- and the reader to go with it.
--
-- `refresh_failures` is new. The AC is "gagal 3x → quarantined", and that needs
-- a count of CONSECUTIVE failures — a signal nothing in the schema held. It is
-- deliberately NOT `health_score` (the P5-02 model, which weighs failures by
-- class and is only reachable from the action-callback path) and not the job
-- queue (a refresh is dispatched over the control channel, not as a job).
-- Reset to 0 on a verified login: one success clears the streak, which is what
-- "consecutive" means.

ALTER TABLE account ADD COLUMN refresh_failures int NOT NULL DEFAULT 0;

ALTER TABLE account ADD CONSTRAINT account_refresh_failures_nonneg
    CHECK (refresh_failures >= 0);

-- The refresh scheduler's claim query: accounts whose cookie is due, that have
-- a container to run on, and that are still in the pool. Partial so the index
-- stays small — most accounts are healthy with no expiry recorded yet.
--
-- `auth_status` is `IN ('AUTHENTICATED', 'FAILED')`, not `= 'AUTHENTICATED'`:
-- a failed refresh sets FAILED, and those accounts must stay eligible or the
-- failure streak could never reach the AC's 3. The predicate has to match the
-- query exactly — Postgres only uses a partial index whose predicate the query
-- provably implies, and `= 'AUTHENTICATED'` does not imply a two-value IN.
CREATE INDEX account_refresh_due_idx
    ON account (cookie_expiry_at)
    WHERE auth_status IN ('AUTHENTICATED', 'FAILED')
      AND status = 'ACTIVE'
      AND worker_id IS NOT NULL;