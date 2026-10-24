-- No row means the email already has an account.
INSERT INTO users (email, display_name, password_hash)
VALUES ($1, $2, $3)
ON CONFLICT (email) DO NOTHING
RETURNING id::text;
