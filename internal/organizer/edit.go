package organizer

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *Store) UpdateEvent(ctx context.Context, userID, tenantID, eventID string, input EditInput) (Event, error) {
	tx, err := s.beginAuthorized(ctx, userID, tenantID)
	if err != nil {
		return Event{}, err
	}
	defer tx.Rollback(context.Background())
	existing, err := lockEvent(ctx, tx, tenantID, eventID, input.ExpectedVersion)
	if err != nil {
		return Event{}, err
	}
	if input.PriceMinor == nil || input.SalesOpenAt.IsZero() || input.StartsAt.IsZero() {
		return Event{}, ErrInvalidEvent
	}
	event := existing
	event.Title = strings.TrimSpace(input.Title)
	event.Description = strings.TrimSpace(input.Description)
	event.VenueName = strings.TrimSpace(input.VenueName)
	event.PriceMinor = *input.PriceMinor
	event.Currency = input.Currency
	event.SalesOpenAt = input.SalesOpenAt.UTC().Truncate(time.Microsecond)
	event.StartsAt = input.StartsAt.UTC().Truncate(time.Microsecond)
	event.LayoutRows = input.LayoutRows
	event.LayoutColumns = input.LayoutColumns
	termsChanged := event.PriceMinor != existing.PriceMinor || event.Currency != existing.Currency ||
		event.LayoutRows != existing.LayoutRows || event.LayoutColumns != existing.LayoutColumns
	if termsChanged && !existing.PriceLayoutEditable {
		return Event{}, ErrFrozenLayout
	}
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return Event{}, err
	}
	// An unchanged start may be in the past; moving the start still requires a future date.
	if event.StartsAt.Equal(existing.StartsAt) && !event.StartsAt.After(now) {
		now = event.StartsAt.Add(-time.Microsecond)
	}
	if err := validateEvent(event, now); err != nil {
		return Event{}, err
	}
	layoutChanged := event.LayoutRows != existing.LayoutRows || event.LayoutColumns != existing.LayoutColumns
	var sections []string
	if layoutChanged && existing.Status == "PUBLISHED" {
		rows, err := tx.Query(ctx, `SELECT DISTINCT section FROM seats WHERE tenant_id = $1 AND event_id = $2 ORDER BY section`, tenantID, eventID)
		if err != nil {
			return Event{}, err
		}
		for rows.Next() {
			var section string
			if err := rows.Scan(&section); err != nil {
				rows.Close()
				return Event{}, err
			}
			sections = append(sections, section)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return Event{}, err
		}
		// Retain seat IDs within the new rectangle; no seat with reservation history can reach this branch.
		if _, err := tx.Exec(ctx, `DELETE FROM seats WHERE tenant_id = $1 AND event_id = $2
			AND (row_number > $3 OR seat_number > $4)`, tenantID, eventID, event.LayoutRows, event.LayoutColumns); err != nil {
			return Event{}, layoutError(err)
		}
	} else if layoutChanged {
		var excludesSeats bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM seats WHERE tenant_id = $1 AND event_id = $2
			AND (row_number > $3 OR seat_number > $4))`, tenantID, eventID, event.LayoutRows, event.LayoutColumns).Scan(&excludesSeats); err != nil {
			return Event{}, err
		}
		if excludesSeats {
			return Event{}, ErrLayoutConflict
		}
	}
	event, err = scanEvent(tx.QueryRow(ctx, `UPDATE events SET title = $3, description = $4, venue_name = $5,
		price_minor = $6, currency = $7, sales_open_at = $8, starts_at = $9, layout_rows = $10, layout_columns = $11
		WHERE tenant_id = $1 AND id = $2 RETURNING `+eventColumns,
		tenantID, eventID, event.Title, event.Description, event.VenueName, event.PriceMinor, event.Currency,
		event.SalesOpenAt, event.StartsAt, event.LayoutRows, event.LayoutColumns))
	if err != nil {
		return Event{}, layoutError(err)
	}
	if layoutChanged && existing.Status == "PUBLISHED" {
		for _, section := range sections {
			if _, err := tx.Exec(ctx, `INSERT INTO seats (tenant_id, event_id, section, row_number, seat_number, label)
				SELECT $1, $2, $3, r, c,
					CASE WHEN $3 = 'Main' THEN '' ELSE $3 || '-' END || 'R' || r::text || '-S' || c::text
				FROM generate_series(1, $4::integer) r CROSS JOIN generate_series(1, $5::integer) c
				WHERE NOT EXISTS (SELECT 1 FROM seats WHERE tenant_id = $1 AND event_id = $2
					AND section = $3 AND row_number = r AND seat_number = c)`, tenantID, eventID, section, event.LayoutRows, event.LayoutColumns); err != nil {
				return Event{}, layoutError(err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Event{}, err
	}
	return event, nil
}

func (s *Store) DeleteEvent(ctx context.Context, userID, tenantID, eventID string, input DeleteInput) error {
	tx, err := s.beginAuthorized(ctx, userID, tenantID)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	event, err := lockEvent(ctx, tx, tenantID, eventID, input.ExpectedVersion)
	if err != nil {
		return err
	}
	if !event.PriceLayoutEditable {
		return ErrHasReservations
	}
	if _, err := tx.Exec(ctx, `DELETE FROM seats WHERE tenant_id = $1 AND event_id = $2`, tenantID, eventID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM events WHERE tenant_id = $1 AND id = $2`, tenantID, eventID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func lockEvent(ctx context.Context, tx pgx.Tx, tenantID, eventID string, expectedVersion *int64) (Event, error) {
	if expectedVersion == nil || *expectedVersion < 1 {
		return Event{}, ErrInvalidEvent
	}
	// Reservation insertion takes the trigger's event SHARE lock. FOR UPDATE
	// serializes against it before we decide whether price/layout/deletion are allowed.
	event, err := scanEvent(tx.QueryRow(ctx, `SELECT `+eventColumns+` FROM events WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenantID, eventID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Event{}, ErrNotFound
	}
	if err != nil {
		return Event{}, err
	}
	if event.Version != *expectedVersion {
		return Event{}, ErrVersionConflict
	}
	// A fresh statement sees any reservation committed while the event lock was waiting.
	if err := tx.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM reservations WHERE tenant_id = $1 AND event_id = $2)`, tenantID, eventID).Scan(&event.PriceLayoutEditable); err != nil {
		return Event{}, err
	}
	return event, nil
}

func layoutError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "23514" || pgErr.Code == "23505" || pgErr.Code == "23503") {
		return ErrLayoutConflict
	}
	return err
}
