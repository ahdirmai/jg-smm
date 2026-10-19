-- Reverse 000019: drop the sentiment columns, constraints and enum.
-- Columns are dropped before the type: a type that still has dependents
-- cannot be dropped, and the CHECK constraints go with their columns.
DROP INDEX IF EXISTS comment_sentiment_idx;

ALTER TABLE comment DROP CONSTRAINT IF EXISTS comment_sentiment_pair;
ALTER TABLE comment DROP CONSTRAINT IF EXISTS comment_sentiment_score_range;
ALTER TABLE post    DROP CONSTRAINT IF EXISTS post_sentiment_pair;
ALTER TABLE post    DROP CONSTRAINT IF EXISTS post_sentiment_score_range;

ALTER TABLE comment DROP COLUMN IF EXISTS sentiment_at;
ALTER TABLE comment DROP COLUMN IF EXISTS sentiment_score;
ALTER TABLE comment DROP COLUMN IF EXISTS sentiment;

ALTER TABLE post DROP COLUMN IF EXISTS sentiment_at;
ALTER TABLE post DROP COLUMN IF EXISTS sentiment_score;
ALTER TABLE post DROP COLUMN IF EXISTS sentiment;

DROP TYPE IF EXISTS sentiment;
