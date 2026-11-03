-- 000020 — account_session. Reversible: dropping the table loses the stored
-- sessions, so every account falls back to needing a fresh login on its next
-- container swap. That is the pre-000020 behaviour, not data loss of anything
-- the account cannot re-obtain.
DROP TABLE IF EXISTS account_session;
