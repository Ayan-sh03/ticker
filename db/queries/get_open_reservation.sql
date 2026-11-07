-- The caller's newest open hold for one event. Overdue holds are not open.
SELECT r.id::text, r.event_id::text,
       ARRAY(SELECT rs.seat_id::text FROM reservation_seats rs WHERE rs.reservation_id = r.id ORDER BY rs.seat_id),
       r.status, r.created_at, r.expires_at, clock_timestamp(), NULL::text, e.price_minor, e.currency
FROM reservations r
JOIN events e ON e.id = r.event_id AND e.tenant_id = r.tenant_id
WHERE r.user_id = $1 AND r.event_id = $2 AND r.status = 'HELD' AND r.expires_at > clock_timestamp()
ORDER BY r.created_at DESC, r.id DESC
LIMIT 1;
