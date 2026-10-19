-- 000019 — sentiment on post and comment.
--
-- Analysts read a scraped post as "what is the room saying", and until now the
-- dashboard could only show the raw comment bodies — the operator had to read
-- every one to judge the mood. This stores one LLM verdict per row so the
-- dashboard renders a distribution (how many positive / neutral / negative)
-- and a per-row label without paying for the analysis on every page view.
--
-- Shape, and why:
--   * sentiment is an enum, not a score. A 0-1 float implies a precision the
--     model does not have, and the operator reads "negative", not "0.31".
--   * sentiment_score (nullable) keeps the model's own confidence for
--     tie-breaking and for auditing a surprising label. NULL means "the model
--     did not report one", never 0 — the same rule the analytics metrics use.
--   * sentiment_at marks WHEN, so a re-scrape can skip rows already analysed
--     and so a verdict from stale text is identifiable. A row is analysed once;
--     normalise() keeps the pair consistent.
--
-- Nullable on purpose. Sentiment is derived, and an unconfigured LLM (or a
-- failed call) must leave the row readable: NULL renders as "not analysed" and
-- the dashboard shows no badge, which is honest. A DEFAULT would invent a
-- verdict for content nobody analysed.
--
-- comment.sentiment carries the most weight: a post's aggregate is computed
-- from its comments, and a single negative reply under a positive post is
-- exactly the signal an operator wants to see before replying to it.
CREATE TYPE sentiment AS ENUM ('POSITIVE', 'NEUTRAL', 'NEGATIVE');

ALTER TABLE post    ADD COLUMN sentiment       sentiment;
ALTER TABLE post    ADD COLUMN sentiment_score double precision;
ALTER TABLE post    ADD COLUMN sentiment_at    timestamptz;
ALTER TABLE comment ADD COLUMN sentiment       sentiment;
ALTER TABLE comment ADD COLUMN sentiment_score double precision;
ALTER TABLE comment ADD COLUMN sentiment_at    timestamptz;

-- The score is a probability-like confidence; anything outside 0..1 is a bad
-- write, not a judgement call. NULL is allowed (see above).
ALTER TABLE post ADD CONSTRAINT post_sentiment_score_range
    CHECK (sentiment_score IS NULL OR (sentiment_score >= 0 AND sentiment_score <= 1));
ALTER TABLE comment ADD CONSTRAINT comment_sentiment_score_range
    CHECK (sentiment_score IS NULL OR (sentiment_score >= 0 AND sentiment_score <= 1));

-- A label without a timestamp (or the reverse) would make "already analysed"
-- undecidable, and the ingest would re-pay for the same row forever.
ALTER TABLE post ADD CONSTRAINT post_sentiment_pair
    CHECK ((sentiment IS NULL) = (sentiment_at IS NULL));
ALTER TABLE comment ADD CONSTRAINT comment_sentiment_pair
    CHECK ((sentiment IS NULL) = (sentiment_at IS NULL));

-- The dashboard reads a post's comments grouped by verdict ("3 negative"),
-- and lists the negatives first, so filter+sort on the label.
CREATE INDEX comment_sentiment_idx ON comment (post_id, sentiment);
