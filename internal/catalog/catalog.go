package catalog

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"ticker/db/queries"
)

var ErrNotFound = errors.New("published event not found")

type Event struct {
	ID            string    `json:"id"`
	TenantID      string    `json:"tenant_id"`
	Title         string    `json:"title"`
	Description   string    `json:"description"`
	VenueName     string    `json:"venue_name"`
	PriceMinor    int64     `json:"price_minor"`
	Currency      string    `json:"currency"`
	SalesOpenAt   time.Time `json:"sales_open_at"`
	StartsAt      time.Time `json:"starts_at"`
	LayoutRows    int       `json:"layout_rows"`
	LayoutColumns int       `json:"layout_columns"`
}

type Seat struct {
	ID         string `json:"id"`
	Section    string `json:"section"`
	RowNumber  int    `json:"row_number"`
	SeatNumber int    `json:"seat_number"`
	Label      string `json:"label"`
	Status     string `json:"status"`
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) ListEvents(ctx context.Context, limit, offset int) ([]Event, error) {
	rows, err := s.pool.Query(ctx, queries.ListPublishedEvents, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]Event, 0)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) GetEvent(ctx context.Context, id string) (Event, error) {
	event, err := scanEvent(s.pool.QueryRow(ctx, queries.GetPublishedEvent, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Event{}, ErrNotFound
	}
	return event, err
}

func (s *Store) ListSeats(ctx context.Context, eventID string) ([]Seat, error) {
	rows, err := s.pool.Query(ctx, queries.ListEventSeats, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seats := make([]Seat, 0)
	for rows.Next() {
		var seat Seat
		if err := rows.Scan(&seat.ID, &seat.Section, &seat.RowNumber, &seat.SeatNumber, &seat.Label, &seat.Status); err != nil {
			return nil, err
		}
		seats = append(seats, seat)
	}
	return seats, rows.Err()
}

func scanEvent(row pgx.Row) (Event, error) {
	var event Event
	err := row.Scan(&event.ID, &event.TenantID, &event.Title, &event.Description, &event.VenueName,
		&event.PriceMinor, &event.Currency, &event.SalesOpenAt, &event.StartsAt, &event.LayoutRows, &event.LayoutColumns)
	return event, err
}
