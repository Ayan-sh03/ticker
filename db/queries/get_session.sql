SELECT u.id::text, u.email, u.display_name,
       ARRAY(SELECT m.tenant_id::text FROM memberships m WHERE m.user_id = u.id ORDER BY m.tenant_id),
       s.expires_at
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1 AND s.expires_at > clock_timestamp();
