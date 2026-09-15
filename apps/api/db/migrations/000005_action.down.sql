-- 000005 down — reverse order of the up migration.

DROP TABLE IF EXISTS action_log;
DROP TABLE IF EXISTS action_job;

DROP TYPE IF EXISTS attempt_status;
