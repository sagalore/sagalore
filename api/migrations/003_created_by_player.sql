ALTER TABLE quests
    ADD COLUMN IF NOT EXISTS created_by_player_id UUID REFERENCES players(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS quests_created_by_player_idx ON quests(created_by_player_id);
