SELECT r.id::text, r.event_id::text, rs.seat_id::text, r.status,
       r.created_at, r.expires_at, clock_timestamp(), b.id::text
FROM reservations r JOIN reservation_seats rs ON rs.reservation_id = r.id
LEFT JOIN bookings b ON b.reservation_id = r.id
WHERE r.id = $1 AND r.user_id = $2;
