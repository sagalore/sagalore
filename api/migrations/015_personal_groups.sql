-- Account-scoped personal groups for onboarding starters.

ALTER TABLE groups ADD COLUMN IF NOT EXISTS personal boolean NOT NULL DEFAULT false;

CREATE UNIQUE INDEX IF NOT EXISTS groups_personal_owner_uidx
    ON groups (owner_player_id)
    WHERE personal AND owner_player_id IS NOT NULL;
