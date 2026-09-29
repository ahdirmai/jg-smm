-- 000016 — post publication date. Reversible: the column is additive and the
-- index is dropped with it. Values are not recoverable on re-apply — rows
-- ingested after 000016 keep published_at once migrated forward again.
DROP INDEX IF EXISTS post_published_at_idx;
ALTER TABLE post DROP COLUMN IF EXISTS published_at;
