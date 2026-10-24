package catalog

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
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
	pool  *pgxpool.Pool
	seats *seatCache
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, seats: newSeatCache()}
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

// SeatMap is one encoded seat-map response, shared by every request until its
// snapshot expires. A 1000-seat map is about 129 KB of JSON and 30 KB gzipped,
// so it is encoded and compressed once per snapshot instead of per request.
type SeatMap struct {
	JSON []byte
	Gzip []byte
}

// ListSeats returns nil when the event is missing or unpublished: published
// events always have seats.
func (s *Store) ListSeats(ctx context.Context, eventID string) (*SeatMap, error) {
	return s.seats.get(ctx, eventID, func(ctx context.Context, previous *SeatMap) (*SeatMap, error) {
		seats, err := s.listSeats(ctx, eventID)
		if err != nil || len(seats) == 0 {
			return nil, err
		}
		return encodeSeatMap(eventID, seats, previous)
	})
}

func encodeSeatMap(eventID string, seats []Seat, previous *SeatMap) (*SeatMap, error) {
	body, err := json.Marshal(struct {
		EventID string `json:"event_id"`
		Seats   []Seat `json:"seats"`
	}{eventID, seats})
	if err != nil {
		return nil, err
	}
	// Match json.Encoder, which the other endpoints use.
	body = append(body, '\n')
	// Most events have not changed since their last snapshot, and compressing
	// costs several times more than encoding.
	if previous != nil && bytes.Equal(previous.JSON, body) {
		return previous, nil
	}
	// Level 2 is about a third faster than the default for 6% more bytes.
	var compressed bytes.Buffer
	zw, err := gzip.NewWriterLevel(&compressed, 2)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(body); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return &SeatMap{JSON: body, Gzip: compressed.Bytes()}, nil
}

func (s *Store) listSeats(ctx context.Context, eventID string) ([]Seat, error) {
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
	event.SalesOpenAt = event.SalesOpenAt.UTC()
	event.StartsAt = event.StartsAt.UTC()
	return event, err
}
