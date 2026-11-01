-- Optional saga clock window and group requirement.
-- Null means the saga has no extra gate. Objective-level windows live in objectives jsonb.

ALTER TABLE quests
    ADD COLUMN IF NOT EXISTS time_window jsonb,
    ADD COLUMN IF NOT EXISTS group_rule jsonb;

ALTER TABLE quest_versions
    ADD COLUMN IF NOT EXISTS time_window jsonb,
    ADD COLUMN IF NOT EXISTS group_rule jsonb;
