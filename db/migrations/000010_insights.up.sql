BEGIN;

-- One platform fee rate in basis points, with an optional rate per tenant. Each
-- booking keeps the rate it was charged, so a rate change affects only new bookings.
CREATE TABLE platform_settings (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    fee_bps integer NOT NULL CHECK (fee_bps BETWEEN 0 AND 10000)
);
INSERT INTO platform_settings (fee_bps) VALUES (500);
ALTER TABLE tenants
    ADD COLUMN fee_bps integer CHECK (fee_bps BETWEEN 0 AND 10000),
    ADD COLUMN time_zone text NOT NULL DEFAULT 'UTC';
ALTER TABLE bookings ADD COLUMN fee_bps integer CHECK (fee_bps BETWEEN 0 AND 10000);
UPDATE bookings b SET fee_bps = coalesce(t.fee_bps, (SELECT fee_bps FROM platform_settings))
FROM tenants t WHERE t.id = b.tenant_id;
ALTER TABLE bookings ALTER COLUMN fee_bps SET NOT NULL;

-- The one revenue definition: active bookings at the price and fee rate saved
-- on each booking. The fee rounds half up to a whole minor unit.
CREATE VIEW booking_revenue AS
SELECT b.id AS booking_id, b.tenant_id, b.event_id, b.currency, b.created_at, b.fee_bps, s.seats,
    b.price_minor * s.seats AS gross_minor,
    round((b.price_minor * s.seats)::numeric * b.fee_bps / 10000)::bigint AS fee_minor,
    b.price_minor * s.seats - round((b.price_minor * s.seats)::numeric * b.fee_bps / 10000)::bigint AS net_minor
FROM bookings b
CROSS JOIN LATERAL (SELECT count(*) AS seats FROM booking_seats bs WHERE bs.booking_id = b.id) s
WHERE b.cancelled_at IS NULL;

-- Signed-in customers mark interest in an event. Organizers see only the count.
CREATE TABLE event_interests (
    tenant_id uuid NOT NULL,
    event_id uuid NOT NULL,
    user_id uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (event_id, user_id),
    FOREIGN KEY (tenant_id, event_id) REFERENCES events (tenant_id, id) ON DELETE CASCADE
);

COMMIT;
