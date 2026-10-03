package reservation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"ticker/db/queries"
)

var ErrNotFound = errors.New("reservation not found")

type Reservation struct {
	ID         string    `json:"id"`
	EventID    string    `json:"event_id"`
	SeatID     string    `json:"seat_id"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	ServerTime time.Time `json:"server_time"`
	BookingID  *string   `json:"booking_id,omitempty"`
}

// Result preserves the original response, including its timestamp, on replay.
type Result struct {
	Status int
	Body   []byte
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Reserve(ctx context.Context, userID, key, eventID, seatID string) (Result, error) {
	tx, err := s.begin(ctx)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback(context.Background())
	// UUIDs are normalized by the handler. Include the operation/schema in the hash.
	payloadHash := sha256.Sum256([]byte("reserve/v1/" + eventID + "/" + seatID))
	if _, err := tx.Exec(ctx, `INSERT INTO idempotency_records (user_id, operation, key, payload_hash)
		VALUES ($1, 'reserve', $2, $3) ON CONFLICT DO NOTHING`, userID, key, payloadHash[:]); err != nil {
		return Result{}, err
	}
	var storedHash, body []byte
	var status *int
	if err := tx.QueryRow(ctx, `SELECT payload_hash, response_status, response_body FROM idempotency_records
		WHERE user_id = $1 AND operation = 'reserve' AND key = $2 FOR UPDATE`, userID, key).
		Scan(&storedHash, &status, &body); err != nil {
		return Result{}, err
	}
	if !bytes.Equal(storedHash, payloadHash[:]) {
		return failure(409, "idempotency_key_reused", "request key was already used with a different payload"), nil
	}
	if status != nil {
		return Result{Status: *status, Body: body}, nil
	}
	// Lock order: request key, new reservation, seat. No existing reservation is
	// locked while acquiring a seat; cancellation/expiration lock reservation first.
	var reservation Reservation
	var tenantID string
	err = tx.QueryRow(ctx, `INSERT INTO reservations (tenant_id, event_id, user_id, expires_at)
		SELECT tenant_id, id, $2, clock_timestamp() + interval '5 minutes' FROM events
		WHERE id = $1 AND status = 'PUBLISHED'
		RETURNING id::text, tenant_id::text, event_id::text, status, created_at, expires_at`, eventID, userID).
		Scan(&reservation.ID, &tenantID, &reservation.EventID, &reservation.Status, &reservation.CreatedAt, &reservation.ExpiresAt)
	result := failure(404, "seat_not_found", "seat in a published event not found")
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Result{}, err
	}
	if err == nil {
		var seatStatus string
		var salesOpen, startsAt time.Time
		err = tx.QueryRow(ctx, queries.LockReservationSeat, seatID, eventID).Scan(&seatStatus, &salesOpen, &startsAt)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Result{}, err
		}
		if err == nil {
			// Take the time and occupancy snapshot after the seat lock is acquired.
			var owned bool
			if err := tx.QueryRow(ctx, `SELECT clock_timestamp(), EXISTS (
				SELECT 1 FROM reservation_seats WHERE seat_id = $1 AND released_at IS NULL)`, seatID).
				Scan(&reservation.ServerTime, &owned); err != nil {
				return Result{}, err
			}
			result = failure(409, "seat_unavailable", "seat is unavailable")
			if reservation.ServerTime.Before(salesOpen) || !reservation.ServerTime.Before(startsAt) {
				result = failure(409, "sales_closed", "event is not currently on sale")
			} else if seatStatus == "AVAILABLE" && !owned {
				reservation.SeatID = seatID
				reservation.ExpiresAt = reservation.ServerTime.Add(5 * time.Minute)
				if _, err := tx.Exec(ctx, `UPDATE reservations SET expires_at = $2 WHERE id = $1`, reservation.ID, reservation.ExpiresAt); err != nil {
					return Result{}, err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO reservation_seats (reservation_id, tenant_id, event_id, seat_id)
					VALUES ($1, $2, $3, $4)`, reservation.ID, tenantID, eventID, seatID); err != nil {
					return Result{}, err
				}
				result = success(201, reservation)
			}
		}
		if result.Status != 201 {
			if _, err := tx.Exec(ctx, "DELETE FROM reservations WHERE id = $1", reservation.ID); err != nil {
				return Result{}, err
			}
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE idempotency_records SET response_status = $3, response_body = $4
		WHERE user_id = $1 AND operation = 'reserve' AND key = $2`, userID, key, result.Status, result.Body); err != nil {
		return Result{}, err
	}
	// A lost commit response is recovered by retrying with this same request key.
	return result, tx.Commit(ctx)
}

func (s *Store) Get(ctx context.Context, userID, id string) (Reservation, error) {
	reservation, err := scan(s.pool.QueryRow(ctx, queries.GetReservation, id, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Reservation{}, ErrNotFound
	}
	return reservation, err
}

func (s *Store) Cancel(ctx context.Context, userID, id string) (Reservation, error) {
	tx, err := s.begin(ctx)
	if err != nil {
		return Reservation{}, err
	}
	defer tx.Rollback(context.Background())
	var status string
	if err := tx.QueryRow(ctx, "SELECT status FROM reservations WHERE id = $1 AND user_id = $2 FOR UPDATE", id, userID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Reservation{}, ErrNotFound
		}
		return Reservation{}, err
	}
	if status == "HELD" {
		if err := lockSeats(ctx, tx, id); err != nil {
			return Reservation{}, err
		}
		var expired bool
		if err := tx.QueryRow(ctx, "SELECT clock_timestamp() >= expires_at FROM reservations WHERE id = $1", id).Scan(&expired); err != nil {
			return Reservation{}, err
		}
		status = "CANCELLED"
		if expired {
			status = "EXPIRED"
		}
		if err := release(ctx, tx, id, status); err != nil {
			return Reservation{}, err
		}
	}
	reservation, err := scan(tx.QueryRow(ctx, queries.GetReservation, id, userID))
	if err != nil {
		return Reservation{}, err
	}
	return reservation, tx.Commit(ctx)
}

// ExpireOne releases one overdue hold per transaction. Other workers skip its
// reservation lock; after a crash, an uncommitted release remains eligible.
func (s *Store) ExpireOne(ctx context.Context) (bool, error) {
	tx, err := s.begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.Background())
	var id string
	err = tx.QueryRow(ctx, `SELECT id::text FROM reservations
		WHERE status = 'HELD' AND expires_at <= clock_timestamp()
		ORDER BY expires_at, id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := lockSeats(ctx, tx, id); err != nil {
		return false, err
	}
	var expired bool
	if err := tx.QueryRow(ctx, "SELECT status = 'HELD' AND expires_at <= clock_timestamp() FROM reservations WHERE id = $1", id).Scan(&expired); err != nil {
		return false, err
	}
	if !expired {
		return false, nil
	}
	if err := release(ctx, tx, id, "EXPIRED"); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (s *Store) begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '250ms'; SET LOCAL statement_timeout = '1s'"); err != nil {
		tx.Rollback(context.Background())
		return nil, err
	}
	return tx, nil
}

