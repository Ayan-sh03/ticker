package organizer

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Revenue sums the booking_revenue view, the one revenue definition: active
// bookings at their saved price and fee rate. Amounts are in minor units.
type Revenue struct {
	Bookings   int64 `json:"bookings"`
	SeatsSold  int64 `json:"seats_sold"`
	GrossMinor int64 `json:"gross_minor"`
	FeeMinor   int64 `json:"fee_minor"`
	NetMinor   int64 `json:"net_minor"`
}

type CurrencyRevenue struct {
	Currency string `json:"currency"`
	Revenue
}

// Capacity counts seats that are not blocked.
type EventSales struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	StartsAt   time.Time `json:"starts_at"`
	Currency   string    `json:"currency"`
	Capacity   int64     `json:"capacity"`
	Interested int64     `json:"interested"`
	Revenue
}

type TenantInsights struct {
	TimeZone string            `json:"time_zone"`
	FeeBps   int               `json:"fee_bps"`
	Totals   []CurrencyRevenue `json:"totals"`
	Events   []EventSales      `json:"events"`
}

// Funnel follows customers from interest to holds and their outcomes. Holds
// counts every reservation; Confirmed counts every booking, cancelled or not.
type Funnel struct {
	Interested        int64 `json:"interested"`
	Holds             int64 `json:"holds"`
	Confirmed         int64 `json:"confirmed"`
	Expired           int64 `json:"expired"`
	CancelledHolds    int64 `json:"cancelled_holds"`
	CancelledBookings int64 `json:"cancelled_bookings"`
}

type DayRevenue struct {
	Day       string `json:"day"`
	SeatsSold int64  `json:"seats_sold"`
	NetMinor  int64  `json:"net_minor"`
}

type SectionSales struct {
	Section    string `json:"section"`
	Capacity   int64  `json:"capacity"`
	SeatsSold  int64  `json:"seats_sold"`
	GrossMinor int64  `json:"gross_minor"`
}

type Count struct {
	Key   int   `json:"key"`
	Count int64 `json:"count"`
}

type EventInsights struct {
	TimeZone    string         `json:"time_zone"`
	Event       EventSales     `json:"event"`
	SalesOpenAt time.Time      `json:"sales_open_at"`
	Funnel      Funnel         `json:"funnel"`
	Daily       []DayRevenue   `json:"daily"`
	Sections    []SectionSales `json:"sections"`
	// Hours counts active bookings by hour of day in the tenant's time zone;
	// Opening counts them by hour since sales opened, for the first 24 hours.
	Hours   []Count `json:"hours"`
	Opening []Count `json:"opening"`
	// Seconds from sales opening to the first booking, and to the booking that
	// sold the last seat while the event is sold out.
	FirstBookingSeconds *float64 `json:"first_booking_seconds"`
	SoldOutSeconds      *float64 `json:"sold_out_seconds"`
}

// eventSales reads published events with their capacity, interest, and revenue.
const eventSales = `SELECT e.id::text, e.title, e.starts_at, e.currency,
	(SELECT count(*) FROM seats s WHERE s.tenant_id = e.tenant_id AND s.event_id = e.id AND s.status <> 'BLOCKED'),
	(SELECT count(*) FROM event_interests i WHERE i.event_id = e.id),
	r.bookings, coalesce(r.seats, 0), coalesce(r.gross, 0), coalesce(r.fee, 0), coalesce(r.net, 0)
	FROM events e CROSS JOIN LATERAL (SELECT count(*) AS bookings, sum(seats) AS seats, sum(gross_minor) AS gross,
		sum(fee_minor) AS fee, sum(net_minor) AS net FROM booking_revenue v WHERE v.tenant_id = e.tenant_id AND v.event_id = e.id) r
	WHERE e.tenant_id = $1 AND e.status = 'PUBLISHED' `

func scanEventSales(row pgx.CollectableRow) (EventSales, error) {
	var e EventSales
	err := row.Scan(&e.ID, &e.Title, &e.StartsAt, &e.Currency, &e.Capacity, &e.Interested,
		&e.Bookings, &e.SeatsSold, &e.GrossMinor, &e.FeeMinor, &e.NetMinor)
	return e, err
}

// TenantInsights returns revenue per currency and for the 100 latest published
// events, from one snapshot.
func (s *Store) TenantInsights(ctx context.Context, userID, tenantID string) (TenantInsights, error) {
	tx, err := s.beginAuthorizedWith(ctx, userID, tenantID, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return TenantInsights{}, err
	}
	defer tx.Rollback(context.Background())
	var out TenantInsights
	if err := tx.QueryRow(ctx, `SELECT t.time_zone, coalesce(t.fee_bps, p.fee_bps) FROM tenants t, platform_settings p WHERE t.id = $1`,
		tenantID).Scan(&out.TimeZone, &out.FeeBps); err != nil {
		return TenantInsights{}, err
	}
	rows, err := tx.Query(ctx, `SELECT currency, count(*), sum(seats), sum(gross_minor), sum(fee_minor), sum(net_minor)
		FROM booking_revenue WHERE tenant_id = $1 GROUP BY currency ORDER BY currency`, tenantID)
	if err != nil {
		return TenantInsights{}, err
	}
	if out.Totals, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (CurrencyRevenue, error) {
		var c CurrencyRevenue
		err := row.Scan(&c.Currency, &c.Bookings, &c.SeatsSold, &c.GrossMinor, &c.FeeMinor, &c.NetMinor)
		return c, err
	}); err != nil {
		return TenantInsights{}, err
	}
	rows, err = tx.Query(ctx, eventSales+`ORDER BY e.starts_at DESC, e.id LIMIT 100`, tenantID)
	if err != nil {
		return TenantInsights{}, err
	}
	if out.Events, err = pgx.CollectRows(rows, scanEventSales); err != nil {
		return TenantInsights{}, err
	}
	return out, tx.Commit(ctx)
}

