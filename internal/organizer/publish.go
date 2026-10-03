package organizer

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Store) PublishEvent(ctx context.Context, userID, tenantID, eventID string) (Event, error) {
	tx, err := s.beginAuthorized(ctx, userID, tenantID)
	if err != nil {
		return Event{}, err
	}
	defer tx.Rollback(context.Background())

	// The parent lock serializes publication and draft seat edits. Generate the
	// complete layout and publish in one transaction so readers never see a part.
	event, err := scanEvent(tx.QueryRow(ctx, `SELECT `+eventColumns+`
		FROM events WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenantID, eventID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Event{}, ErrNotFound
	}
	if err != nil {
		return Event{}, err
	}
	// A retry must not recreate seats or change inventory after sales begin.
	if event.Status == "PUBLISHED" {
		return event, nil
	}

	var now time.Time
	var hasSeats bool
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp(),
		EXISTS (SELECT 1 FROM seats WHERE tenant_id = $1 AND event_id = $2)`, tenantID, eventID).
		Scan(&now, &hasSeats); err != nil {
		return Event{}, err
	}
	if err := validateEvent(event, now); err != nil {
		return Event{}, err
	}
	if hasSeats {
		return Event{}, ErrLayoutExists
	}

	if _, err := tx.Exec(ctx, `INSERT INTO seats
		(tenant_id, event_id, section, row_number, seat_number, label)
		SELECT $1, $2, 'Main', r, c, 'R' || r::text || '-S' || c::text
		FROM generate_series(1, $3::integer) AS r
		CROSS JOIN generate_series(1, $4::integer) AS c`, tenantID, eventID, event.LayoutRows, event.LayoutColumns); err != nil {
		return Event{}, err
	}
	event, err = scanEvent(tx.QueryRow(ctx, `UPDATE events SET status = 'PUBLISHED'
		WHERE tenant_id = $1 AND id = $2 RETURNING `+eventColumns, tenantID, eventID))
	if err != nil {
		return Event{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Event{}, err
	}
	return event, nil
}
