BEGIN;

-- Older code reads every booking as active and every seat as booked once.
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM bookings WHERE cancelled_at IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot roll back while cancelled bookings exist';
    END IF;
END $$;
ALTER TABLE audit_entries DROP CONSTRAINT audit_entries_reason_check;
ALTER TABLE audit_entries ADD CONSTRAINT audit_entries_reason_check CHECK (reason IN ('customer_request', 'deadline_passed'));
ALTER TABLE audit_entries DROP CONSTRAINT audit_entries_from_status_check;
ALTER TABLE audit_entries ADD CONSTRAINT audit_entries_from_status_check CHECK (from_status = 'HELD');
DROP INDEX booking_seats_owner_idx;
ALTER TABLE booking_seats ADD CONSTRAINT booking_seats_seat_id_key UNIQUE (seat_id);
ALTER TABLE booking_seats DROP COLUMN released_at;
ALTER TABLE bookings DROP COLUMN cancelled_by, DROP COLUMN cancelled_at;

COMMIT;
