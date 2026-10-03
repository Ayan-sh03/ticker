BEGIN;

-- Refuse rollback with permanent inventory rather than silently undoing sales.
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM bookings) OR EXISTS (SELECT 1 FROM seats WHERE status = 'SOLD')
        OR EXISTS (SELECT 1 FROM reservations WHERE status = 'CONFIRMED') THEN
        RAISE EXCEPTION 'cannot roll back bookings while confirmed inventory exists';
    END IF;
END $$;
DROP TABLE booking_seats;
DROP TABLE bookings;
DELETE FROM idempotency_records WHERE operation = 'confirm';
ALTER TABLE reservation_seats DROP CONSTRAINT reservation_seats_booking_reference;
ALTER TABLE reservations DROP CONSTRAINT reservations_tenant_id_event_id_id_user_id_key;
ALTER TABLE reservations DROP CONSTRAINT reservations_status_check;
ALTER TABLE reservations ADD CHECK (status IN ('HELD', 'CANCELLED', 'EXPIRED'));
ALTER TABLE seats DROP CONSTRAINT seats_status_check;
ALTER TABLE seats ADD CHECK (status IN ('AVAILABLE', 'BLOCKED'));
ALTER TABLE idempotency_records DROP CONSTRAINT idempotency_records_operation_check;
ALTER TABLE idempotency_records ADD CHECK (operation IN ('reserve'));

COMMIT;
