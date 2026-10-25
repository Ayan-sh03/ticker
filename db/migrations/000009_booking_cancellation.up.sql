BEGIN;

-- Organizers may cancel a confirmed booking. Its rows stay as history; a
-- released booking seat no longer owns its seat, so the seat can sell again.
ALTER TABLE bookings
    ADD COLUMN cancelled_at timestamptz,
    ADD COLUMN cancelled_by uuid REFERENCES users(id),
    ADD CONSTRAINT bookings_cancelled_check CHECK ((cancelled_at IS NULL) = (cancelled_by IS NULL));
ALTER TABLE booking_seats ADD COLUMN released_at timestamptz;
ALTER TABLE booking_seats DROP CONSTRAINT booking_seats_seat_id_key;
CREATE UNIQUE INDEX booking_seats_owner_idx ON booking_seats (seat_id) WHERE released_at IS NULL;

-- A cancelled booking records CONFIRMED -> CANCELLED by the organizer.
ALTER TABLE audit_entries DROP CONSTRAINT audit_entries_from_status_check;
ALTER TABLE audit_entries ADD CONSTRAINT audit_entries_from_status_check CHECK (from_status IN ('HELD', 'CONFIRMED'));
ALTER TABLE audit_entries DROP CONSTRAINT audit_entries_reason_check;
ALTER TABLE audit_entries ADD CONSTRAINT audit_entries_reason_check
    CHECK (reason IN ('customer_request', 'deadline_passed', 'organizer_request'));

COMMIT;
