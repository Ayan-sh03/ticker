package organizer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrInvalidCursor = errors.New("invalid page cursor")

type Customer struct {
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

// Booking is one purchase as an organizer sees it: CONFIRMED, or CANCELLED by
// an organizer. PriceMinor is the per-seat price saved on the booking.
type Booking struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	CancelledAt *time.Time `json:"cancelled_at"`
	Customer    Customer   `json:"customer"`
	SeatLabels  []string   `json:"seat_labels"`
	PriceMinor  int64      `json:"price_minor"`
	TotalMinor  int64      `json:"total_minor"`
	Currency    string     `json:"currency"`
}

// Hold is a reservation that never became a booking, or one still open.
// EndedAt is when it expired or was cancelled; holds from before the audit
// trail began have none.
type Hold struct {
	ID         string     `json:"id"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	EndedAt    *time.Time `json:"ended_at"`
	Customer   Customer   `json:"customer"`
	SeatLabels []string   `json:"seat_labels"`
}

// Bookings and SeatsSold count active bookings only.
type SalesSummary struct {
	Bookings          int64 `json:"bookings"`
	CancelledBookings int64 `json:"cancelled_bookings"`
	SeatsSold         int64 `json:"seats_sold"`
	OpenHolds         int64 `json:"open_holds"`
	ExpiredHolds      int64 `json:"expired_holds"`
	CancelledHolds    int64 `json:"cancelled_holds"`
}

// Cursor marks the last row of a page; the next page starts after it. Pages run
// newest first by (created_at, id), so rows added or changed meanwhile never
// shift later pages.
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

// ParseCursor reads "<RFC 3339 time>~<uuid>". An empty value means the first page.
func ParseCursor(value string) (*Cursor, error) {
	if value == "" {
		return nil, nil
	}
	at, id, ok := strings.Cut(value, "~")
	var uuid pgtype.UUID
	createdAt, err := time.Parse(time.RFC3339Nano, at)
	if !ok || err != nil || len(id) != 36 || uuid.Scan(id) != nil {
		return nil, ErrInvalidCursor
	}
	return &Cursor{CreatedAt: createdAt, ID: uuid.String()}, nil
}

func (c Cursor) String() string { return c.CreatedAt.UTC().Format(time.RFC3339Nano) + "~" + c.ID }

// HoldStates lists the hold filters. An overdue hold reads as expired before it
// is released, as customers see it.
var HoldStates = map[string]bool{"open": true, "expired": true, "cancelled": true}

// ListBookings returns one page of the event's bookings, newest first, and the
// event's totals, both from one snapshot.
func (s *Store) ListBookings(ctx context.Context, userID, tenantID, eventID string, limit int, after *Cursor) (SalesSummary, []Booking, *Cursor, error) {
	tx, now, err := s.beginSnapshot(ctx, userID, tenantID, eventID)
	if err != nil {
		return SalesSummary{}, nil, nil, err
	}
	defer tx.Rollback(context.Background())
	summary, err := salesSummary(ctx, tx, tenantID, eventID, now)
	if err != nil {
		return SalesSummary{}, nil, nil, err
	}
	bookings, err := readBookings(ctx, tx, `WHERE b.tenant_id = $1 AND b.event_id = $2
		AND ($4::timestamptz IS NULL OR (b.created_at, b.id) < ($4, $5::uuid))
		ORDER BY b.created_at DESC, b.id DESC LIMIT $3`, tenantID, eventID, limit, cursorTime(after), cursorID(after))
	if err != nil {
		return SalesSummary{}, nil, nil, err
	}
	var next *Cursor
	if len(bookings) == limit {
		last := bookings[len(bookings)-1]
		next = &Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return summary, bookings, next, tx.Commit(ctx)
}

// ListHolds returns one page of the event's holds in one state, newest first,
// and the event's totals, both from one snapshot.
func (s *Store) ListHolds(ctx context.Context, userID, tenantID, eventID, state string, limit int, after *Cursor) (SalesSummary, []Hold, *Cursor, error) {
	if !HoldStates[state] {
		return SalesSummary{}, nil, nil, ErrInvalidEvent
	}
	tx, now, err := s.beginSnapshot(ctx, userID, tenantID, eventID)
	if err != nil {
		return SalesSummary{}, nil, nil, err
	}
	defer tx.Rollback(context.Background())
	summary, err := salesSummary(ctx, tx, tenantID, eventID, now)
	if err != nil {
		return SalesSummary{}, nil, nil, err
	}
	// Each branch walks reservations_event_status_idx in page order. Expired
	// holds are released ones plus overdue ones not yet released. A cancelled
	// booking's reservation is CANCELLED too, but it is listed as a booking.
	page := `SELECT id, created_at FROM reservations
		WHERE tenant_id = $1 AND event_id = $2 AND status = '%s' AND %s
		AND ($4::timestamptz IS NULL OR (created_at, id) < ($4, $5::uuid))
		ORDER BY created_at DESC, id DESC LIMIT $3`
	var ids string
	switch state {
	case "open":
		ids = fmt.Sprintf(page, "HELD", "expires_at > $6")
	case "expired":
		ids = "(" + fmt.Sprintf(page, "EXPIRED", "true") + ") UNION ALL (" + fmt.Sprintf(page, "HELD", "expires_at <= $6") + ")"
	case "cancelled":
		ids = fmt.Sprintf(page, "CANCELLED", "NOT EXISTS (SELECT 1 FROM bookings b WHERE b.reservation_id = reservations.id)")
	}
	rows, err := tx.Query(ctx, `WITH page AS (`+ids+`)
		SELECT r.id::text, CASE WHEN r.status = 'HELD' AND r.expires_at <= $6 THEN 'EXPIRED' ELSE r.status END,
		r.created_at, r.expires_at,
		(SELECT max(a.created_at) FROM audit_entries a WHERE a.reservation_id = r.id AND a.to_status IN ('EXPIRED', 'CANCELLED')),
		u.display_name, u.email,
		ARRAY(SELECT s.label FROM reservation_seats rs JOIN seats s ON s.id = rs.seat_id
			WHERE rs.reservation_id = r.id ORDER BY s.section, s.row_number, s.seat_number)
		FROM page JOIN reservations r ON r.id = page.id JOIN users u ON u.id = r.user_id
		ORDER BY r.created_at DESC, r.id DESC LIMIT $3`,
		tenantID, eventID, limit, cursorTime(after), cursorID(after), now)
	if err != nil {
		return SalesSummary{}, nil, nil, err
	}
	holds, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Hold, error) {
		var h Hold
		err := row.Scan(&h.ID, &h.Status, &h.CreatedAt, &h.ExpiresAt, &h.EndedAt, &h.Customer.DisplayName, &h.Customer.Email, &h.SeatLabels)
		return h, err
	})
	if err != nil {
		return SalesSummary{}, nil, nil, err
	}
	var next *Cursor
	if len(holds) == limit {
		last := holds[len(holds)-1]
		next = &Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return summary, holds, next, tx.Commit(ctx)
}

// beginSnapshot authorizes the organizer and checks the event inside one
// REPEATABLE READ transaction, so totals and rows agree. It also returns one
// database time, so a hold cannot count as both open and expired.
func (s *Store) beginSnapshot(ctx context.Context, userID, tenantID, eventID string) (pgx.Tx, time.Time, error) {
	tx, err := s.beginAuthorizedWith(ctx, userID, tenantID, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, time.Time{}, err
	}
	var exists bool
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM events WHERE tenant_id = $1 AND id = $2), clock_timestamp()`,
		tenantID, eventID).Scan(&exists, &now); err != nil {
		_ = tx.Rollback(context.Background())
		return nil, time.Time{}, err
	}
	if !exists {
		_ = tx.Rollback(context.Background())
		return nil, time.Time{}, ErrNotFound
	}
	return tx, now, nil
}

