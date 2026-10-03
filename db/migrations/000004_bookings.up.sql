BEGIN;

ALTER TABLE reservations DROP CONSTRAINT reservations_status_check;
ALTER TABLE reservations ADD CHECK (status IN ('HELD', 'CANCELLED', 'EXPIRED', 'CONFIRMED'));
ALTER TABLE reservations ADD UNIQUE (tenant_id, event_id, id, user_id);
ALTER TABLE seats DROP CONSTRAINT seats_status_check;
ALTER TABLE seats ADD CHECK (status IN ('AVAILABLE', 'BLOCKED', 'SOLD'));
ALTER TABLE idempotency_records DROP CONSTRAINT idempotency_records_operation_check;
ALTER TABLE idempotency_records ADD CHECK (operation IN ('reserve', 'confirm'));
ALTER TABLE reservation_seats ADD CONSTRAINT reservation_seats_booking_reference UNIQUE (tenant_id, event_id, reservation_id, seat_id);

CREATE TABLE bookings (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL,
    event_id uuid NOT NULL,
    reservation_id uuid NOT NULL UNIQUE,
    user_id uuid NOT NULL,
    price_minor bigint NOT NULL CHECK (price_minor >= 0),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (tenant_id, event_id, reservation_id, user_id)
        REFERENCES reservations (tenant_id, event_id, id, user_id),
    UNIQUE (tenant_id, event_id, reservation_id, id)
);
CREATE INDEX bookings_user_idx ON bookings (user_id, created_at, id);

CREATE TABLE booking_seats (
    booking_id uuid NOT NULL,
    reservation_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    event_id uuid NOT NULL,
    seat_id uuid NOT NULL UNIQUE,
    PRIMARY KEY (booking_id, seat_id),
    FOREIGN KEY (tenant_id, event_id, reservation_id, booking_id)
        REFERENCES bookings (tenant_id, event_id, reservation_id, id),
    FOREIGN KEY (tenant_id, event_id, reservation_id, seat_id)
        REFERENCES reservation_seats (tenant_id, event_id, reservation_id, seat_id)
);

COMMIT;
