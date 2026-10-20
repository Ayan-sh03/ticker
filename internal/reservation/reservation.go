package reservation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"ticker/db/queries"
)

var ErrNotFound = errors.New("reservation not found")

// PriceMinor is per seat; TotalMinor covers every seat in the hold.
type Reservation struct {
	ID         string    `json:"id"`
	EventID    string    `json:"event_id"`
	SeatIDs    []string  `json:"seat_ids"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	ServerTime time.Time `json:"server_time"`
	BookingID  *string   `json:"booking_id,omitempty"`
	PriceMinor int64     `json:"price_minor"`
	TotalMinor int64     `json:"total_minor"`
	Currency   string    `json:"currency"`
}

// Result preserves the original response, including its timestamp, on replay.
type Result struct {
	Status int
	Body   []byte
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Reserve holds every requested seat or none. seatIDs are distinct canonical UUIDs.
func (s *Store) Reserve(ctx context.Context, userID, key, eventID string, seatIDs []string) (Result, error) {
	seatIDs = slices.Sorted(slices.Values(seatIDs))
	tx, err := s.begin(ctx)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback(context.Background())
	// The sorted seat set makes [a, b] and [b, a] the same request.
	payloadHash := sha256.Sum256([]byte("reserve/v2/" + eventID + "/" + strings.Join(seatIDs, ",")))
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
	// Lock order: request key, new reservation, overdue reservations by ascending ID,
	// seats by ascending ID. Like cancellation and expiration, reservations come before seats.
	var reservation Reservation
	var tenantID string
	err = tx.QueryRow(ctx, `INSERT INTO reservations (tenant_id, event_id, user_id, quantity, expires_at)
		SELECT tenant_id, id, $2, $3, clock_timestamp() + interval '5 minutes' FROM events
		WHERE id = $1 AND status = 'PUBLISHED'
		RETURNING id::text, tenant_id::text, event_id::text, status, created_at, expires_at`, eventID, userID, len(seatIDs)).
		Scan(&reservation.ID, &tenantID, &reservation.EventID, &reservation.Status, &reservation.CreatedAt, &reservation.ExpiresAt)
	result := failure(404, "seat_not_found", "every seat must belong to a published event")
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Result{}, err
	}
	if err == nil {
		if err := releaseOverdue(ctx, tx, seatIDs); err != nil {
			return Result{}, err
		}
		if result, err = allocate(ctx, tx, &reservation, tenantID, userID, key, seatIDs); err != nil {
			return Result{}, err
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

// releaseOverdue frees expired holds on the requested seats, so expiry does not
// depend on a running worker. A row changed while waiting for its lock is rechecked
// by PostgreSQL, so a hold confirmed or cancelled meanwhile is skipped.
func releaseOverdue(ctx context.Context, tx pgx.Tx, seatIDs []string) error {
	rows, err := tx.Query(ctx, `SELECT r.id::text FROM reservations r
		WHERE r.status = 'HELD' AND r.expires_at <= clock_timestamp() AND EXISTS (
			SELECT 1 FROM reservation_seats rs WHERE rs.reservation_id = r.id
			AND rs.seat_id = ANY($1::uuid[]) AND rs.released_at IS NULL)
		ORDER BY r.id FOR UPDATE OF r`, seatIDs)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := release(ctx, tx, id, "EXPIRED", "", "deadline_passed"); err != nil {
			return err
		}
	}
	return nil
}

// allocate locks every requested seat before deciding, then assigns all of them or none.
func allocate(ctx context.Context, tx pgx.Tx, reservation *Reservation, tenantID, userID, key string, seatIDs []string) (Result, error) {
	rows, err := tx.Query(ctx, queries.LockReservationSeats, seatIDs, reservation.EventID)
	if err != nil {
		return Result{}, err
	}
	statuses, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return Result{}, err
	}
	if len(statuses) != len(seatIDs) {
		return failure(404, "seat_not_found", "every seat must belong to a published event"), nil
	}
	// Take the time and occupancy snapshot after every seat lock is acquired. The
	// reservation's event lock prevents a price edit before this quote.
	var salesOpen, startsAt time.Time
	var held bool
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp(), sales_open_at, starts_at, price_minor, currency,
		EXISTS (SELECT 1 FROM reservation_seats WHERE seat_id = ANY($3::uuid[]) AND released_at IS NULL)
		FROM events WHERE tenant_id = $1 AND id = $2`, tenantID, reservation.EventID, seatIDs).
		Scan(&reservation.ServerTime, &salesOpen, &startsAt, &reservation.PriceMinor, &reservation.Currency, &held); err != nil {
		return Result{}, err
	}
	if reservation.ServerTime.Before(salesOpen) || !reservation.ServerTime.Before(startsAt) {
		return failure(409, "sales_closed", "event is not currently on sale"), nil
	}
	if held || slices.ContainsFunc(statuses, func(status string) bool { return status != "AVAILABLE" }) {
		return failure(409, "seat_unavailable", "one or more seats are unavailable"), nil
	}
	reservation.SeatIDs = seatIDs
	reservation.TotalMinor = reservation.PriceMinor * int64(len(seatIDs))
	reservation.ExpiresAt = reservation.ServerTime.Add(5 * time.Minute)
	if _, err := tx.Exec(ctx, `UPDATE reservations SET expires_at = $2 WHERE id = $1`, reservation.ID, reservation.ExpiresAt); err != nil {
		return Result{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO reservation_seats (reservation_id, tenant_id, event_id, seat_id)
		SELECT $1, $2, $3, unnest($4::uuid[])`, reservation.ID, tenantID, reservation.EventID, seatIDs); err != nil {
		return Result{}, err
	}
	if err := audit(ctx, tx, reservation.ID, userID, "", "HELD", "customer_request", key); err != nil {
		return Result{}, err
	}
	return success(201, *reservation), nil
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
		status, reason := "CANCELLED", "customer_request"
		if expired {
			status, reason = "EXPIRED", "deadline_passed"
		}
		if err := release(ctx, tx, id, status, userID, reason); err != nil {
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
	if err := release(ctx, tx, id, "EXPIRED", "", "deadline_passed"); err != nil {
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

// release frees every seat of a held reservation. An empty actor means the system:
// the worker or an on-demand release during another reservation.
func release(ctx context.Context, tx pgx.Tx, id, status, actor, reason string) error {
	if _, err := tx.Exec(ctx, "UPDATE reservation_seats SET released_at = clock_timestamp() WHERE reservation_id = $1 AND released_at IS NULL", id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "UPDATE reservations SET status = $2 WHERE id = $1", id, status); err != nil {
		return err
	}
	return audit(ctx, tx, id, actor, "HELD", status, reason, "")
}

// audit records a transition in the same transaction as the state change, so
// both commit or neither does. Empty actor, from, and key values are stored as NULL.
func audit(ctx context.Context, tx pgx.Tx, id, actor, from, to, reason, key string) error {
	tag, err := tx.Exec(ctx, `INSERT INTO audit_entries
		(tenant_id, event_id, reservation_id, actor_user_id, from_status, to_status, reason, request_key)
		SELECT tenant_id, event_id, id, NULLIF($2, '')::uuid, NULLIF($3, ''), $4, $5, NULLIF($6, '')
		FROM reservations WHERE id = $1`, id, actor, from, to, reason, key)
	if err == nil && tag.RowsAffected() != 1 {
		err = errors.New("audit target reservation not found")
	}
	return err
}

func scan(row pgx.Row) (Reservation, error) {
	var reservation Reservation
	err := row.Scan(&reservation.ID, &reservation.EventID, &reservation.SeatIDs, &reservation.Status,
		&reservation.CreatedAt, &reservation.ExpiresAt, &reservation.ServerTime, &reservation.BookingID,
		&reservation.PriceMinor, &reservation.Currency)
	reservation.TotalMinor = reservation.PriceMinor * int64(len(reservation.SeatIDs))
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
