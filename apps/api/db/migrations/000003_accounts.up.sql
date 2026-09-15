-- 000003 — P1: account lifecycle & provisioning.
-- Scope: Platform/AuthStatus/AccountStatus/WorkerStatus/DesiredState/WorkerSource/
-- ProvisionOp enums; ProxyGroup, Worker, Account, Heartbeat, ProvisionLog.
--
-- Invariants (DEVELOPMENT_RULE.md §DB, ERD.md):
--   * 1 Worker (container) = 1 device hosting N accounts, AT MOST ONE per platform
--     -> UNIQUE (worker_id, platform) on account.
--   * The same platform account may not live in two containers
--     -> UNIQUE (platform, username) on account.
--   * Worker.generation guards idempotent reconcile (pod label smm.generation).

-- ---------------------------------------------------------------------------
-- enums
-- ---------------------------------------------------------------------------
CREATE TYPE platform AS ENUM (
    'INSTAGRAM', 'THREADS', 'FACEBOOK', 'TIKTOK', 'LINKEDIN', 'X', 'YOUTUBE'
);

-- login state machine, kept separate from the account lifecycle status.
CREATE TYPE auth_status AS ENUM (
    'AUTHENTICATING', 'NEEDS_INPUT', 'AUTHENTICATED', 'FAILED'
);

CREATE TYPE account_status AS ENUM (
    'PENDING', 'ACTIVE', 'PAUSED', 'QUARANTINED', 'DEAD', 'ARCHIVED'
);

CREATE TYPE worker_status AS ENUM (
    'PENDING', 'READY', 'IDLE', 'BUSY', 'DRAINING', 'ERROR', 'DEAD', 'QUARANTINED'
);

CREATE TYPE desired_state AS ENUM ('RUNNING', 'STOPPED');

-- MANUAL = created by the user from the dashboard (never auto-deleted at 0 accounts);
-- AUTO   = fallback auto-create during bin-packing (auto-deleted at 0 accounts).
CREATE TYPE worker_source AS ENUM ('MANUAL', 'AUTO');

CREATE TYPE provision_op AS ENUM ('CREATE', 'DELETE');

-- ---------------------------------------------------------------------------
-- proxy_group — residential proxy pools bound to accounts at region level.
-- pool_key is stored encrypted at rest (AES-256-GCM, see P1-07).
-- ---------------------------------------------------------------------------
CREATE TABLE proxy_group (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    name            text        NOT NULL,
    region          text        NOT NULL,
    provider        text        NOT NULL,
    pool_key        bytea       NOT NULL,
    max_concurrency int         NOT NULL DEFAULT 5,
    daily_budget_mb int         NOT NULL DEFAULT 1024,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT proxy_group_name_key UNIQUE (name),
    CONSTRAINT proxy_group_region_iso CHECK (region ~ '^[A-Z]{2}$'),
    CONSTRAINT proxy_group_max_concurrency_pos CHECK (max_concurrency > 0),
    CONSTRAINT proxy_group_daily_budget_pos CHECK (daily_budget_mb > 0)
);

-- ---------------------------------------------------------------------------
-- worker — one container / one "device". Hosts many accounts, at most one per
-- platform (enforced by the account unique constraint below).
-- ---------------------------------------------------------------------------
CREATE TABLE worker (
    id              uuid          PRIMARY KEY DEFAULT gen_random_uuid(),
    name            text          NOT NULL,
    container_id    text,
    control_channel text,
    action_queue    text,
    session_pvc     text,
    novnc_service   text,
    desired_state   desired_state NOT NULL DEFAULT 'RUNNING',
    source          worker_source NOT NULL DEFAULT 'MANUAL',
    region          text          NOT NULL,
    status          worker_status NOT NULL DEFAULT 'IDLE',
    generation      int           NOT NULL DEFAULT 1,
    observed_gen    int,
    provision_err   text,
    browser_status  text          NOT NULL DEFAULT 'cold',
    current_job_id  uuid,
    last_heartbeat  timestamptz,
    last_action_at  timestamptz,
    last_error      text,
    queue_depth     int           NOT NULL DEFAULT 0,
    restart_count   int           NOT NULL DEFAULT 0,
    image_version   text          NOT NULL DEFAULT 'v0.1',
    created_at      timestamptz   NOT NULL DEFAULT now(),
    CONSTRAINT worker_name_key UNIQUE (name),
    CONSTRAINT worker_container_id_key UNIQUE (container_id),
    CONSTRAINT worker_region_iso CHECK (region ~ '^[A-Z]{2}$'),
    CONSTRAINT worker_generation_pos CHECK (generation > 0),
    CONSTRAINT worker_queue_depth_nonneg CHECK (queue_depth >= 0),
    CONSTRAINT worker_restart_count_nonneg CHECK (restart_count >= 0)
);

