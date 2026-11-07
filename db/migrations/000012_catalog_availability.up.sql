-- Cover the catalog's AVAILABLE count without fetching every seat's heap row.
-- Holds stay in reservations; sold and blocked seats leave this partial index.
CREATE INDEX seats_available_event_idx ON seats (tenant_id, event_id)
    WHERE status = 'AVAILABLE';
