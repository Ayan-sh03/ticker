package booking

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("booking not found")

// PriceMinor is per seat; TotalMinor covers every booked seat. Status is
// CONFIRMED, or CANCELLED once the customer or an organizer cancels the booking.
type Receipt struct {
	ID            string     `json:"id"`
	ReservationID string     `json:"reservation_id"`
	EventID       string     `json:"event_id"`
	SeatIDs       []string   `json:"seat_ids"`
	PriceMinor    int64      `json:"price_minor"`
	TotalMinor    int64      `json:"total_minor"`
	Currency      string     `json:"currency"`
	CreatedAt     time.Time  `json:"created_at"`
	Status        string     `json:"status"`
	CancelledAt   *time.Time `json:"cancelled_at"`
	// CancelledByCustomer is false while active and when an organizer cancelled.
	CancelledByCustomer bool `json:"cancelled_by_customer"`
}

type Querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// CancelledByCustomer is a SQL expression over bookings b: whether the
// booking's cancellation was the customer's own request.
const CancelledByCustomer = `EXISTS (SELECT 1 FROM audit_entries a WHERE a.reservation_id = b.reservation_id
        AND a.from_status = 'CONFIRMED' AND a.reason = 'customer_request')`

// Read also accepts a transaction so confirmation returns the persisted receipt.
func Read(ctx context.Context, q Querier, userID, id string) (Receipt, error) {
	var receipt Receipt
	err := q.QueryRow(ctx, `SELECT b.id::text, b.reservation_id::text, b.event_id::text,
        ARRAY(SELECT bs.seat_id::text FROM booking_seats bs WHERE bs.booking_id = b.id ORDER BY bs.seat_id),
        b.price_minor, b.currency, b.created_at, b.cancelled_at, `+CancelledByCustomer+`
        FROM bookings b WHERE b.id = $1 AND b.user_id = $2`, id, userID).Scan(
		&receipt.ID, &receipt.ReservationID, &receipt.EventID, &receipt.SeatIDs,
		&receipt.PriceMinor, &receipt.Currency, &receipt.CreatedAt, &receipt.CancelledAt, &receipt.CancelledByCustomer)
	if errors.Is(err, pgx.ErrNoRows) {
		return Receipt{}, ErrNotFound
	}
	receipt.TotalMinor = receipt.PriceMinor * int64(len(receipt.SeatIDs))
	receipt.Status = "CONFIRMED"
	if receipt.CancelledAt != nil {
		receipt.Status = "CANCELLED"
	}
	return receipt, err
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }
func (s *Store) Get(ctx context.Context, userID, id string) (Receipt, error) {
	return Read(ctx, s.pool, userID, id)
}
