SELECT s.id::text, s.section, s.row_number, s.seat_number, s.label,
       CASE WHEN s.status = 'SOLD' THEN 'SOLD'
            WHEN rs.seat_id IS NOT NULL THEN 'HELD' ELSE s.status END
FROM seats s
JOIN events e ON e.id = s.event_id AND e.tenant_id = s.tenant_id
LEFT JOIN reservation_seats rs ON rs.seat_id = s.id AND rs.released_at IS NULL
WHERE s.event_id = $1 AND e.status = 'PUBLISHED'
ORDER BY s.section, s.row_number, s.seat_number;
