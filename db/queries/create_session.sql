INSERT INTO sessions (user_id, token_hash, expires_at)
VALUES ($1, $2, clock_timestamp() + interval '24 hours')
RETURNING expires_at;
