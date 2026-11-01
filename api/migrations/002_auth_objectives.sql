-- Sessions. BattleTag may arrive with the character report. A fresh database
-- never creates the retired login cache; migration 009 drops it if present.

ALTER TABLE players ADD COLUMN email text NOT NULL DEFAULT '';
ALTER TABLE players ALTER COLUMN battletag DROP NOT NULL;
ALTER TABLE players DROP CONSTRAINT players_battletag_key;
CREATE UNIQUE INDEX players_battletag_uidx
    ON players (lower(battletag))
    WHERE battletag IS NOT NULL AND battletag <> '';
ALTER TABLE players ALTER COLUMN api_key_hash DROP NOT NULL;

CREATE TABLE sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    player_id uuid NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);

CREATE INDEX sessions_player_idx ON sessions (player_id);

ALTER TABLE quests ALTER COLUMN objectives DROP DEFAULT;
ALTER TABLE quests
    ALTER COLUMN objectives TYPE jsonb
    USING (
        CASE
            WHEN objectives IS NULL OR btrim(objectives) = '' THEN '[]'::jsonb
            WHEN left(btrim(objectives), 1) = '[' THEN objectives::jsonb
            ELSE jsonb_build_array(jsonb_build_object('id', 'legacy', 'type', 'legacy', 'text', objectives))
        END
    );
ALTER TABLE quests ALTER COLUMN objectives SET DEFAULT '[]'::jsonb;
ALTER TABLE quests ALTER COLUMN objectives SET NOT NULL;

ALTER TABLE quest_versions ALTER COLUMN objectives DROP DEFAULT;
ALTER TABLE quest_versions
    ALTER COLUMN objectives TYPE jsonb
    USING (
        CASE
            WHEN objectives IS NULL OR btrim(objectives) = '' THEN '[]'::jsonb
            WHEN left(btrim(objectives), 1) = '[' THEN objectives::jsonb
            ELSE jsonb_build_array(jsonb_build_object('id', 'legacy', 'type', 'legacy', 'text', objectives))
        END
    );
ALTER TABLE quest_versions ALTER COLUMN objectives SET DEFAULT '[]'::jsonb;
ALTER TABLE quest_versions ALTER COLUMN objectives SET NOT NULL;
