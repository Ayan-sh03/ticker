package booking

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("booking not found")

type Receipt struct {
	ID            string    `json:"id"`
	ReservationID string    `json:"reservation_id"`
	EventID       string    `json:"event_id"`
	SeatID        string    `json:"seat_id"`
	PriceMinor    int64     `json:"price_minor"`
	Currency      string    `json:"currency"`
	CreatedAt     time.Time `json:"created_at"`
}

type Querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Read also accepts a transaction so confirmation returns the persisted receipt.
func Read(ctx context.Context, q Querier, userID, id string) (Receipt, error) {
	var receipt Receipt
	err := q.QueryRow(ctx, `SELECT b.id::text, b.reservation_id::text, b.event_id::text,
        bs.seat_id::text, b.price_minor, b.currency, b.created_at
        FROM bookings b JOIN booking_seats bs ON bs.booking_id = b.id
        WHERE b.id = $1 AND b.user_id = $2`, id, userID).Scan(
		&receipt.ID, &receipt.ReservationID, &receipt.EventID, &receipt.SeatID,
		&receipt.PriceMinor, &receipt.Currency, &receipt.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Receipt{}, ErrNotFound
	}
	return receipt, err
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }
func (s *Store) Get(ctx context.Context, userID, id string) (Receipt, error) {
	return Read(ctx, s.pool, userID, id)
}
