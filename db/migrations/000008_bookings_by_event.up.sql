-- Organizer booking lists page through an event's bookings, newest first.
CREATE INDEX bookings_event_idx ON bookings (tenant_id, event_id, created_at, id);
