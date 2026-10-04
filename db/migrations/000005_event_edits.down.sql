BEGIN;

DROP TRIGGER reservations_event_lock ON reservations;
DROP FUNCTION lock_reservation_event();
DROP TRIGGER events_version_bump ON events;
DROP FUNCTION bump_event_version();
DROP INDEX reservations_event_idx;
ALTER TABLE events DROP COLUMN version;

CREATE OR REPLACE FUNCTION guard_event_catalog() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF OLD.status = 'PUBLISHED' THEN
            RAISE EXCEPTION 'published events cannot be deleted' USING ERRCODE = '23514';
        END IF;
        RETURN OLD;
    END IF;

    IF TG_OP = 'INSERT' AND NEW.status <> 'DRAFT' THEN
        RAISE EXCEPTION 'events must be created as drafts' USING ERRCODE = '23514';
    END IF;

    IF TG_OP = 'UPDATE' THEN
        IF ROW(NEW.id, NEW.tenant_id) IS DISTINCT FROM ROW(OLD.id, OLD.tenant_id) THEN
            RAISE EXCEPTION 'event identity and tenant are immutable' USING ERRCODE = '23514';
        END IF;
        IF OLD.status = 'PUBLISHED' AND (
            NEW.status <> 'PUBLISHED' OR
            ROW(NEW.price_minor, NEW.currency, NEW.layout_rows, NEW.layout_columns)
                IS DISTINCT FROM ROW(OLD.price_minor, OLD.currency, OLD.layout_rows, OLD.layout_columns)
        ) THEN
            RAISE EXCEPTION 'published prices and layout are immutable' USING ERRCODE = '23514';
        END IF;

        IF EXISTS (
            SELECT 1 FROM seats WHERE event_id = NEW.id
            AND (row_number > NEW.layout_rows OR seat_number > NEW.layout_columns)
        ) THEN
            RAISE EXCEPTION 'layout dimensions exclude existing seats' USING ERRCODE = '23514';
        END IF;

        IF OLD.status = 'DRAFT' AND NEW.status = 'PUBLISHED' THEN
            IF NOT EXISTS (SELECT 1 FROM seats WHERE event_id = NEW.id) OR EXISTS (
                SELECT 1 FROM seats WHERE event_id = NEW.id GROUP BY section
                HAVING count(*) <> NEW.layout_rows::bigint * NEW.layout_columns
            ) THEN
                RAISE EXCEPTION 'every section must have a complete rectangular layout before publication'
                    USING ERRCODE = '23514';
            END IF;
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION guard_seat_layout() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    parent events%ROWTYPE;
    parent_id uuid;
    parent_tenant uuid;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF ROW(NEW.id, NEW.tenant_id, NEW.event_id) IS DISTINCT FROM ROW(OLD.id, OLD.tenant_id, OLD.event_id) THEN
            RAISE EXCEPTION 'seat identity, tenant, and event are immutable' USING ERRCODE = '23514';
        END IF;
        -- Availability changes do not take a parent event lock.
        IF ROW(NEW.section, NEW.row_number, NEW.seat_number, NEW.label)
            IS NOT DISTINCT FROM ROW(OLD.section, OLD.row_number, OLD.seat_number, OLD.label) THEN
            RETURN NEW;
        END IF;
    END IF;

    IF TG_OP = 'DELETE' THEN
        parent_id := OLD.event_id;
        parent_tenant := OLD.tenant_id;
    ELSE
        parent_id := NEW.event_id;
        parent_tenant := NEW.tenant_id;
    END IF;

    -- Serialize draft layout edits against publication, including concurrent inserts.
    SELECT * INTO parent FROM events
    WHERE id = parent_id AND tenant_id = parent_tenant FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'seat tenant/event relationship does not exist' USING ERRCODE = '23503';
    END IF;
    IF parent.status = 'PUBLISHED' THEN
        RAISE EXCEPTION 'published seat layouts are immutable' USING ERRCODE = '23514';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    IF NEW.row_number > parent.layout_rows OR NEW.seat_number > parent.layout_columns THEN
        RAISE EXCEPTION 'seat coordinates exceed the event layout' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

COMMIT;
