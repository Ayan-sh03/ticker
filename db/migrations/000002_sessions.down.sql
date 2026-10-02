BEGIN;
DROP TABLE sessions;
ALTER TABLE users DROP COLUMN password_hash;
COMMIT;
