package organizer

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"ticker/internal/catalog"
	"ticker/internal/reservation"
)

var (
	ErrNotPublished = errors.New("event is not published")
	ErrSeatNotFound = errors.New("seat not found")
)

// SeatConflictError lists seats that cannot take the requested status because
// they are sold or held. No seat changed.
type SeatConflictError struct{ SeatIDs []string }

func (e *SeatConflictError) Error() string { return "seats are sold or held" }

// SeatInput blocks or unblocks seats. SeatIDs are distinct canonical UUIDs.
type SeatInput struct {
	SeatIDs []string `json:"seat_ids"`
	Status  string   `json:"status"`
}

// seatColumns matches the public seat map: an open hold reads HELD, an overdue
// one reads as its stored status.
const seatColumns = `s.id::text, s.section, s.row_number, s.seat_number, s.label,
	CASE WHEN s.status = 'SOLD' THEN 'SOLD' WHEN EXISTS (
		SELECT 1 FROM reservation_seats rs JOIN reservations r ON r.id = rs.reservation_id
		WHERE rs.seat_id = s.id AND rs.released_at IS NULL
		AND r.status = 'HELD' AND r.expires_at > clock_timestamp()) THEN 'HELD' ELSE s.status END`

// ListSeats reads the event's seats fresh, without the public seat-map cache.
// Drafts have no seats yet.
func (s *Store) ListSeats(ctx context.Context, userID, tenantID, eventID string) ([]catalog.Seat, error) {
	tx, err := s.beginAuthorized(ctx, userID, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM events WHERE tenant_id = $1 AND id = $2)`, tenantID, eventID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	seats, err := readSeats(ctx, tx, `SELECT `+seatColumns+` FROM seats s WHERE s.tenant_id = $1 AND s.event_id = $2
		ORDER BY s.section, s.row_number, s.seat_number`, tenantID, eventID)
	if err != nil {
		return nil, err
	}
	return seats, tx.Commit(ctx)
}

// SetSeatStatus blocks or unblocks every requested seat, or none. Repeating a
// request is harmless: seats already in the target status stay as they are.
// Lock order matches a reservation: event, overdue reservations, then seats by ID.
func (s *Store) SetSeatStatus(ctx context.Context, userID, tenantID, eventID string, input SeatInput) ([]catalog.Seat, error) {
	if (input.Status != "BLOCKED" && input.Status != "AVAILABLE") || len(input.SeatIDs) == 0 {
		return nil, ErrInvalidEvent
	}
	tx, err := s.beginAuthorized(ctx, userID, tenantID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	// Fail fast instead of queueing behind a busy sale; the caller may retry.
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '1s'"); err != nil {
		return nil, err
	}
	// The same event lock a reservation takes, so a layout edit cannot run meanwhile.
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM events WHERE tenant_id = $1 AND id = $2 FOR SHARE`, tenantID, eventID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if status != "PUBLISHED" {
		return nil, ErrNotPublished
	}
	if err := reservation.ReleaseOverdue(ctx, tx, input.SeatIDs); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM seats WHERE tenant_id = $1 AND event_id = $2 AND id = ANY($3::uuid[])
		ORDER BY id FOR UPDATE`, tenantID, eventID, input.SeatIDs)
	if err != nil {
		return nil, err
	}
	locked, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	if len(locked) != len(input.SeatIDs) {
		return nil, ErrSeatNotFound
	}
	// A fresh statement after the locks sees holds and sales committed meanwhile.
	// Confirmed seats keep their reservation row, so "owned" covers sold seats too.
	rows, err = tx.Query(ctx, `SELECT s.id::text FROM seats s WHERE s.id = ANY($1::uuid[]) AND (s.status = 'SOLD'
		OR EXISTS (SELECT 1 FROM reservation_seats rs WHERE rs.seat_id = s.id AND rs.released_at IS NULL))
		ORDER BY s.id`, input.SeatIDs)
	if err != nil {
		return nil, err
	}
	conflicts, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	if len(conflicts) > 0 {
		return nil, &SeatConflictError{SeatIDs: conflicts}
	}
	if _, err := tx.Exec(ctx, `UPDATE seats SET status = $2 WHERE id = ANY($1::uuid[]) AND status <> $2`, input.SeatIDs, input.Status); err != nil {
		return nil, err
	}
	seats, err := readSeats(ctx, tx, `SELECT `+seatColumns+` FROM seats s WHERE s.id = ANY($1::uuid[])
		ORDER BY s.section, s.row_number, s.seat_number`, input.SeatIDs)
	if err != nil {
		return nil, err
	}
	return seats, tx.Commit(ctx)
}

func readSeats(ctx context.Context, tx pgx.Tx, query string, args ...any) ([]catalog.Seat, error) {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seats := make([]catalog.Seat, 0)
	for rows.Next() {
		var seat catalog.Seat
		if err := rows.Scan(&seat.ID, &seat.Section, &seat.RowNumber, &seat.SeatNumber, &seat.Label, &seat.Status); err != nil {
			return nil, err
		}
		seats = append(seats, seat)
	}
	return seats, rows.Err()
}
