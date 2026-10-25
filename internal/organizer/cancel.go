package organizer

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"ticker/internal/reservation"
)

var (
	ErrBookingNotFound = errors.New("booking not found")
	ErrEventStarted    = reservation.ErrEventStarted
)

// CancelBooking cancels a confirmed booking and puts its seats back on sale,
// until the event starts. Repeating it returns the cancelled booking unchanged.
// Lock order matches a reservation: the event's share lock, so a start-date edit
// cannot run meanwhile, then the reservation, then its seats by ID.
func (s *Store) CancelBooking(ctx context.Context, userID, tenantID, eventID, bookingID string) (Booking, error) {
	tx, err := s.beginAuthorized(ctx, userID, tenantID)
	if err != nil {
		return Booking{}, err
	}
	defer tx.Rollback(context.Background())
	// Fail fast instead of queueing behind a busy sale; the caller may retry.
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '1s'"); err != nil {
		return Booking{}, err
	}
	var reservationID, status string
	err = tx.QueryRow(ctx, `SELECT 1 FROM events WHERE tenant_id = $1 AND id = $2 FOR SHARE`, tenantID, eventID).Scan(new(int))
	if err == nil {
		err = tx.QueryRow(ctx, `SELECT r.id::text, r.status FROM bookings b JOIN reservations r ON r.id = b.reservation_id
			WHERE b.tenant_id = $1 AND b.event_id = $2 AND b.id = $3 FOR UPDATE OF r`, tenantID, eventID, bookingID).
			Scan(&reservationID, &status)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Booking{}, ErrBookingNotFound
	}
	if err != nil {
		return Booking{}, err
	}
	if status == "CONFIRMED" {
		if err := reservation.CancelBooking(ctx, tx, reservationID, userID); err != nil {
			return Booking{}, err
		}
	}
	bookings, err := readBookings(ctx, tx, `WHERE b.id = $1`, bookingID)
	if err != nil {
		return Booking{}, err
	}
	return bookings[0], tx.Commit(ctx)
}
