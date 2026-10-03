SELECT s.status, e.sales_open_at, e.starts_at
FROM seats s JOIN events e ON e.id = s.event_id AND e.tenant_id = s.tenant_id
WHERE s.id = $1 AND s.event_id = $2 AND e.status = 'PUBLISHED'
FOR UPDATE OF s;
