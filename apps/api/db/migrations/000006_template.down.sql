-- 000006 down — reverse the template engine.
-- Order: drop the FK columns/indexes first, then the table. The action tables
-- must not be dropped; only the template link is removed.
ALTER TABLE action_log DROP COLUMN IF EXISTS template_id;
ALTER TABLE action_job DROP COLUMN IF EXISTS template_id;
DROP TABLE IF EXISTS comment_template;
