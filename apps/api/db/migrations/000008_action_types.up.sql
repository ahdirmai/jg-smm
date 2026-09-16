-- 000008_action_types — report + reply-comment job types (enum only).
--
-- The queue started with like + comment only. The dashboard's action matrix
-- also offers "Report post" and "Reply comment", which need their own job
-- types so the worker can branch on them. Both ride the exact same pipeline
-- (enqueue → cooldown/rate gates → dispatch → verify) — the type is the only
-- thing that changes, so this is an enum extension, not a new table.
--
-- Enum values and the CHECK that allows them are split across 000008/000009
-- on purpose: a migration runs inside one transaction, and Postgres refuses
-- to *use* a value added by ALTER TYPE in that same transaction
-- ("unsafe use of new value"). Adding in 000008 and using in 000009 is the
-- standard workaround.

ALTER TYPE job_type ADD VALUE IF NOT EXISTS 'ACTION_REPORT';
ALTER TYPE job_type ADD VALUE IF NOT EXISTS 'ACTION_REPLY_COMMENT';
