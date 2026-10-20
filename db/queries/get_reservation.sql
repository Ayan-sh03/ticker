SELECT r.id::text, r.event_id::text,
       ARRAY(SELECT rs.seat_id::text FROM reservation_seats rs WHERE rs.reservation_id = r.id ORDER BY rs.seat_id),
       r.status, r.created_at, r.expires_at, clock_timestamp(), b.id::text, e.price_minor, e.currency
FROM reservations r
JOIN events e ON e.id = r.event_id AND e.tenant_id = r.tenant_id
LEFT JOIN bookings b ON b.reservation_id = r.id
WHERE r.id = $1 AND r.user_id = $2;
