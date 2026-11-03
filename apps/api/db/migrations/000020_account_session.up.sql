-- 000020 — account_session: one encrypted browser session per account.
--
-- WHY THIS EXISTS
-- Until now a session lived only on the container that created it, at
-- /data/sessions/session-<platform>.json (see apps/worker/src/core/session.ts).
-- That path carries the PLATFORM, not the account, so one container could hold
-- exactly one Instagram session at a time. With 53 accounts and 5 containers
-- there was nowhere for the other 48 sessions to live, so every container swap
-- forced a fresh login — 53 logins per round, which is precisely the pattern
-- that trips Instagram's challenge walls.
--
-- This table gives each account its own session row, so a login is paid ONCE
-- and the cookie is re-imported into whichever container picks the account up
-- next. The worker still keeps its per-platform file; this is the durable copy
-- that survives the container.
--
-- SECURITY
-- storage_state is AES-256-GCM ciphertext under CREDENTIAL_KEY, the same sealer
-- that protects account.password_enc. It is NEVER selected into an API response,
-- never logged, and the only read path is the control-message dispatch that
-- hands it back to a worker (port.Sealer.Open). A session cookie is a bearer
-- credential: anyone holding it can act as the account without a password.
--
-- This deliberately reverses the earlier "the session is never persisted
-- server-side" policy in apps/api/internal/http/account.go. That policy was
-- written when one container owned one account forever; it does not survive a
-- rotating fleet, and the alternative (re-login on every swap) is strictly
-- worse for account safety. The owner approved the reversal explicitly.
CREATE TABLE account_session (
    account_id     uuid        PRIMARY KEY REFERENCES account (id) ON DELETE CASCADE,
    -- AES-256-GCM ciphertext of the worker's storageState() JSON.
    storage_state  bytea       NOT NULL,
    -- When the platform's auth cookie expires, when it told us. Drives refresh
    -- scheduling; NULL for a session cookie that carries no date.
    expires_at     timestamptz,
    -- Which container produced this session, for debugging a bad cookie.
    captured_on    uuid        REFERENCES worker (id) ON DELETE SET NULL,
    captured_at    timestamptz NOT NULL DEFAULT now(),
    -- Last time a worker successfully hydrated from it. NULL = never reused
    -- yet, which distinguishes "fresh login" from "reimported 40 times".
    last_used_at   timestamptz,
    CONSTRAINT account_session_state_not_empty CHECK (length(storage_state) > 0)
);

-- The refresher sweeps for sessions that are about to die, so it needs an
-- ordered read on expiry; partial so the undated (session-cookie) rows, which
-- can never be scheduled, stay out of it.
CREATE INDEX account_session_expiry_idx
    ON account_session (expires_at)
    WHERE expires_at IS NOT NULL;
