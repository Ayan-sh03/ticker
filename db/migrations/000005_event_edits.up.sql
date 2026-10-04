BEGIN;

ALTER TABLE events ADD COLUMN version bigint NOT NULL DEFAULT 1 CHECK (version > 0);
CREATE INDEX reservations_event_idx ON reservations (event_id);

CREATE FUNCTION bump_event_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.version := OLD.version + 1;
    RETURN NEW;
END;
$$;
CREATE TRIGGER events_version_bump BEFORE UPDATE ON events
    FOR EACH ROW EXECUTE FUNCTION bump_event_version();

-- A reservation takes this lock before its foreign-key check or any seat lock.
-- FOR SHARE also conflicts with ordinary event updates outside the organizer API.
CREATE FUNCTION lock_reservation_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM id FROM events WHERE tenant_id = NEW.tenant_id AND id = NEW.event_id FOR SHARE;
    RETURN NEW;
END;
$$;
CREATE TRIGGER reservations_event_lock BEFORE INSERT ON reservations
    FOR EACH ROW EXECUTE FUNCTION lock_reservation_event();

CREATE OR REPLACE FUNCTION guard_event_catalog() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF EXISTS (SELECT 1 FROM reservations WHERE event_id = OLD.id) THEN
            RAISE EXCEPTION 'events with reservation history cannot be deleted' USING ERRCODE = '23514';
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
        IF OLD.status = 'PUBLISHED' AND NEW.status <> 'PUBLISHED' THEN
            RAISE EXCEPTION 'published events cannot become drafts' USING ERRCODE = '23514';
        END IF;
        IF ROW(NEW.price_minor, NEW.currency, NEW.layout_rows, NEW.layout_columns)
            IS DISTINCT FROM ROW(OLD.price_minor, OLD.currency, OLD.layout_rows, OLD.layout_columns)
            AND EXISTS (SELECT 1 FROM reservations WHERE event_id = OLD.id) THEN
            RAISE EXCEPTION 'prices and layout are locked after the first reservation' USING ERRCODE = '23514';
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

    -- Serialize layout edits against publication and the first reservation.
    SELECT * INTO parent FROM events
    WHERE id = parent_id AND tenant_id = parent_tenant FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'seat tenant/event relationship does not exist' USING ERRCODE = '23503';
    END IF;
    IF EXISTS (SELECT 1 FROM reservations WHERE event_id = parent_id) THEN
        RAISE EXCEPTION 'seat layouts are locked after the first reservation' USING ERRCODE = '23514';
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
