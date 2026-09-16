-- Audit trail queries (P6-10). The read path joins app_user so the dashboard
-- gets a human-readable actor (email + name) without a second round trip; the
-- actor is NULL for system-initiated rows, hence the LEFT JOIN.
--
-- Optional filters follow the report.sql convention: an unset bound is passed
-- as an empty value, so each clause also treats '' as "no filter" (the
-- `::type IS NULL` arm stays for a real NULL if a caller ever sends one).

-- name: InsertAuditLog :one
INSERT INTO audit_log (actor_id, action, entity, entity_id, diff, ip, result)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, actor_id, action, entity, entity_id, diff, ip, result, ts;

-- name: ListAuditLogsFiltered :many
SELECT
    a.id,
    a.actor_id,
    a.action,
    a.entity,
    a.entity_id,
    a.result,
    a.ip,
    a.ts,
    u.email  AS actor_email,
    u.name   AS actor_name
FROM audit_log a
LEFT JOIN app_user u ON u.id = a.actor_id
WHERE ($1::uuid         IS NULL OR a.actor_id = $1)
  AND ($2::text         IS NULL OR $2 = '' OR a.action   = $2)
  AND ($3::text         IS NULL OR $3 = '' OR a.entity   = $3)
  AND ($4::timestamptz  IS NULL OR a.ts >= $4)
  AND ($5::timestamptz  IS NULL OR a.ts <  $5)
ORDER BY a.ts DESC
LIMIT $6 OFFSET $7;

-- name: CountAuditLogsFiltered :one
SELECT count(*)
FROM audit_log a
WHERE ($1::uuid         IS NULL OR a.actor_id = $1)
  AND ($2::text         IS NULL OR $2 = '' OR a.action   = $2)
  AND ($3::text         IS NULL OR $3 = '' OR a.entity   = $3)
  AND ($4::timestamptz  IS NULL OR a.ts >= $4)
  AND ($5::timestamptz  IS NULL OR a.ts <  $5);
