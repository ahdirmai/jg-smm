-- CommentTemplate (P3-02): the comment pool. A template is platform-scoped,
-- declares the vars its text may reference ({topic}), carries a pick weight and
-- a per-template denylist, and can be soft-disabled without deleting the rows
-- that explain past action_logs.
--
-- Dedupe: PickForTarget excludes any template already used against THIS target
-- in the last 7 days, so the same variant does not repeat on one post. It is a
-- query, not a constraint: a CHECK cannot express a temporal join, and dedupe
-- is a pool property (which variants are still fresh), not a row property.

-- name: CreateCommentTemplate :one
INSERT INTO comment_template (platform, text, vars, weight, banned_words, is_active)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, platform, text, vars, weight, banned_words, is_active, created_at;

-- name: GetCommentTemplateByID :one
SELECT id, platform, text, vars, weight, banned_words, is_active, created_at
FROM comment_template
WHERE id = $1;

-- name: ListCommentTemplates :many
-- The pool view for one platform. include_inactive is the dashboard's
-- "show paused variants" toggle; the pool a pick draws from is always active-only
-- (see PickForTarget).
SELECT id, platform, text, vars, weight, banned_words, is_active, created_at
FROM comment_template
WHERE platform = $1
  AND ($2::boolean OR is_active = TRUE)
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- name: ListAllCommentTemplates :many
-- The pool view across EVERY platform. The composer's pick is platform-scoped
-- (see PickForTarget), but the dashboard listing needs to show all variants
-- when no platform filter is applied — otherwise non-Instagram variants are
-- invisible and the platform filter has nothing to filter.
SELECT id, platform, text, vars, weight, banned_words, is_active, created_at
FROM comment_template
WHERE ($1::boolean OR is_active = TRUE)
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: UpdateCommentTemplate :one
-- Full-row update: weight/vars/banned_words/is_active all change together from
-- the composer, so a partial-update builder would only hide a missed field.
UPDATE comment_template
SET text         = $2,
    vars         = $3,
    weight       = $4,
    banned_words = $5,
    is_active    = $6
WHERE id = $1
RETURNING id, platform, text, vars, weight, banned_words, is_active, created_at;

-- name: DeleteCommentTemplate :exec
-- Hard delete is allowed: ON DELETE SET NULL keeps the history readable, it
-- only loses the link to the variant's definition.
DELETE FROM comment_template WHERE id = $1;

-- name: PickForTarget :many
-- The dedupe pool for one (platform, target): every active template that has
-- NOT been used against this target in the last 7 days, heaviest first. The
-- caller does the weighted random pick over this candidate set (see the
-- template service) — weighting in SQL needs a seed and is harder to test.
SELECT t.id, t.platform, t.text, t.vars, t.weight, t.banned_words, t.is_active,
       t.created_at
FROM comment_template AS t
WHERE t.platform  = $1
  AND t.is_active = TRUE
  AND NOT EXISTS (
      SELECT 1
      FROM action_log AS al
      WHERE al.template_id = t.id
        AND al.action_job_id IN (
            SELECT aj.id
            FROM action_job AS aj
            WHERE aj.target_id = $2
        )
        AND al.ts >= now() - INTERVAL '7 days'
  )
ORDER BY t.weight DESC, t.created_at;
