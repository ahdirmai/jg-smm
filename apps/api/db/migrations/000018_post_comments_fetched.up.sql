-- 000018 — post.comments_fetched.
--
-- `post.scraped_at` is `now()` for EVERY upsert, on-demand and keyword-batch
-- alike, and it only says "we looked at this post", not "we fetched its
-- comments". The keyword-batch path ingests a post from the search actor,
-- which returns no comment bodies (see parseIGFlatPostItems), yet still stamps
-- scraped_at = now(). The on-demand 6h freshness cache (scrape_ondemand.go)
-- keys off scraped_at alone, so a post a batch ingested five minutes ago
-- answers "no comments" as if that were a fresh answer — and the /actions
-- button had to send force=true to escape it, which in turn made EVERY
-- single-URL scrape pay even when the post was already fully scraped.
--
-- The flag separates the two truths: a permalink scrape always fetches the
-- comment bodies (set true in the on-demand path); a keyword-batch ingest
-- never does (leaves it false). The cache then short-circuits only when the
-- stored post is both fresh AND comment-complete, which is the only case
-- where returning it is honest.
--
-- Defaulted false on purpose: a row whose comment-fetching we cannot prove is
-- treated as "comments may be missing", so the cache pays once more rather
-- than serve a possibly-empty list as a fresh answer. The backfill below
-- recovers the rows we CAN prove: any post with stored comment rows was
-- ingested by a path that fetched them.
ALTER TABLE post ADD COLUMN comments_fetched boolean NOT NULL DEFAULT false;
UPDATE post
SET comments_fetched = true
WHERE EXISTS (SELECT 1 FROM comment c WHERE c.post_id = post.id);
