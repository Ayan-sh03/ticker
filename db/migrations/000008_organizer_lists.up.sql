-- Organizer lists page through an event's bookings and holds, newest first.
CREATE INDEX bookings_event_idx ON bookings (tenant_id, event_id, created_at, id);
CREATE INDEX reservations_event_status_idx ON reservations (tenant_id, event_id, status, created_at, id);
