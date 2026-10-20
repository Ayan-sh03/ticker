-- Every writer locks seats in ascending ID order, so overlapping requests cannot deadlock.
SELECT s.status
FROM seats s
WHERE s.event_id = $2 AND s.id = ANY($1::uuid[])
ORDER BY s.id
FOR UPDATE;