CREATE INDEX worker_status_idx ON worker (status);
CREATE INDEX worker_desired_state_idx ON worker (desired_state);
CREATE INDEX worker_last_heartbeat_idx ON worker (last_heartbeat);
CREATE INDEX worker_current_job_idx ON worker (current_job_id);

-- ---------------------------------------------------------------------------
-- account — a worker account (executor). Login + Playwright action identity.
-- NOT the same as an official (monitored) account, which has no credentials.
-- ---------------------------------------------------------------------------
CREATE TABLE account (
    id               uuid           PRIMARY KEY DEFAULT gen_random_uuid(),
    platform         platform       NOT NULL,
    username         text           NOT NULL,
    -- AES-256-GCM ciphertext (12-byte nonce prepended). WRITE-ONLY: never
    -- selected or decrypted through the API (see P1-07).
    password_enc     bytea          NOT NULL,
    auth_status      auth_status    NOT NULL DEFAULT 'AUTHENTICATING',
    handle           text,
    last_verified_at timestamptz,
    -- encrypted fallback cookie blob (legacy paste-cookie path).
    credentials      jsonb,
    cookie_expiry_at timestamptz,
    proxy_group_id   uuid           REFERENCES proxy_group (id) ON DELETE SET NULL,
    health_score     int            NOT NULL DEFAULT 100,
    status           account_status NOT NULL DEFAULT 'PENDING',
    tags             text[]         NOT NULL DEFAULT '{}',
    worker_id        uuid           REFERENCES worker (id) ON DELETE SET NULL,
    last_used_at     timestamptz,
    last_checked_at  timestamptz,
    last_error       text,
    created_at       timestamptz    NOT NULL DEFAULT now(),
    -- JANTUNG: max one account per platform inside a single container.
    CONSTRAINT account_worker_platform_key UNIQUE (worker_id, platform),
    -- the same platform account must not exist in two containers.
    CONSTRAINT account_platform_username_key UNIQUE (platform, username),
    CONSTRAINT account_health_score_range CHECK (health_score BETWEEN 0 AND 100),
    CONSTRAINT account_username_not_blank CHECK (length(btrim(username)) > 0)
);

CREATE INDEX account_status_idx ON account (status);
CREATE INDEX account_auth_status_idx ON account (auth_status);
CREATE INDEX account_worker_idx ON account (worker_id);
CREATE INDEX account_health_score_idx ON account (health_score);
CREATE INDEX account_tags_idx ON account USING gin (tags);

-- ---------------------------------------------------------------------------
-- heartbeat — worker resource telemetry (reported via callback, P1-13).
-- ---------------------------------------------------------------------------
CREATE TABLE heartbeat (
    id        uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    worker_id uuid        NOT NULL REFERENCES worker (id) ON DELETE CASCADE,
    ts        timestamptz NOT NULL DEFAULT now(),
    cpu       double precision NOT NULL DEFAULT 0,
    mem       double precision NOT NULL DEFAULT 0,
    jobs_done int         NOT NULL DEFAULT 0,
    CONSTRAINT heartbeat_jobs_done_nonneg CHECK (jobs_done >= 0)
);

CREATE INDEX heartbeat_worker_ts_idx ON heartbeat (worker_id, ts DESC);

-- ---------------------------------------------------------------------------
-- provision_log — every CREATE/DELETE op the provisioner performs (audit).
-- ---------------------------------------------------------------------------
CREATE TABLE provision_log (
    id         uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
    worker_id  uuid         NOT NULL REFERENCES worker (id) ON DELETE CASCADE,
    op         provision_op NOT NULL,
    generation int          NOT NULL,
    k8s_ref    text,
    status     text         NOT NULL DEFAULT 'PENDING',
    error      text,
    ts         timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT provision_log_status_enum CHECK (status IN ('PENDING', 'APPLIED', 'FAILED'))
);

CREATE INDEX provision_log_worker_ts_idx ON provision_log (worker_id, ts DESC);
CREATE INDEX provision_log_status_idx ON provision_log (status);
