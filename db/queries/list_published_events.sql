-- seats_available counts seats on sale now: available seats minus those in
-- open holds. A hold past its deadline counts as free, as on the seat map.
-- Page first: otherwise the correlated counts also run for OFFSET's skipped
-- events. Materialization bounds this work to the requested page.
WITH page AS MATERIALIZED (
    SELECT * FROM events
    WHERE status = 'PUBLISHED'
    ORDER BY starts_at, id
    LIMIT $1 OFFSET $2
)
SELECT e.id::text, e.tenant_id::text, e.title, e.description, e.venue_name,
       e.price_minor, e.currency, e.sales_open_at, e.starts_at, e.layout_rows, e.layout_columns,
       (SELECT count(*) FROM seats s
         WHERE s.tenant_id = e.tenant_id AND s.event_id = e.id AND s.status = 'AVAILABLE')
       - (SELECT count(*) FROM reservations r
           JOIN reservation_seats rs ON rs.reservation_id = r.id AND rs.released_at IS NULL
         WHERE r.event_id = e.id AND r.status = 'HELD' AND r.expires_at > clock_timestamp())
FROM page e
ORDER BY e.starts_at, e.id;
