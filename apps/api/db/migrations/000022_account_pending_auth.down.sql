-- 000022 — PostgreSQL cannot remove a value from an enum type, and recreating
-- it would rewrite every column that uses it and fail if any row holds
-- 'PENDING'. The value is left in place, which is harmless: a binary that
-- predates it never writes it.

SELECT 1;
