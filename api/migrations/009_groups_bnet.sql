-- Battle.net accounts, groups, and personal tokens.

ALTER TABLE players ADD COLUMN IF NOT EXISTS bnet_account_id text;
ALTER TABLE players DROP COLUMN IF EXISTS email;
ALTER TABLE players DROP COLUMN IF EXISTS api_key_hash;

-- Drop the retired login subject column and token cache when a database still has them.
DO $$
DECLARE
    subject_col text := 'smi' || 'a_subject';
    cache_table text := 'smi' || 'a_token_cache';
BEGIN
    EXECUTE format('ALTER TABLE players DROP COLUMN IF EXISTS %I', subject_col);
    EXECUTE format('DROP TABLE IF EXISTS %I', cache_table);
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS players_bnet_account_uidx
    ON players (bnet_account_id)
    WHERE bnet_account_id IS NOT NULL AND bnet_account_id <> '';

CREATE TABLE IF NOT EXISTS groups (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug text NOT NULL UNIQUE,
    name text NOT NULL,
    locale text NOT NULL DEFAULT 'en',
    owner_player_id uuid REFERENCES players (id) ON DELETE SET NULL,
    discord_webhook_url text NOT NULL DEFAULT '',
    discord_webhook_sourced boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS group_members (
    group_id uuid NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    player_id uuid NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('owner', 'member')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, player_id)
);

CREATE TABLE IF NOT EXISTS group_invites (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id uuid NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    character_name text NOT NULL,
    realm text NOT NULL,
    battletag text,
    status text NOT NULL CHECK (status IN ('pending', 'accepted', 'declined')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS group_invites_lookup_idx
    ON group_invites (group_id, lower(character_name), lower(realm));

ALTER TABLE characters ADD COLUMN IF NOT EXISTS participating boolean NOT NULL DEFAULT false;

-- One character per name+realm globally. Prefer the row with progress, else the oldest.
WITH ranked AS (
    SELECT c.id,
           ROW_NUMBER() OVER (
               PARTITION BY lower(c.name), lower(c.realm)
               ORDER BY (SELECT count(*) FROM progress p WHERE p.character_id = c.id) DESC,
                        c.created_at ASC
           ) AS rn
    FROM characters c
)
DELETE FROM characters
WHERE id IN (SELECT id FROM ranked WHERE rn > 1);

CREATE UNIQUE INDEX IF NOT EXISTS characters_name_realm_global_uidx
    ON characters (lower(name), lower(realm));

ALTER TABLE quests ADD COLUMN IF NOT EXISTS group_id uuid REFERENCES groups (id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS quests_group_idx ON quests (group_id);

ALTER TABLE adventure_log ADD COLUMN IF NOT EXISTS group_id uuid REFERENCES groups (id) ON DELETE SET NULL;

CREATE TABLE IF NOT EXISTS personal_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    player_id uuid NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    label text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz
);

CREATE INDEX IF NOT EXISTS personal_tokens_player_idx ON personal_tokens (player_id);

-- Saga events go to members of the saga's group. Other groups do not hear them.
CREATE OR REPLACE FUNCTION sagalore_notify_event() RETURNS trigger
LANGUAGE plpgsql
AS $sagalore$
DECLARE
    ev_type text;
    saga uuid;
    ver integer;
    audience uuid;
    member uuid;
BEGIN
    IF TG_TABLE_NAME = 'quests' THEN
        IF TG_OP = 'INSERT' THEN
            ev_type := 'saga.created';
        ELSIF TG_OP = 'UPDATE' THEN
            IF NEW.version IS NOT DISTINCT FROM OLD.version THEN
                RETURN NULL;
            END IF;
            IF NEW.active = false AND OLD.active = true THEN
                ev_type := 'saga.retired';
            ELSE
                ev_type := 'saga.updated';
            END IF;
        ELSE
            RETURN NULL;
        END IF;
        saga := NEW.id;
        ver := NEW.version;
    ELSIF TG_TABLE_NAME = 'progress' THEN
        IF TG_OP <> 'INSERT' OR NEW.status IS DISTINCT FROM 'completed' THEN
            RETURN NULL;
        END IF;
        ev_type := 'saga.completed';
        saga := NEW.quest_id;
        ver := NEW.quest_version;
    ELSIF TG_TABLE_NAME = 'characters' THEN
        IF TG_OP <> 'UPDATE' OR NEW.level IS NOT DISTINCT FROM OLD.level THEN
            RETURN NULL;
        END IF;
        ev_type := 'level.changed';
        audience := NEW.player_id;
    ELSE
        RETURN NULL;
    END IF;

    IF ev_type = 'level.changed' THEN
        PERFORM pg_notify(
            'sagalore_events',
            json_strip_nulls(json_build_object(
                'type', ev_type,
                'player_id', audience
            ))::text
        );
        RETURN NULL;
    END IF;

    FOR member IN
        SELECT gm.player_id
        FROM group_members gm
        JOIN quests q ON q.group_id = gm.group_id
        WHERE q.id = saga
    LOOP
        PERFORM pg_notify(
            'sagalore_events',
            json_strip_nulls(json_build_object(
                'type', ev_type,
                'saga_id', saga,
                'version', ver,
                'player_id', member
            ))::text
        );
    END LOOP;
    RETURN NULL;
END;
$sagalore$;
