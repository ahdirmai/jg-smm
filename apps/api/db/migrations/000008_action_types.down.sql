-- Reverse 000008: remove the report/reply enum values.
--
-- Rows typed ACTION_REPORT/ACTION_REPLY_COMMENT are deleted first — Postgres
-- will not drop an enum value that is in use. This is the rollback path only;
-- it never runs as part of normal operation.
DELETE FROM action_job WHERE type IN ('ACTION_REPORT', 'ACTION_REPLY_COMMENT');

ALTER TYPE job_type REMOVE VALUE IF EXISTS 'ACTION_REPLY_COMMENT';
ALTER TYPE job_type REMOVE VALUE IF EXISTS 'ACTION_REPORT';