func lockSeats(ctx context.Context, tx pgx.Tx, id string) error {
	rows, err := tx.Query(ctx, `SELECT s.id FROM seats s JOIN reservation_seats rs ON rs.seat_id = s.id
		WHERE rs.reservation_id = $1 AND rs.released_at IS NULL ORDER BY s.id FOR UPDATE OF s`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		// Consume the rows to acquire every lock before sampling database time.
	}
	return rows.Err()
}

func release(ctx context.Context, tx pgx.Tx, id, status string) error {
	if _, err := tx.Exec(ctx, "UPDATE reservation_seats SET released_at = clock_timestamp() WHERE reservation_id = $1 AND released_at IS NULL", id); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "UPDATE reservations SET status = $2 WHERE id = $1", id, status)
	return err
}

func scan(row pgx.Row) (Reservation, error) {
	var reservation Reservation
	err := row.Scan(&reservation.ID, &reservation.EventID, &reservation.SeatID, &reservation.Status,
		&reservation.CreatedAt, &reservation.ExpiresAt, &reservation.ServerTime, &reservation.BookingID)
	return reservation, err
}

func success(status int, reservation Reservation) Result {
	body, _ := json.Marshal(struct {
		Reservation Reservation `json:"reservation"`
	}{reservation})
	return Result{Status: status, Body: body}
}

func failure(status int, code, message string) Result {
	body, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": message}})
	return Result{Status: status, Body: body}
}
