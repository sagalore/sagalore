-- Generic group notification webhooks (JSON POST). Rename Discord-era columns.

ALTER TABLE groups RENAME COLUMN discord_webhook_url TO webhook_url;
ALTER TABLE groups RENAME COLUMN discord_webhook_sourced TO webhook_sourced;

ALTER TABLE progress RENAME COLUMN discord_posted TO webhook_posted;
ALTER TABLE progress DROP COLUMN IF EXISTS discord_url;
