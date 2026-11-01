-- Saga chains. requires is every prerequisite saga id (all must be completed
-- by that character). chain_* is an optional display group. Empty chain_id
-- means the saga is not in a chain. A completion on any quest_version still
-- satisfies a prerequisite. See the README section Saga-kjeder.

ALTER TABLE quests
    ADD COLUMN IF NOT EXISTS requires jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS chain_id text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS chain_name text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS chain_order integer;

ALTER TABLE quest_versions
    ADD COLUMN IF NOT EXISTS requires jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS chain_id text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS chain_name text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS chain_order integer;

ALTER TABLE quests DROP CONSTRAINT IF EXISTS quests_requires_array;
ALTER TABLE quests
    ADD CONSTRAINT quests_requires_array CHECK (jsonb_typeof(requires) = 'array');

ALTER TABLE quests DROP CONSTRAINT IF EXISTS quests_chain_fields;
ALTER TABLE quests
    ADD CONSTRAINT quests_chain_fields CHECK (
        (chain_id = '' AND chain_name = '' AND chain_order IS NULL)
        OR (chain_id <> '' AND chain_name <> '' AND chain_order IS NOT NULL AND chain_order >= 1)
    );

CREATE UNIQUE INDEX IF NOT EXISTS quests_chain_order_uidx
    ON quests (chain_id, chain_order)
    WHERE chain_id <> '';
