BEGIN;

-- Older code reads exactly one seat per reservation.
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM reservations WHERE quantity > 1) THEN
        RAISE EXCEPTION 'cannot roll back while multi-seat reservations exist';
    END IF;
END $$;
DROP TABLE audit_entries;
DROP FUNCTION reject_audit_change();
DROP TRIGGER reservation_seats_seat_count_check ON reservation_seats;
DROP TRIGGER reservations_seat_count_check ON reservations;
DROP FUNCTION check_reservation_quantity();
ALTER TABLE reservations DROP COLUMN quantity;

COMMIT;
