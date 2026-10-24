-- Seat maps read only an event's open holds; sold seats come from seats.status.
CREATE INDEX reservations_event_held_idx ON reservations (event_id) WHERE status = 'HELD';
