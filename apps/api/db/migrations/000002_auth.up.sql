-- 000002_auth — refresh-token session store (P0-06, PRD F1.3).
--
-- Access tokens are stateless JWTs (24h) held in an HttpOnly cookie; refresh
-- tokens are opaque random strings (30d) held in this table so they can be
-- revoked server-side. Only the SHA-256 hash of the token is stored.

CREATE TYPE auth_session_revoked_reason AS ENUM ('logout', 'rotated', 'reuse');

CREATE TABLE auth_session (
    id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        uuid        NOT NULL REFERENCES app_user (id) ON DELETE CASCADE,
    token_hash     bytea       NOT NULL,
    user_agent     text,
    ip             inet,
    expires_at     timestamptz NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    last_used_at   timestamptz,
    revoked_at     timestamptz,
    revoked_reason auth_session_revoked_reason
);

CREATE UNIQUE INDEX auth_session_token_hash_key ON auth_session (token_hash);
CREATE INDEX auth_session_user_id_idx ON auth_session (user_id);
CREATE INDEX auth_session_expires_at_idx ON auth_session (expires_at)
    WHERE revoked_at IS NULL;
