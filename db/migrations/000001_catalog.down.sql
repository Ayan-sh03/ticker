BEGIN;
DROP TABLE seats;
DROP TABLE events;
DROP FUNCTION guard_seat_layout();
DROP FUNCTION guard_event_catalog();
DROP TABLE memberships;
DROP TABLE tenants;
DROP TABLE users;
COMMIT;
