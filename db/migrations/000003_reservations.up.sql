BEGIN;

CREATE TABLE reservations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL,
    event_id uuid NOT NULL,
    user_id uuid NOT NULL REFERENCES users(id),
    status text NOT NULL DEFAULT 'HELD' CHECK (status IN ('HELD', 'CANCELLED', 'EXPIRED')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at),
    FOREIGN KEY (tenant_id, event_id) REFERENCES events (tenant_id, id),
    UNIQUE (tenant_id, event_id, id)
);
CREATE INDEX reservations_deadline_idx ON reservations (expires_at, id) WHERE status = 'HELD';
CREATE INDEX reservations_user_idx ON reservations (user_id, created_at, id);

CREATE TABLE reservation_seats (
    reservation_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    event_id uuid NOT NULL,
    seat_id uuid NOT NULL,
    released_at timestamptz,
    PRIMARY KEY (reservation_id, seat_id),
    FOREIGN KEY (tenant_id, event_id, reservation_id) REFERENCES reservations (tenant_id, event_id, id),
    FOREIGN KEY (tenant_id, event_id, seat_id) REFERENCES seats (tenant_id, event_id, id)
);
-- An overdue hold keeps ownership until its release transaction commits.
CREATE UNIQUE INDEX reservation_seats_owner_idx ON reservation_seats (seat_id) WHERE released_at IS NULL;

CREATE TABLE idempotency_records (
    user_id uuid NOT NULL REFERENCES users(id),
    operation text NOT NULL CHECK (operation IN ('reserve')),
    key text NOT NULL CHECK (length(key) BETWEEN 1 AND 128),
    payload_hash bytea NOT NULL CHECK (octet_length(payload_hash) = 32),
    response_status integer CHECK (response_status IN (201, 404, 409)),
    response_body bytea,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (user_id, operation, key),
    CHECK ((response_status IS NULL) = (response_body IS NULL))
);

COMMIT;