// EventInsights returns one published event's sales, funnel, and timing, from
// one snapshot. Days and hours use the tenant's time zone.
func (s *Store) EventInsights(ctx context.Context, userID, tenantID, eventID string) (EventInsights, error) {
	tx, err := s.beginAuthorizedWith(ctx, userID, tenantID, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return EventInsights{}, err
	}
	defer tx.Rollback(context.Background())
	var out EventInsights
	rows, err := tx.Query(ctx, eventSales+`AND e.id = $2`, tenantID, eventID)
	if err != nil {
		return EventInsights{}, err
	}
	out.Event, err = pgx.CollectExactlyOneRow(rows, scanEventSales)
	if errors.Is(err, pgx.ErrNoRows) {
		return EventInsights{}, ErrNotFound
	}
	if err != nil {
		return EventInsights{}, err
	}
	// One database time, so a hold cannot count as both open and expired.
	if err := tx.QueryRow(ctx, `SELECT t.time_zone, e.sales_open_at,
		(SELECT count(*) FROM event_interests WHERE event_id = e.id),
		(SELECT count(*) FROM reservations WHERE tenant_id = e.tenant_id AND event_id = e.id),
		(SELECT count(*) FROM bookings WHERE tenant_id = e.tenant_id AND event_id = e.id),
		(SELECT count(*) FROM reservations WHERE tenant_id = e.tenant_id AND event_id = e.id
			AND (status = 'EXPIRED' OR (status = 'HELD' AND expires_at <= clock_timestamp()))),
		(SELECT count(*) FROM reservations r WHERE r.tenant_id = e.tenant_id AND r.event_id = e.id AND r.status = 'CANCELLED'
			AND NOT EXISTS (SELECT 1 FROM bookings b WHERE b.reservation_id = r.id)),
		(SELECT count(*) FROM bookings WHERE tenant_id = e.tenant_id AND event_id = e.id AND cancelled_at IS NOT NULL),
		(SELECT extract(epoch FROM min(created_at) - e.sales_open_at)::float8 FROM bookings WHERE tenant_id = e.tenant_id AND event_id = e.id)
		FROM events e JOIN tenants t ON t.id = e.tenant_id WHERE e.tenant_id = $1 AND e.id = $2`, tenantID, eventID).Scan(
		&out.TimeZone, &out.SalesOpenAt, &out.Funnel.Interested, &out.Funnel.Holds, &out.Funnel.Confirmed, &out.Funnel.Expired,
		&out.Funnel.CancelledHolds, &out.Funnel.CancelledBookings, &out.FirstBookingSeconds); err != nil {
		return EventInsights{}, err
	}
	if out.Event.Capacity > 0 && out.Event.SeatsSold == out.Event.Capacity {
		if err := tx.QueryRow(ctx, `SELECT extract(epoch FROM max(created_at) - $3::timestamptz)::float8 FROM booking_revenue
			WHERE tenant_id = $1 AND event_id = $2`, tenantID, eventID, out.SalesOpenAt).Scan(&out.SoldOutSeconds); err != nil {
			return EventInsights{}, err
		}
	}
	rows, err = tx.Query(ctx, `SELECT to_char(created_at AT TIME ZONE $3, 'YYYY-MM-DD') AS day, sum(seats), sum(net_minor)
		FROM booking_revenue WHERE tenant_id = $1 AND event_id = $2 GROUP BY day ORDER BY day`, tenantID, eventID, out.TimeZone)
	if err != nil {
		return EventInsights{}, err
	}
	if out.Daily, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (DayRevenue, error) {
		var d DayRevenue
		err := row.Scan(&d.Day, &d.SeatsSold, &d.NetMinor)
		return d, err
	}); err != nil {
		return EventInsights{}, err
	}
	rows, err = tx.Query(ctx, `SELECT s.section, count(*) FILTER (WHERE s.status <> 'BLOCKED'), count(b.id), coalesce(sum(b.price_minor), 0)
		FROM seats s LEFT JOIN booking_seats bs ON bs.seat_id = s.id AND bs.released_at IS NULL
		LEFT JOIN bookings b ON b.id = bs.booking_id
		WHERE s.tenant_id = $1 AND s.event_id = $2 GROUP BY s.section ORDER BY s.section`, tenantID, eventID)
	if err != nil {
		return EventInsights{}, err
	}
	if out.Sections, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (SectionSales, error) {
		var s SectionSales
		err := row.Scan(&s.Section, &s.Capacity, &s.SeatsSold, &s.GrossMinor)
		return s, err
	}); err != nil {
		return EventInsights{}, err
	}
	counts := func(key string, args ...any) ([]Count, error) {
		rows, err := tx.Query(ctx, `SELECT `+key+` AS key, count(*) FROM booking_revenue
			WHERE tenant_id = $1 AND event_id = $2 AND ($4::timestamptz IS NULL OR created_at < $4) GROUP BY key ORDER BY key`, args...)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, pgx.RowToStructByPos[Count])
	}
	if out.Hours, err = counts(`extract(hour FROM created_at AT TIME ZONE $3)::int`, tenantID, eventID, out.TimeZone, nil); err != nil {
		return EventInsights{}, err
	}
	if out.Opening, err = counts(`floor(extract(epoch FROM created_at - $3::timestamptz) / 3600)::int`,
		tenantID, eventID, out.SalesOpenAt, out.SalesOpenAt.Add(24*time.Hour)); err != nil {
		return EventInsights{}, err
	}
	return out, tx.Commit(ctx)
}
