-- 000016 — post publication date.
--
-- `post` had exactly one timestamp: `scraped_at`, which is `now()`. The date a
-- post was actually PUBLISHED arrived in the actor payload (`pub_date` for the
-- IG flat search row, `timestamp` / `posted_at` / `taken_at` / `created_at` on
-- the other parsers), was used to evaluate the operator's window, and was then
-- discarded — `scrapeItem` had no field to carry it and no column to land it in.
--
-- The symptom was not a wrong number but a plausible one: the batch detail page
-- renders `scrapedAt` (`apps/web/.../scrape/batches/[id]/page.tsx:258`) with no
-- label, so every post in a batch read as the day the batch ran. A 2026-09-04
-- post scraped on 2026-09-29 displayed as 2026-09-29. Concretely it made
-- recency unsortable, made "posts from the last N days" unanswerable from the
-- DB, and made the window filter's own result unauditable without re-reading the
-- raw payload.
--
-- Nullable on purpose: existing rows are unbackfillable — the dates are gone,
-- not hidden. `scraped_at` remains the fallback and is now labelled as such in
-- the UI rather than silently standing in for a date we never had.
ALTER TABLE post ADD COLUMN published_at timestamptz;

-- Recency queries. `ListPosts` orders by scraped_at today, which sorts by when
-- we looked rather than by when it was posted; the index is here for the sort
-- and any "posted since" predicate that replaces the in-memory window filter.
CREATE INDEX post_published_at_idx ON post (published_at DESC NULLS LAST);
