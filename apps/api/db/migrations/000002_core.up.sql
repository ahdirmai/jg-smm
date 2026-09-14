-- P0-04 — core schema base (single-team).
-- Scope: TeamConfig (singleton), User (auth), AuditLog.
-- Everything else (ProxyGroup, Account, Worker, ...) lands in its owning phase.
--
-- Conventions (see DEVELOPMENT_RULE.md §DB):
--   * PK = uuid v4 generated in the DB (portable, no extension requirement).
--   * timestamptz everywhere; defaults come from now().
--   * snake_case identifiers, explicit constraints named by Postgres.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ---------------------------------------------------------------------------
-- enums
-- ---------------------------------------------------------------------------
CREATE TYPE role AS ENUM ('OWNER', 'STRATEGIST', 'OPERATOR', 'ANALYST');

-- ---------------------------------------------------------------------------
-- team_config — singleton row. Root entity for the single-team setup; becomes
-- `workspace` (with workspace_id added to every child table) when we go SaaS.
-- ---------------------------------------------------------------------------
CREATE TABLE team_config (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT team_config_name_not_blank CHECK (length(btrim(name)) > 0)
);

CREATE UNIQUE INDEX team_config_singleton ON team_config ((true));

-- ---------------------------------------------------------------------------
-- app_user — named app_user because `user` is reserved in Postgres.
-- ---------------------------------------------------------------------------
CREATE TABLE app_user (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    email         text        NOT NULL,
    name          text        NOT NULL,
    password_hash text        NOT NULL,
    role          role        NOT NULL DEFAULT 'OPERATOR',
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT app_user_email_lowercase CHECK (email = lower(email))
);

CREATE UNIQUE INDEX app_user_email_key ON app_user (email);

-- ---------------------------------------------------------------------------
-- audit_log — required for state-mutating admin actions (kill worker, edit
-- template, delete account, manual action). See ERD.md > Catatan.
-- ---------------------------------------------------------------------------
CREATE TABLE audit_log (
    id        uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id  uuid        REFERENCES app_user (id) ON DELETE SET NULL,
    action    text        NOT NULL,
    entity    text        NOT NULL,
    entity_id text        NOT NULL,
    diff      jsonb,
    ts        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_log_ts_idx ON audit_log (ts DESC);
CREATE INDEX audit_log_actor_ts_idx ON audit_log (actor_id, ts DESC);
