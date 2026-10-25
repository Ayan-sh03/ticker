BEGIN;

DROP TABLE event_interests;
DROP VIEW booking_revenue;
ALTER TABLE bookings DROP COLUMN fee_bps;
ALTER TABLE tenants DROP COLUMN time_zone, DROP COLUMN fee_bps;
DROP TABLE platform_settings;

COMMIT;
