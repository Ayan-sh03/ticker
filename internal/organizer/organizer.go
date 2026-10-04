package organizer

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"ticker/internal/catalog"
)

var (
	ErrForbidden       = errors.New("organizer membership required")
	ErrNotFound        = errors.New("event not found")
	ErrInvalidEvent    = errors.New("invalid event details")
	ErrLayoutExists    = errors.New("draft already has seats")
	ErrVersionConflict = errors.New("event changed since it was loaded")
	ErrFrozenLayout    = errors.New("event prices and layout are locked after the first reservation")
	ErrLayoutConflict  = errors.New("layout change conflicts with existing seats")
	ErrHasReservations = errors.New("event has reservation history")
)

type Event struct {
	catalog.Event
	Status              string `json:"status"`
	Version             int64  `json:"version"`
	PriceLayoutEditable bool   `json:"price_layout_editable"`
}

type Tenant struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type DraftInput struct {
	Title         string    `json:"title"`
	Description   string    `json:"description"`
	VenueName     string    `json:"venue_name"`
	PriceMinor    *int64    `json:"price_minor"`
	Currency      string    `json:"currency"`
	SalesOpenAt   time.Time `json:"sales_open_at"`
	StartsAt      time.Time `json:"starts_at"`
	LayoutRows    int       `json:"layout_rows"`
	LayoutColumns int       `json:"layout_columns"`
}

type EditInput struct {
	DraftInput
	ExpectedVersion *int64 `json:"expected_version"`
}

type DeleteInput struct {
	ExpectedVersion *int64 `json:"expected_version"`
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) ListTenants(ctx context.Context, userID string) ([]Tenant, error) {
	rows, err := s.pool.Query(ctx, `SELECT t.id::text, t.name FROM tenants t
		JOIN memberships m ON m.tenant_id = t.id WHERE m.user_id = $1 ORDER BY t.name, t.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tenants := make([]Tenant, 0)
	for rows.Next() {
		var tenant Tenant
		if err := rows.Scan(&tenant.ID, &tenant.Name); err != nil {
			return nil, err
		}
		tenants = append(tenants, tenant)
	}
	return tenants, rows.Err()
}

const eventColumns = `id::text, tenant_id::text, title, description, venue_name,
	price_minor, currency, sales_open_at, starts_at, layout_rows, layout_columns, status, version,
	NOT EXISTS (SELECT 1 FROM reservations WHERE reservations.event_id = events.id)`

func (s *Store) CreateEvent(ctx context.Context, userID, tenantID string, input DraftInput) (Event, error) {
	tx, err := s.beginAuthorized(ctx, userID, tenantID)
	if err != nil {
		return Event{}, err
	}
	defer tx.Rollback(context.Background())

	if input.PriceMinor == nil || input.SalesOpenAt.IsZero() || input.StartsAt.IsZero() {
		return Event{}, ErrInvalidEvent
	}
	// PostgreSQL stores microseconds; validate the times that will be persisted.
	event := Event{Event: catalog.Event{
		TenantID: tenantID, Title: strings.TrimSpace(input.Title), Description: strings.TrimSpace(input.Description),
		VenueName: strings.TrimSpace(input.VenueName), PriceMinor: *input.PriceMinor, Currency: input.Currency,
		SalesOpenAt: input.SalesOpenAt.UTC().Truncate(time.Microsecond), StartsAt: input.StartsAt.UTC().Truncate(time.Microsecond),
		LayoutRows: input.LayoutRows, LayoutColumns: input.LayoutColumns,
	}, Status: "DRAFT"}
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return Event{}, err
	}
	if err := validateEvent(event, now); err != nil {
		return Event{}, err
	}
	event, err = scanEvent(tx.QueryRow(ctx, `INSERT INTO events
		(tenant_id, title, description, venue_name, price_minor, currency, sales_open_at, starts_at, layout_rows, layout_columns)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING `+eventColumns,
		tenantID, event.Title, event.Description, event.VenueName, event.PriceMinor, event.Currency,
		event.SalesOpenAt, event.StartsAt, event.LayoutRows, event.LayoutColumns))
	if err != nil {
		return Event{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Event{}, err
	}
	return event, nil
}

func (s *Store) ListEvents(ctx context.Context, userID, tenantID string, limit, offset int) ([]Event, error) {
	tx, err := s.beginAuthorized(ctx, userID, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(ctx, `SELECT `+eventColumns+` FROM events
		WHERE tenant_id = $1 ORDER BY starts_at, id LIMIT $2 OFFSET $3`, tenantID, limit, offset)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *Store) GetEvent(ctx context.Context, userID, tenantID, eventID string) (Event, error) {
	tx, err := s.beginAuthorized(ctx, userID, tenantID)
	if err != nil {
		return Event{}, err
	}
	defer tx.Rollback(context.Background())
	event, err := scanEvent(tx.QueryRow(ctx, `SELECT `+eventColumns+` FROM events WHERE tenant_id = $1 AND id = $2`, tenantID, eventID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Event{}, ErrNotFound
	}
	if err != nil {
		return Event{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Event{}, err
	}
	return event, nil
}

// Keep membership locked through the operation so revocation cannot race a
// private read or mutation after its authorization check.
func (s *Store) beginAuthorized(ctx context.Context, userID, tenantID string) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	var member int
	err = tx.QueryRow(ctx, `SELECT 1 FROM memberships WHERE tenant_id = $1 AND user_id = $2 FOR SHARE`, tenantID, userID).Scan(&member)
	if err != nil {
		_ = tx.Rollback(context.Background())
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrForbidden
		}
		return nil, err
	}
	return tx, nil
}

func validateEvent(event Event, now time.Time) error {
	if strings.TrimSpace(event.Title) == "" || utf8.RuneCountInString(event.Title) > 200 ||
		utf8.RuneCountInString(event.Description) > 5000 || strings.TrimSpace(event.VenueName) == "" ||
		utf8.RuneCountInString(event.VenueName) > 200 || strings.ContainsRune(event.Title+event.Description+event.VenueName, '\x00') ||
		event.PriceMinor < 0 || event.PriceMinor > 9007199254740991 || len(event.Currency) != 3 ||
		event.LayoutRows < 1 || event.LayoutRows > 100 || event.LayoutColumns < 1 || event.LayoutColumns > 100 {
		return ErrInvalidEvent
	}
	for _, char := range event.Currency {
		if char < 'A' || char > 'Z' {
			return ErrInvalidEvent
		}
	}
	if event.SalesOpenAt.UTC().Year() < 1 || event.SalesOpenAt.UTC().Year() > 9999 ||
		event.StartsAt.UTC().Year() < 1 || event.StartsAt.UTC().Year() > 9999 ||
		!event.SalesOpenAt.Before(event.StartsAt) || !event.StartsAt.After(now) {
		return ErrInvalidEvent
	}
	return nil
}

func scanEvent(row pgx.Row) (Event, error) {
	var event Event
	err := row.Scan(&event.ID, &event.TenantID, &event.Title, &event.Description, &event.VenueName,
		&event.PriceMinor, &event.Currency, &event.SalesOpenAt, &event.StartsAt, &event.LayoutRows, &event.LayoutColumns, &event.Status,
		&event.Version, &event.PriceLayoutEditable)
	event.SalesOpenAt = event.SalesOpenAt.UTC()
	event.StartsAt = event.StartsAt.UTC()
	return event, err
}
