-- Rewards were free text with no grant path. Change notes live on each snapshot.
ALTER TABLE quests DROP COLUMN IF EXISTS reward;
ALTER TABLE quest_versions DROP COLUMN IF EXISTS reward;
ALTER TABLE quest_versions ADD COLUMN IF NOT EXISTS changes jsonb NOT NULL DEFAULT '[]'::jsonb;
