-- 000023 — give a never-attempted account a state the scheduler can actually
-- reach.
--
-- PART 1: the data fix. See 000022 for why AUTHENTICATING + NULL
-- last_checked_at made a row unreachable by the due query, the stale-login
-- sweeper and the in-flight gate at once.
--
-- The predicate is deliberately narrow. A real in-flight login always carries
-- last_checked_at: BeginSessionRefresh stamps it on the way IN, precisely so a
-- dispatch whose worker never reports back still ages out. NULL therefore means
-- "never dispatched", not "dispatched and abandoned" — the latter has a
-- timestamp and belongs to ListStaleLoginAttempts, which is left untouched.
--
-- Rows reclaimed by that sweeper are FAILED with a timestamp and match nothing
-- here, so this migration cannot overwrite a real outcome.

UPDATE account
SET auth_status = 'PENDING'
WHERE auth_status = 'AUTHENTICATING'
  AND last_checked_at IS NULL;

-- PART 2: the default that caused it. Every import and seed inherited
-- AUTHENTICATING, so new accounts were born in the unreachable state.
ALTER TABLE account ALTER COLUMN auth_status SET DEFAULT 'PENDING';

-- PART 3: the due index must cover a first login. The 000015 index is
-- predicated on `cookie_expiry_at` being inside the lead window and on
-- auth_status IN ('AUTHENTICATED','FAILED'), so neither PENDING rows nor
-- NULL-expiry rows are in it. Creating a wider index without dropping the old
-- one would leave the planner two partial indexes and no reason to prefer
-- either.
DROP INDEX IF EXISTS account_refresh_due_idx;

CREATE INDEX account_refresh_due_idx
    ON account (cookie_expiry_at)
    WHERE (
        (auth_status = 'PENDING' AND cookie_expiry_at IS NULL)
        OR (
          cookie_expiry_at IS NOT NULL
          AND auth_status IN ('AUTHENTICATED', 'FAILED')
        )
      )
      AND status = 'ACTIVE'
      AND worker_id IS NOT NULL;