func salesSummary(ctx context.Context, tx pgx.Tx, tenantID, eventID string, now time.Time) (SalesSummary, error) {
	var s SalesSummary
	err := tx.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM bookings WHERE tenant_id = $1 AND event_id = $2 AND cancelled_at IS NULL),
		(SELECT count(*) FROM bookings WHERE tenant_id = $1 AND event_id = $2 AND cancelled_at IS NOT NULL),
		(SELECT count(*) FROM bookings b JOIN booking_seats bs ON bs.booking_id = b.id
			WHERE b.tenant_id = $1 AND b.event_id = $2 AND bs.released_at IS NULL),
		count(*) FILTER (WHERE r.status = 'HELD' AND r.expires_at > $3),
		count(*) FILTER (WHERE r.status = 'EXPIRED' OR (r.status = 'HELD' AND r.expires_at <= $3)),
		count(*) FILTER (WHERE r.status = 'CANCELLED' AND b.id IS NULL)
		FROM reservations r LEFT JOIN bookings b ON b.reservation_id = r.id
		WHERE r.tenant_id = $1 AND r.event_id = $2`, tenantID, eventID, now).
		Scan(&s.Bookings, &s.CancelledBookings, &s.SeatsSold, &s.OpenHolds, &s.ExpiredHolds, &s.CancelledHolds)
	return s, err
}

// readBookings reads bookings as organizers see them; where filters and orders them.
func readBookings(ctx context.Context, tx pgx.Tx, where string, args ...any) ([]Booking, error) {
	rows, err := tx.Query(ctx, `SELECT b.id::text, b.created_at, b.cancelled_at, u.display_name, u.email,
		ARRAY(SELECT s.label FROM booking_seats bs JOIN seats s ON s.id = bs.seat_id
			WHERE bs.booking_id = b.id ORDER BY s.section, s.row_number, s.seat_number),
		b.price_minor, b.currency
		FROM bookings b JOIN users u ON u.id = b.user_id `+where, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Booking, error) {
		var b Booking
		err := row.Scan(&b.ID, &b.CreatedAt, &b.CancelledAt, &b.Customer.DisplayName, &b.Customer.Email, &b.SeatLabels, &b.PriceMinor, &b.Currency)
		b.Status = "CONFIRMED"
		if b.CancelledAt != nil {
			b.Status = "CANCELLED"
		}
		b.TotalMinor = b.PriceMinor * int64(len(b.SeatLabels))
		return b, err
	})
}

func cursorTime(c *Cursor) *time.Time {
	if c == nil {
		return nil
	}
	return &c.CreatedAt
}

func cursorID(c *Cursor) *string {
	if c == nil {
		return nil
	}
	return &c.ID
}
