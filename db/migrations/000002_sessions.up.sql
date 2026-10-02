BEGIN;

-- Existing users cannot log in until the demo seed assigns a password.
ALTER TABLE users ADD COLUMN password_hash text
    CHECK (password_hash IS NULL OR btrim(password_hash) <> '');

CREATE TABLE sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id),
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at)
);
CREATE INDEX sessions_user_idx ON sessions (user_id);
CREATE INDEX sessions_expiration_idx ON sessions (expires_at);

COMMIT;
