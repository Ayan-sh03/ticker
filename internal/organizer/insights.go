package organizer

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Revenue sums the booking_revenue view, the one revenue definition: active
// bookings at their saved price and fee rate. Amounts are in minor units, as
// decimal strings, because sums can exceed both int64 and JavaScript numbers.
type Revenue struct {
	Bookings   int64  `json:"bookings"`
	SeatsSold  int64  `json:"seats_sold"`
	GrossMinor string `json:"gross_minor"`
	FeeMinor   string `json:"fee_minor"`
	NetMinor   string `json:"net_minor"`
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

// Events lists at most eventLimit published events, latest start first;
// EventCount says how many there are in all.
type TenantInsights struct {
	TimeZone   string            `json:"time_zone"`
	FeeBps     int               `json:"fee_bps"`
	Totals     []CurrencyRevenue `json:"totals"`
	Events     []EventSales      `json:"events"`
	EventCount int64             `json:"event_count"`
}

const eventLimit = 100

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
	NetMinor  string `json:"net_minor"`
}

type SectionSales struct {
	Section    string `json:"section"`
	Capacity   int64  `json:"capacity"`
	SeatsSold  int64  `json:"seats_sold"`
	GrossMinor string `json:"gross_minor"`
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
	// Opening counts them by hour in the first 24 hours after sales opened.
	Hours   []Count `json:"hours"`
	Opening []Count `json:"opening"`
	// Timing is measured from the current sales opening; bookings made before
	// it, when the opening was moved later, do not count. Sell-out is known only
	// while no seat is blocked, because blocking shrinks capacity after the fact.
	FirstBookingSeconds *float64 `json:"first_booking_seconds"`
	SoldOutSeconds      *float64 `json:"sold_out_seconds"`
}

// eventSales reads published events with their capacity, interest, and revenue.
const eventSales = `SELECT e.id::text, e.title, e.starts_at, e.currency,
	(SELECT count(*) FROM seats s WHERE s.tenant_id = e.tenant_id AND s.event_id = e.id AND s.status <> 'BLOCKED'),
	(SELECT count(*) FROM event_interests i WHERE i.event_id = e.id),
	r.bookings, coalesce(r.seats, 0), coalesce(r.gross, 0)::text, coalesce(r.fee, 0)::text, coalesce(r.net, 0)::text
	FROM events e CROSS JOIN LATERAL (SELECT count(*) AS bookings, sum(seats) AS seats, sum(gross_minor) AS gross,
		sum(fee_minor) AS fee, sum(net_minor) AS net FROM booking_revenue v WHERE v.tenant_id = e.tenant_id AND v.event_id = e.id) r
	WHERE e.tenant_id = $1 AND e.status = 'PUBLISHED' `

func scanEventSales(row pgx.CollectableRow) (EventSales, error) {
	var e EventSales
	err := row.Scan(&e.ID, &e.Title, &e.StartsAt, &e.Currency, &e.Capacity, &e.Interested,
		&e.Bookings, &e.SeatsSold, &e.GrossMinor, &e.FeeMinor, &e.NetMinor)
	return e, err
}

