-- Sagalore initial schema.

CREATE TABLE players (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    battletag text NOT NULL UNIQUE,
    display_name text NOT NULL,
    api_key_hash text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE characters (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    player_id uuid NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    name text NOT NULL,
    realm text NOT NULL,
    race text NOT NULL DEFAULT '',
    class text NOT NULL DEFAULT '',
    level integer NOT NULL DEFAULT 1 CHECK (level >= 1 AND level <= 1000),
    backstory text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX characters_player_name_realm_lower
    ON characters (player_id, lower(name), lower(realm));

CREATE TABLE quests (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    title text NOT NULL,
    description text NOT NULL DEFAULT '',
    objectives text NOT NULL DEFAULT '',
    reward text NOT NULL DEFAULT '',
    min_level integer,
    party boolean NOT NULL DEFAULT false,
    active boolean NOT NULL DEFAULT true,
    version integer NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_by text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE quest_assignments (
    quest_id uuid NOT NULL REFERENCES quests (id) ON DELETE CASCADE,
    character_id uuid NOT NULL REFERENCES characters (id) ON DELETE CASCADE,
    PRIMARY KEY (quest_id, character_id)
);

-- One immutable snapshot per quest version. assigned_to is a JSON array of character ids.
CREATE TABLE quest_versions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    quest_id uuid NOT NULL REFERENCES quests (id) ON DELETE CASCADE,
    version integer NOT NULL CHECK (version >= 1),
    title text NOT NULL,
    description text NOT NULL DEFAULT '',
    objectives text NOT NULL DEFAULT '',
    reward text NOT NULL DEFAULT '',
    min_level integer,
    party boolean NOT NULL DEFAULT false,
    active boolean NOT NULL DEFAULT true,
    assigned_to jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_by text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (quest_id, version)
);

CREATE TABLE screenshots (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    content_type text NOT NULL,
    bytes bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- One row per character + quest. The first completion wins and keeps the quest
-- version the player had loaded. Repeating the call is a no-op.
CREATE TABLE progress (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    character_id uuid NOT NULL REFERENCES characters (id) ON DELETE CASCADE,
    quest_id uuid NOT NULL REFERENCES quests (id) ON DELETE CASCADE,
    quest_version integer NOT NULL,
    status text NOT NULL CHECK (status IN ('offered', 'active', 'completed')),
    completed_at timestamptz,
    level_at_completion integer,
    screenshot_id uuid REFERENCES screenshots (id),
    discord_posted boolean NOT NULL DEFAULT false,
    discord_url text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (character_id, quest_id)
);

CREATE INDEX progress_character_idx ON progress (character_id);

CREATE TABLE adventure_log (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    player_id uuid REFERENCES players (id) ON DELETE SET NULL,
    character_id uuid REFERENCES characters (id) ON DELETE SET NULL,
    quest_id uuid REFERENCES quests (id) ON DELETE SET NULL,
    type text NOT NULL,
    title text NOT NULL,
    body text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX adventure_log_created_idx ON adventure_log (created_at DESC);
