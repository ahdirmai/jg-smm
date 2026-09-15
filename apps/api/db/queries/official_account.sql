-- OfficialAccount: a monitored brand/client account (read-only). NOT an
-- `account` (executor) — no credentials, no login, no actions. Metrics come
-- from a 3rd-party provider and land in analytics_snapshot/analytics_mention.

-- name: GetOfficialAccountByID :one
SELECT id, platform, handle, display_name, profile_url, avatar_url, status,
       provider, provider_ref, tags, last_fetched_at, created_at
FROM official_account
WHERE id = $1;

-- name: GetOfficialAccountByHandle :one
SELECT id, platform, handle, display_name, profile_url, avatar_url, status,
       provider, provider_ref, tags, last_fetched_at, created_at
FROM official_account
WHERE platform = $1 AND handle = $2;

-- name: ListOfficialAccounts :many
SELECT id, platform, handle, display_name, profile_url, avatar_url, status,
       provider, provider_ref, tags, last_fetched_at, created_at
FROM official_account
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: ListOfficialAccountsByPlatform :many
SELECT id, platform, handle, display_name, profile_url, avatar_url, status,
       provider, provider_ref, tags, last_fetched_at, created_at
FROM official_account
WHERE platform = $1
ORDER BY handle
LIMIT $2 OFFSET $3;

-- name: CountOfficialAccountsByPlatform :one
SELECT count(*) FROM official_account WHERE platform = $1;

-- name: CreateOfficialAccount :one
INSERT INTO official_account (
    platform, handle, display_name, profile_url, avatar_url, status, provider,
    provider_ref, tags
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id, platform, handle, display_name, profile_url, avatar_url, status,
          provider, provider_ref, tags, last_fetched_at, created_at;

-- name: UpdateOfficialAccount :one
UPDATE official_account SET
    platform       = $2,
    handle         = $3,
    display_name   = $4,
    profile_url    = $5,
    avatar_url     = $6,
    status         = $7,
    provider       = $8,
    provider_ref   = $9,
    tags           = $10
WHERE id = $1
RETURNING id, platform, handle, display_name, profile_url, avatar_url, status,
          provider, provider_ref, tags, last_fetched_at, created_at;

-- name: TouchOfficialAccountFetched :exec
-- written by the ingestor only after a successful snapshot write, so
-- last_fetched_at always means "data present", not "attempt ran".
UPDATE official_account SET last_fetched_at = now() WHERE id = $1;

-- name: ArchiveOfficialAccount :one
UPDATE official_account SET status = 'ARCHIVED'
WHERE id = $1
RETURNING id, platform, handle, display_name, profile_url, avatar_url, status,
          provider, provider_ref, tags, last_fetched_at, created_at;

-- name: ListOfficialAccountsForIngest :many
-- every ACTIVE account for a provider, walked by the ingestor cron tick.
SELECT id, platform, handle, display_name, profile_url, avatar_url, status,
       provider, provider_ref, tags, last_fetched_at, created_at
FROM official_account
WHERE status   = 'ACTIVE'
  AND provider = $1
ORDER BY last_fetched_at NULLS FIRST, handle
LIMIT $2;
