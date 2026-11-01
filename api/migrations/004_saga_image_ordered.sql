-- Saga cover image and whether goals must be completed in list order.
-- objectives_ordered defaults to false (any order).

ALTER TABLE quests
    ADD COLUMN IF NOT EXISTS objectives_ordered boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS image_content_type text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS image_bytes bytea;

ALTER TABLE quest_versions
    ADD COLUMN IF NOT EXISTS objectives_ordered boolean NOT NULL DEFAULT false;
