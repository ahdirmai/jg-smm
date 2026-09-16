-- Report builder (P4-04 / P4-05). The dashboard's report view is a pivot over
-- existing tables, not a new write path: everything here is a read that sums
-- the action or analytics history the platform already stores.

-- name: ActionRollupDaily :many
-- One row per (day, account, action type): the operator's "what did we do"
-- report. status is projected from the job (the queue's own projection), so a
-- re-run after a retry converges without double counting. NULL filters are the
-- "all" case — the caller passes NULL, not an empty string.
SELECT
    date_trunc('day', aj.scheduled_at)::date                AS day,
    aj.account_id,
    a.username,
    a.platform,
    aj.type,
    count(*)                                                AS total,
    count(*) FILTER (WHERE aj.status = 'SUCCESS')           AS succeeded,
    count(*) FILTER (WHERE aj.status = 'FAILED')            AS failed
FROM action_job AS aj
JOIN account AS a ON a.id = aj.account_id
WHERE ($1::date IS NULL OR aj.scheduled_at >= $1)
  AND ($2::date IS NULL OR aj.scheduled_at < ($2::date + interval '1 day'))
  AND ($3::text IS NULL OR a.platform = $3)
  AND ($4::uuid IS NULL OR aj.account_id = $4)
GROUP BY day, aj.account_id, a.username, a.platform, aj.type
ORDER BY day, a.username, aj.type;

-- name: ActionTargetRollup :many
-- The per-target (post) view: the same jobs grouped by what they were aimed
-- at, so a strategist can see which content attracted engagement.
SELECT
    t.id,
    t.url,
    t.platform,
    count(DISTINCT aj.id)                                   AS total,
    count(DISTINCT aj.id) FILTER (WHERE aj.status = 'SUCCESS') AS succeeded,
    count(DISTINCT aj.id) FILTER (WHERE aj.status = 'FAILED')  AS failed
FROM action_job AS aj
JOIN target AS t ON t.id = aj.target_id
WHERE ($1::date IS NULL OR aj.scheduled_at >= $1)
  AND ($2::date IS NULL OR aj.scheduled_at < ($2::date + interval '1 day'))
  AND ($3::text IS NULL OR t.platform = $3)
GROUP BY t.id, t.url, t.platform
ORDER BY total DESC
LIMIT 200;
