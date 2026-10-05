-- 000018 — post.comments_fetched. Reversible: the column is additive. The
-- backfilled values are not recoverable on re-apply — rows ingested after
-- 000018 keep comments_fetched once migrated forward again.
ALTER TABLE post DROP COLUMN IF EXISTS comments_fetched;
