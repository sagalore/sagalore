-- Opaque JSON for webhook receivers (e.g. Smia Discord relay config).
-- The API does not interpret keys; meta is copied onto each event as "meta".

ALTER TABLE groups ADD COLUMN IF NOT EXISTS webhook_meta jsonb NOT NULL DEFAULT '{}'::jsonb;
