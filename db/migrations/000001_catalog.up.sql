BEGIN;

CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email text NOT NULL UNIQUE CHECK (email = lower(btrim(email)) AND email <> ''),
    display_name text NOT NULL CHECK (btrim(display_name) <> ''),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE tenants (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    name text NOT NULL CHECK (btrim(name) <> ''),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- Membership grants organizer access. Customers do not need a tenant membership.
CREATE TABLE memberships (
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    user_id uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, user_id)
);
CREATE INDEX memberships_user_idx ON memberships (user_id, tenant_id);

CREATE TABLE events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    title text NOT NULL CHECK (btrim(title) <> ''),
    description text NOT NULL DEFAULT '',
    venue_name text NOT NULL CHECK (btrim(venue_name) <> ''),
    status text NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT', 'PUBLISHED')),
    price_minor bigint NOT NULL CHECK (price_minor >= 0),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    sales_open_at timestamptz NOT NULL,
    starts_at timestamptz NOT NULL,
    layout_rows integer NOT NULL CHECK (layout_rows BETWEEN 1 AND 100),
    layout_columns integer NOT NULL CHECK (layout_columns BETWEEN 1 AND 100),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (tenant_id, id),
    CHECK (sales_open_at < starts_at)
);
CREATE INDEX events_published_idx ON events (starts_at, id) WHERE status = 'PUBLISHED';

CREATE TABLE seats (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL,
    event_id uuid NOT NULL,
    section text NOT NULL CHECK (btrim(section) <> ''),
    row_number integer NOT NULL CHECK (row_number > 0),
    seat_number integer NOT NULL CHECK (seat_number > 0),
    label text NOT NULL CHECK (btrim(label) <> ''),
    status text NOT NULL DEFAULT 'AVAILABLE' CHECK (status IN ('AVAILABLE', 'BLOCKED')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (tenant_id, event_id) REFERENCES events (tenant_id, id),
    UNIQUE (tenant_id, event_id, id),
    UNIQUE (event_id, label),
    UNIQUE (event_id, section, row_number, seat_number)
);

CREATE FUNCTION guard_event_catalog() RETURNS trigger LANGUAGE plpgsql AS $$
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

CREATE TRIGGER events_catalog_guard BEFORE INSERT OR UPDATE OR DELETE ON events
    FOR EACH ROW EXECUTE FUNCTION guard_event_catalog();

CREATE FUNCTION guard_seat_layout() RETURNS trigger LANGUAGE plpgsql AS $$
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

CREATE TRIGGER seats_layout_guard BEFORE INSERT OR UPDATE OR DELETE ON seats
    FOR EACH ROW EXECUTE FUNCTION guard_seat_layout();

COMMIT;
