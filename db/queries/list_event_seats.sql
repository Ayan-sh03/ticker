SELECT s.id::text, s.section, s.row_number, s.seat_number, s.label, s.status
FROM seats s
JOIN events e ON e.id = s.event_id AND e.tenant_id = s.tenant_id
WHERE s.event_id = $1 AND e.status = 'PUBLISHED'
ORDER BY s.section, s.row_number, s.seat_number;
