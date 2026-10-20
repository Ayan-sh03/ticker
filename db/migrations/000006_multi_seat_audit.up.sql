BEGIN;

-- Existing holds all have one seat. New rows must state their quantity.
ALTER TABLE reservations ADD COLUMN quantity smallint NOT NULL DEFAULT 1 CHECK (quantity BETWEEN 1 AND 6);
ALTER TABLE reservations ALTER COLUMN quantity DROP DEFAULT;

-- Checked at commit, so a transaction may insert the reservation before its seats.
-- A partially allocated hold therefore cannot commit.
CREATE FUNCTION check_reservation_quantity() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    target uuid;
    expected smallint;
BEGIN
    IF TG_TABLE_NAME = 'reservations' THEN
        target := NEW.id;
    ELSE
        target := NEW.reservation_id;
    END IF;
    SELECT quantity INTO expected FROM reservations WHERE id = target;
    -- A failed reservation is deleted in the same transaction.
    IF FOUND AND expected <> (SELECT count(*) FROM reservation_seats WHERE reservation_id = target) THEN
        RAISE EXCEPTION 'reservation % must hold exactly % seats', target, expected USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$;
CREATE CONSTRAINT TRIGGER reservations_seat_count_check AFTER INSERT ON reservations
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_reservation_quantity();
CREATE CONSTRAINT TRIGGER reservation_seats_seat_count_check AFTER INSERT ON reservation_seats
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_reservation_quantity();

CREATE TABLE audit_entries (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id uuid NOT NULL,
    event_id uuid NOT NULL,
    reservation_id uuid NOT NULL,
    -- NULL means the expiration worker.
    actor_user_id uuid REFERENCES users(id),
    from_status text CHECK (from_status = 'HELD'),
    to_status text NOT NULL CHECK (to_status IN ('HELD', 'CONFIRMED', 'CANCELLED', 'EXPIRED')),
    reason text NOT NULL CHECK (reason IN ('customer_request', 'deadline_passed')),
    request_key text,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (tenant_id, event_id, reservation_id) REFERENCES reservations (tenant_id, event_id, id),
    CHECK ((from_status IS NULL) = (to_status = 'HELD'))
);
CREATE INDEX audit_entries_reservation_idx ON audit_entries (reservation_id, id);

CREATE FUNCTION reject_audit_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit entries are append-only' USING ERRCODE = '23514';
END;
$$;
CREATE TRIGGER audit_entries_append_only BEFORE UPDATE OR DELETE ON audit_entries
    FOR EACH ROW EXECUTE FUNCTION reject_audit_change();

COMMIT;
