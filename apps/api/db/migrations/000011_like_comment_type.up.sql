-- 000011_like_comment_type — the "like a comment" job type (enum only).
--
-- The action matrix adds "Like comment" (like a specific comment on a post,
-- not the post itself). It rides the exact same pipeline as every other action
-- (enqueue → cooldown/rate gates → dispatch → verify); only the worker's DOM
-- step differs (it targets a comment's like heart, not the post's), so this is
-- an enum extension, not a new table.
--
-- Split across 000011/000012 for the same reason as 000008/000009: Postgres
-- refuses to *use* a value added by ALTER TYPE in the same transaction, so the
-- value is added here and referenced by the CHECK in 000012.

ALTER TYPE job_type ADD VALUE IF NOT EXISTS 'ACTION_LIKE_COMMENT';
