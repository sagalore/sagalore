-- Shareable group join links (open or gated) and owner-reviewed join requests.

CREATE TABLE IF NOT EXISTS group_join_links (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id uuid NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    created_by uuid NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    mode text NOT NULL CHECK (mode IN ('open', 'gated')),
    expires_at timestamptz NOT NULL,
    max_uses integer CHECK (max_uses IS NULL OR max_uses > 0),
    use_count integer NOT NULL DEFAULT 0 CHECK (use_count >= 0),
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS group_join_links_group_idx
    ON group_join_links (group_id, created_at DESC);

CREATE TABLE IF NOT EXISTS group_join_requests (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    link_id uuid NOT NULL REFERENCES group_join_links (id) ON DELETE CASCADE,
    group_id uuid NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    player_id uuid NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (link_id, player_id)
);

CREATE INDEX IF NOT EXISTS group_join_requests_group_status_idx
    ON group_join_requests (group_id, status, created_at);

CREATE TABLE IF NOT EXISTS group_join_request_characters (
    request_id uuid NOT NULL REFERENCES group_join_requests (id) ON DELETE CASCADE,
    character_id uuid NOT NULL REFERENCES characters (id) ON DELETE CASCADE,
    PRIMARY KEY (request_id, character_id)
);
