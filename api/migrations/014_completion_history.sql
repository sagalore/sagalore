-- Completion history. Each finish can be its own row so a later repeatable
-- saga does not need another migration. The API still records only the first
-- finish per character and saga; repeatable sagas are not enabled.
-- started_at is the addon clock when the first goal gained progress.
-- duration_sec is completed_at - started_at, computed by the server.
-- Both times are addon-reported and are not verified.

ALTER TABLE progress ADD COLUMN IF NOT EXISTS started_at timestamptz;
ALTER TABLE progress ADD COLUMN IF NOT EXISTS duration_sec integer;

ALTER TABLE progress DROP CONSTRAINT IF EXISTS progress_character_id_quest_id_key;

ALTER TABLE progress DROP CONSTRAINT IF EXISTS progress_duration_sec_check;
ALTER TABLE progress ADD CONSTRAINT progress_duration_sec_check
    CHECK (duration_sec IS NULL OR duration_sec >= 0);

CREATE INDEX IF NOT EXISTS progress_character_quest_idx
    ON progress (character_id, quest_id);

CREATE INDEX IF NOT EXISTS progress_quest_completed_idx
    ON progress (quest_id, completed_at);
