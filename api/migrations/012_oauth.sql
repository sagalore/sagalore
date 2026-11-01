-- OAuth 2.1 clients, authorization requests, codes, and rotating refresh tokens.
-- An access token is a gst_ personal token with an expiry. A refresh token is
-- not an API key. Existing personal tokens leave expires_at null and keep
-- working until they are revoked.
--
-- Version 012 because 010 and 011 are already applied in production. migrate
-- skips a version it has recorded, so this file must not be numbered 010.

ALTER TABLE personal_tokens ADD COLUMN IF NOT EXISTS expires_at timestamptz;
ALTER TABLE personal_tokens ADD COLUMN IF NOT EXISTS oauth_client_id text;
ALTER TABLE personal_tokens ADD COLUMN IF NOT EXISTS oauth_family_id uuid;

CREATE INDEX IF NOT EXISTS personal_tokens_oauth_family_idx
    ON personal_tokens (oauth_family_id)
    WHERE oauth_family_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS oauth_clients (
    client_id text PRIMARY KEY,
    client_secret_hash text,
    client_name text NOT NULL,
    redirect_uris jsonb NOT NULL,
    grant_types jsonb NOT NULL,
    response_types jsonb NOT NULL,
    token_endpoint_auth_method text NOT NULL,
    scope text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS oauth_requests (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id text NOT NULL,
    redirect_uri text NOT NULL,
    state text NOT NULL,
    code_challenge text NOT NULL,
    scope text NOT NULL,
    resource text NOT NULL DEFAULT '',
    csrf_hash text NOT NULL DEFAULT '',
    consent_session_hash text NOT NULL DEFAULT '',
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE oauth_requests ADD COLUMN IF NOT EXISTS csrf_hash text NOT NULL DEFAULT '';
ALTER TABLE oauth_requests ADD COLUMN IF NOT EXISTS consent_session_hash text NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS oauth_codes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code_hash text NOT NULL UNIQUE,
    client_id text NOT NULL,
    player_id uuid NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    redirect_uri text NOT NULL,
    code_challenge text NOT NULL,
    scope text NOT NULL,
    resource text NOT NULL DEFAULT '',
    family_id uuid,
    expires_at timestamptz NOT NULL,
    used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS oauth_refresh_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash text NOT NULL UNIQUE,
    family_id uuid NOT NULL,
    client_id text NOT NULL,
    player_id uuid NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    scope text NOT NULL,
    resource text NOT NULL DEFAULT '',
    family_expires_at timestamptz NOT NULL DEFAULT now() + interval '90 days',
    expires_at timestamptz NOT NULL,
    rotated_at timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE oauth_refresh_tokens ADD COLUMN IF NOT EXISTS family_expires_at timestamptz;
UPDATE oauth_refresh_tokens
SET family_expires_at = created_at + interval '90 days'
WHERE family_expires_at IS NULL;
ALTER TABLE oauth_refresh_tokens ALTER COLUMN family_expires_at SET NOT NULL;

CREATE INDEX IF NOT EXISTS oauth_refresh_family_idx ON oauth_refresh_tokens (family_id);

-- Browser sign-in for the consent page. The raw token is goc_ and is not an API key.
CREATE TABLE IF NOT EXISTS oauth_consent_sessions (
    token_hash text PRIMARY KEY,
    player_id uuid NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Battle.net PKCE attempts. kind separates companion polls from OAuth consent,
-- so the same attempt id cannot cross the two flows. state is what Battle.net echoes.
CREATE TABLE IF NOT EXISTS bnet_attempts (
    kind text NOT NULL,
    id text NOT NULL,
    verifier text NOT NULL,
    state text NOT NULL UNIQUE,
    oauth_request_id text NOT NULL DEFAULT '',
    session_json jsonb,
    error_code text NOT NULL DEFAULT '',
    error_message text NOT NULL DEFAULT '',
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (kind, id)
);

CREATE TABLE IF NOT EXISTS oauth_register_attempts (
    id bigserial PRIMARY KEY,
    ip text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS oauth_register_attempts_ip_idx
    ON oauth_register_attempts (ip, created_at);
