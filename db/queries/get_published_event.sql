SELECT id::text, tenant_id::text, title, description, venue_name,
       price_minor, currency, sales_open_at, starts_at, layout_rows, layout_columns
FROM events
WHERE id = $1 AND status = 'PUBLISHED';