// TenantInsights returns revenue per currency and for the latest published
// events, from one snapshot.
func (s *Store) TenantInsights(ctx context.Context, userID, tenantID string) (TenantInsights, error) {
	tx, err := s.beginAuthorizedWith(ctx, userID, tenantID, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return TenantInsights{}, err
	}
	defer tx.Rollback(context.Background())
	var out TenantInsights
	if err := tx.QueryRow(ctx, `SELECT t.time_zone, coalesce(t.fee_bps, p.fee_bps),
		(SELECT count(*) FROM events WHERE tenant_id = t.id AND status = 'PUBLISHED')
		FROM tenants t, platform_settings p WHERE t.id = $1`, tenantID).Scan(&out.TimeZone, &out.FeeBps, &out.EventCount); err != nil {
		return TenantInsights{}, err
	}
	rows, err := tx.Query(ctx, `SELECT currency, count(*), sum(seats), sum(gross_minor)::text, sum(fee_minor)::text, sum(net_minor)::text
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
	rows, err = tx.Query(ctx, eventSales+`ORDER BY e.starts_at DESC, e.id LIMIT $2`, tenantID, eventLimit)
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
	// Read event metadata without a separate revenue scan. The grouped query
	// below computes all active-booking totals and chart series together.
	err = tx.QueryRow(ctx, `SELECT e.id::text, e.title, e.starts_at, e.currency,
		(SELECT count(*) FROM seats s WHERE s.tenant_id = e.tenant_id AND s.event_id = e.id AND s.status <> 'BLOCKED'),
		(SELECT count(*) FROM event_interests i WHERE i.event_id = e.id)
		FROM events e WHERE e.tenant_id = $1 AND e.id = $2 AND e.status = 'PUBLISHED'`, tenantID, eventID).Scan(
		&out.Event.ID, &out.Event.Title, &out.Event.StartsAt, &out.Event.Currency, &out.Event.Capacity, &out.Event.Interested)
	if errors.Is(err, pgx.ErrNoRows) {
		return EventInsights{}, ErrNotFound
	}
	if err != nil {
		return EventInsights{}, err
	}
	out.Funnel.Interested = out.Event.Interested
	var blocked bool
	// One database time, so a hold cannot count as both open and expired.
	if err := tx.QueryRow(ctx, `SELECT t.time_zone, e.sales_open_at,
		(SELECT count(*) FROM reservations WHERE tenant_id = e.tenant_id AND event_id = e.id),
		(SELECT count(*) FROM bookings WHERE tenant_id = e.tenant_id AND event_id = e.id),
		(SELECT count(*) FROM reservations WHERE tenant_id = e.tenant_id AND event_id = e.id
			AND (status = 'EXPIRED' OR (status = 'HELD' AND expires_at <= clock_timestamp()))),
		(SELECT count(*) FROM reservations r WHERE r.tenant_id = e.tenant_id AND r.event_id = e.id AND r.status = 'CANCELLED'
			AND NOT EXISTS (SELECT 1 FROM bookings b WHERE b.reservation_id = r.id)),
		(SELECT count(*) FROM bookings WHERE tenant_id = e.tenant_id AND event_id = e.id AND cancelled_at IS NOT NULL),
		(SELECT extract(epoch FROM min(created_at) - e.sales_open_at)::float8 FROM bookings
			WHERE tenant_id = e.tenant_id AND event_id = e.id AND created_at >= e.sales_open_at),
		EXISTS (SELECT 1 FROM seats WHERE tenant_id = e.tenant_id AND event_id = e.id AND status = 'BLOCKED')
		FROM events e JOIN tenants t ON t.id = e.tenant_id WHERE e.tenant_id = $1 AND e.id = $2`, tenantID, eventID).Scan(
		&out.TimeZone, &out.SalesOpenAt, &out.Funnel.Holds, &out.Funnel.Confirmed, &out.Funnel.Expired,
		&out.Funnel.CancelledHolds, &out.Funnel.CancelledBookings, &out.FirstBookingSeconds, &blocked); err != nil {
		return EventInsights{}, err
	}
	// GROUPING SETS shares the booking_revenue scan while preserving its
	// per-booking fee rounding, exact sums and the tenant's local day/hour.
	// The opening series has an absolute 24-hour window, including across DST.
	rows, err := tx.Query(ctx, `WITH sales AS (
		SELECT seats, gross_minor, fee_minor, net_minor,
			to_char(created_at AT TIME ZONE $3, 'YYYY-MM-DD') AS day,
			extract(hour FROM created_at AT TIME ZONE $3)::int AS hour,
			CASE WHEN created_at >= $4::timestamptz AND created_at < $4::timestamptz + interval '24 hours'
				THEN floor(extract(epoch FROM created_at - $4::timestamptz) / 3600)::int END AS opening_hour,
			CASE WHEN created_at >= $4::timestamptz
				THEN extract(epoch FROM created_at - $4::timestamptz)::float8 END AS since_open
		FROM booking_revenue WHERE tenant_id = $1 AND event_id = $2
	)
	SELECT grouping(day, hour, opening_hour), day, hour, opening_hour,
		count(*), coalesce(sum(seats), 0), coalesce(sum(gross_minor), 0)::text,
		coalesce(sum(fee_minor), 0)::text, coalesce(sum(net_minor), 0)::text, max(since_open)
	FROM sales GROUP BY GROUPING SETS ((), (day), (hour), (opening_hour))
	ORDER BY day, hour, opening_hour`, tenantID, eventID, out.TimeZone, out.SalesOpenAt)
	if err != nil {
		return EventInsights{}, err
	}
	defer rows.Close()
	out.Daily = []DayRevenue{}
	out.Hours = []Count{}
	out.Opening = []Count{}
	for rows.Next() {
		var grouping int
		var day *string
		var hour, openingHour *int
		var revenue Revenue
		var sinceOpen *float64
		if err := rows.Scan(&grouping, &day, &hour, &openingHour,
			&revenue.Bookings, &revenue.SeatsSold, &revenue.GrossMinor, &revenue.FeeMinor, &revenue.NetMinor, &sinceOpen); err != nil {
			return EventInsights{}, err
		}
		// GROUPING marks omitted keys with a bit: day=4, hour=2, opening=1.
		switch grouping {
		case 7:
			out.Event.Revenue = revenue
			if !blocked && out.Event.Capacity > 0 && revenue.SeatsSold == out.Event.Capacity {
				out.SoldOutSeconds = sinceOpen
			}
		case 3:
			out.Daily = append(out.Daily, DayRevenue{Day: *day, SeatsSold: revenue.SeatsSold, NetMinor: revenue.NetMinor})
		case 5:
			out.Hours = append(out.Hours, Count{Key: *hour, Count: revenue.Bookings})
		case 6:
			// Rows outside the opening window form a NULL group, not a bar.
			if openingHour != nil {
				out.Opening = append(out.Opening, Count{Key: *openingHour, Count: revenue.Bookings})
			}
		}
	}
	if err := rows.Err(); err != nil {
		return EventInsights{}, err
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT s.section, count(*) FILTER (WHERE s.status <> 'BLOCKED'), count(b.id), coalesce(sum(b.price_minor), 0)::text
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
	return out, tx.Commit(ctx)
}
