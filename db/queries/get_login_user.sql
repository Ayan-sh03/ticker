SELECT u.id::text, u.email, u.display_name, coalesce(u.password_hash, ''),
       ARRAY(SELECT m.tenant_id::text FROM memberships m WHERE m.user_id = u.id ORDER BY m.tenant_id)
FROM users u WHERE u.email = $1;
