-- A consumed Allow can be repeated for a short window, and the browser is sent
-- through a same-origin page before it leaves for the client. The raw code is
-- stored only so that repeat can return the same value. Exchange still uses
-- the hash in oauth_codes and accepts the code once.

CREATE TABLE IF NOT EXISTS oauth_approve_replays (
    request_id uuid PRIMARY KEY,
    code text NOT NULL,
    redirect_uri text NOT NULL,
    state text NOT NULL,
    csrf_hash text NOT NULL,
    consent_session_hash text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS oauth_handoffs (
    ticket_hash text PRIMARY KEY,
    target text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
