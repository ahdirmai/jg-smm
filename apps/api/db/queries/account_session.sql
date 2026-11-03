-- Account session queries. storage_state is AES-256-GCM ciphertext and shares
-- password_enc's rule: it is read ONLY by the control-message dispatch that
-- hands it back to a worker, never by a read model, an API response, or a log
-- line. GetAccountSessionState is the single decrypting read, exactly as
-- GetAccountPasswordEnc is for the password.

-- name: GetAccountSession :one
-- Metadata only — no ciphertext. Safe for scheduling and diagnostics.
SELECT account_id, expires_at, captured_on, captured_at, last_used_at
FROM account_session
WHERE account_id = $1;

-- name: GetAccountSessionState :one
-- The narrow credential read: the auth-import dispatch unseals this once and
-- puts it on the private per-worker control channel.
SELECT storage_state, expires_at
FROM account_session
WHERE account_id = $1;

-- name: UpsertAccountSession :one
-- A fresh login replaces whatever was stored: the new cookie is strictly
-- better, and keeping the old one would let a revoked session shadow a live
-- one. captured_on records which container produced it, so a bad cookie can be
-- traced to a worker.
INSERT INTO account_session (account_id, storage_state, expires_at, captured_on, captured_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (account_id) DO UPDATE SET
    storage_state = EXCLUDED.storage_state,
    expires_at    = EXCLUDED.expires_at,
    captured_on   = EXCLUDED.captured_on,
    captured_at   = now(),
    last_used_at  = NULL
RETURNING account_id, expires_at, captured_on, captured_at, last_used_at;

-- name: TouchAccountSession :exec
-- Records that a worker hydrated from this session successfully. Distinguishes
-- a cookie that has actually been reused from one that was captured and never
-- tried.
UPDATE account_session SET last_used_at = now() WHERE account_id = $1;

-- name: DeleteAccountSession :exec
DELETE FROM account_session WHERE account_id = $1;

-- name: ListSessionsExpiringBefore :many
-- Refresh sweep: sessions whose dated cookie dies before the cutoff. Undated
-- (session-cookie) rows are excluded by the index predicate and by the NOT
-- NULL check, because a date we do not have cannot be scheduled against.
SELECT account_id, expires_at
FROM account_session
WHERE expires_at IS NOT NULL AND expires_at < $1
ORDER BY expires_at ASC
LIMIT $2;
