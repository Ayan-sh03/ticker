package organizer

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type Customer struct {
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

// Booking is one confirmed purchase as an organizer sees it. PriceMinor is the
// per-seat price saved on the booking.
type Booking struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	Customer   Customer  `json:"customer"`
	SeatLabels []string  `json:"seat_labels"`
	PriceMinor int64     `json:"price_minor"`
	TotalMinor int64     `json:"total_minor"`
	Currency   string    `json:"currency"`
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

type SalesSummary struct {
	Bookings       int64 `json:"bookings"`
	SeatsSold      int64 `json:"seats_sold"`
	OpenHolds      int64 `json:"open_holds"`
	ExpiredHolds   int64 `json:"expired_holds"`
	CancelledHolds int64 `json:"cancelled_holds"`
}

// holdStates maps the filter to reservation states. An overdue hold reads as
// expired before the worker releases it, as customers see it.
var holdStates = map[string]string{
	"open":      `r.status = 'HELD' AND r.expires_at > clock_timestamp()`,
	"expired":   `(r.status = 'EXPIRED' OR (r.status = 'HELD' AND r.expires_at <= clock_timestamp()))`,
	"cancelled": `r.status = 'CANCELLED'`,
}

// ListBookings returns one page of the event's bookings, newest first, with the
// event's totals.
func (s *Store) ListBookings(ctx context.Context, userID, tenantID, eventID string, limit, offset int) (SalesSummary, []Booking, error) {
	tx, err := s.beginAuthorized(ctx, userID, tenantID)
	if err != nil {
		return SalesSummary{}, nil, err
	}
	defer tx.Rollback(context.Background())
	summary, err := salesSummary(ctx, tx, tenantID, eventID)
	if err != nil {
		return SalesSummary{}, nil, err
	}
	rows, err := tx.Query(ctx, `SELECT b.id::text, b.created_at, u.display_name, u.email,
		ARRAY(SELECT s.label FROM booking_seats bs JOIN seats s ON s.id = bs.seat_id
			WHERE bs.booking_id = b.id ORDER BY s.section, s.row_number, s.seat_number),
		b.price_minor, b.currency
		FROM bookings b JOIN users u ON u.id = b.user_id
		WHERE b.tenant_id = $1 AND b.event_id = $2
		ORDER BY b.created_at DESC, b.id DESC LIMIT $3 OFFSET $4`, tenantID, eventID, limit, offset)
	if err != nil {
		return SalesSummary{}, nil, err
	}
	bookings, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Booking, error) {
		var b Booking
		err := row.Scan(&b.ID, &b.CreatedAt, &b.Customer.DisplayName, &b.Customer.Email, &b.SeatLabels, &b.PriceMinor, &b.Currency)
		b.TotalMinor = b.PriceMinor * int64(len(b.SeatLabels))
		return b, err
	})
	if err != nil {
		return SalesSummary{}, nil, err
	}
	return summary, bookings, tx.Commit(ctx)
}

// ListHolds returns one page of the event's holds in one state, newest first.
func (s *Store) ListHolds(ctx context.Context, userID, tenantID, eventID, state string, limit, offset int) ([]Hold, error) {
	where, ok := holdStates[state]
	if !ok {
		return nil, ErrInvalidEvent
	}
	tx, err := s.beginAuthorized(ctx, userID, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	if err := eventExists(ctx, tx, tenantID, eventID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT r.id::text,
		CASE WHEN r.status = 'HELD' AND r.expires_at <= clock_timestamp() THEN 'EXPIRED' ELSE r.status END,
		r.created_at, r.expires_at,
		(SELECT max(a.created_at) FROM audit_entries a WHERE a.reservation_id = r.id AND a.to_status IN ('EXPIRED', 'CANCELLED')),
		u.display_name, u.email,
		ARRAY(SELECT s.label FROM reservation_seats rs JOIN seats s ON s.id = rs.seat_id
			WHERE rs.reservation_id = r.id ORDER BY s.section, s.row_number, s.seat_number)
		FROM reservations r JOIN users u ON u.id = r.user_id
		WHERE r.tenant_id = $1 AND r.event_id = $2 AND `+where+`
		ORDER BY r.created_at DESC, r.id DESC LIMIT $3 OFFSET $4`, tenantID, eventID, limit, offset)
	if err != nil {
		return nil, err
	}
	holds, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Hold, error) {
		var h Hold
		err := row.Scan(&h.ID, &h.Status, &h.CreatedAt, &h.ExpiresAt, &h.EndedAt, &h.Customer.DisplayName, &h.Customer.Email, &h.SeatLabels)
		return h, err
	})
	if err != nil {
		return nil, err
	}
	return holds, tx.Commit(ctx)
}

func salesSummary(ctx context.Context, tx pgx.Tx, tenantID, eventID string) (SalesSummary, error) {
	if err := eventExists(ctx, tx, tenantID, eventID); err != nil {
		return SalesSummary{}, err
	}
	var s SalesSummary
	err := tx.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM bookings WHERE tenant_id = $1 AND event_id = $2),
		(SELECT count(*) FROM bookings b JOIN booking_seats bs ON bs.booking_id = b.id WHERE b.tenant_id = $1 AND b.event_id = $2),
		count(*) FILTER (WHERE status = 'HELD' AND expires_at > clock_timestamp()),
		count(*) FILTER (WHERE status = 'EXPIRED' OR (status = 'HELD' AND expires_at <= clock_timestamp())),
		count(*) FILTER (WHERE status = 'CANCELLED')
		FROM reservations WHERE tenant_id = $1 AND event_id = $2`, tenantID, eventID).
		Scan(&s.Bookings, &s.SeatsSold, &s.OpenHolds, &s.ExpiredHolds, &s.CancelledHolds)
	return s, err
}

func eventExists(ctx context.Context, tx pgx.Tx, tenantID, eventID string) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM events WHERE tenant_id = $1 AND id = $2)`, tenantID, eventID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}
