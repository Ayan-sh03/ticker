-- A tenant's time zone must be a named zone, such as Asia/Kolkata, so day and
-- hour buckets never fail. Offsets like GMT+5 are rejected.
CREATE FUNCTION check_tenant_time_zone() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_timezone_names WHERE name = NEW.time_zone) THEN
        RAISE EXCEPTION 'unknown time zone: %', NEW.time_zone USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER tenants_time_zone_check BEFORE INSERT OR UPDATE OF time_zone ON tenants
    FOR EACH ROW EXECUTE FUNCTION check_tenant_time_zone();
