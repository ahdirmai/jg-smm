-- 000003 — down: drop P1 account lifecycle objects (reverse dependency order).

DROP TABLE IF EXISTS provision_log;
DROP TABLE IF EXISTS heartbeat;
DROP TABLE IF EXISTS account;
DROP TABLE IF EXISTS worker;
DROP TABLE IF EXISTS proxy_group;

DROP TYPE IF EXISTS provision_op;
DROP TYPE IF EXISTS worker_source;
DROP TYPE IF EXISTS desired_state;
DROP TYPE IF EXISTS worker_status;
DROP TYPE IF EXISTS account_status;
DROP TYPE IF EXISTS auth_status;
DROP TYPE IF EXISTS platform;
