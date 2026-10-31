package reservation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"ticker/internal/booking"
)

func (s *Store) Confirm(ctx context.Context, userID, key, id string) (Result, error) {
	tx, err := s.begin(ctx)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback(context.Background())
	hash := sha256.Sum256([]byte("confirm/v1/" + id))
	if _, err := tx.Exec(ctx, `INSERT INTO idempotency_records (user_id, operation, key, payload_hash)
        VALUES ($1, 'confirm', $2, $3) ON CONFLICT DO NOTHING`, userID, key, hash[:]); err != nil {
		return Result{}, err
	}
	var storedHash, body []byte
	var savedStatus *int
	if err := tx.QueryRow(ctx, `SELECT payload_hash, response_status, response_body FROM idempotency_records
        WHERE user_id = $1 AND operation = 'confirm' AND key = $2 FOR UPDATE`, userID, key).
		Scan(&storedHash, &savedStatus, &body); err != nil {
		return Result{}, err
	}
	if !bytes.Equal(hash[:], storedHash) {
		return failure(409, "idempotency_key_reused", "request key was already used with a different payload"), nil
	}
	if savedStatus != nil {
		return Result{Status: *savedStatus, Body: body}, nil
	}

	var status string
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `SELECT status, expires_at FROM reservations
        WHERE id = $1 AND user_id = $2 FOR UPDATE`, id, userID).Scan(&status, &expiresAt)
	result := failure(404, "reservation_not_found", "reservation not found")
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Result{}, err
	}
	if err == nil {
		if err := lockSeats(ctx, tx, id); err != nil {
			return Result{}, err
		}
		// Eligibility is decided using the database clock after all inventory locks.
		// Keeping these locks permits a valid confirmation to commit past its deadline.
		var now time.Time
		var ownsSeats bool
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp(),
            (SELECT count(*) = (SELECT quantity FROM reservations WHERE id = $1)
                AND bool_and(rs.released_at IS NULL AND s.status = 'AVAILABLE')
             FROM reservation_seats rs JOIN seats s ON s.id = rs.seat_id
             WHERE rs.reservation_id = $1)`, id).Scan(&now, &ownsSeats); err != nil {
			return Result{}, err
		}
		result = failure(409, "reservation_not_confirmable", "reservation is no longer confirmable")
		if status == "HELD" && now.Before(expiresAt) && ownsSeats {
			var bookingID string
			// The booking keeps the fee rate in force now, so later rate changes affect only new bookings.
			if err := tx.QueryRow(ctx, `INSERT INTO bookings (tenant_id, event_id, reservation_id, user_id, price_minor, currency, fee_bps)
                SELECT r.tenant_id, r.event_id, r.id, r.user_id, e.price_minor, e.currency,
                    coalesce(t.fee_bps, (SELECT fee_bps FROM platform_settings))
                FROM reservations r JOIN events e ON e.id = r.event_id AND e.tenant_id = r.tenant_id
                JOIN tenants t ON t.id = r.tenant_id
                WHERE r.id = $1 RETURNING id::text`, id).Scan(&bookingID); err != nil {
				return Result{}, err
			}
			// Eligibility and lock acquisition are complete. Send these writes in
			// one round trip; PostgreSQL still executes them in this order, in
			// this transaction. Drain every result before reading the receipt.
			var writes pgx.Batch
			writes.Queue(`INSERT INTO booking_seats (booking_id, reservation_id, tenant_id, event_id, seat_id)
                SELECT $2, reservation_id, tenant_id, event_id, seat_id FROM reservation_seats
                WHERE reservation_id = $1 AND released_at IS NULL`, id, bookingID)
			writes.Queue(`UPDATE seats SET status = 'SOLD' WHERE id IN
                (SELECT seat_id FROM booking_seats WHERE booking_id = $1)`, bookingID)
			writes.Queue(`UPDATE reservations SET status = 'CONFIRMED' WHERE id = $1`, id)
			writes.Queue(insertAudit, id, userID, "HELD", "CONFIRMED", "customer_request", key)
			results := tx.SendBatch(ctx, &writes)
			for i := 0; i < writes.Len(); i++ {
				tag, err := results.Exec()
				if err != nil {
					_ = results.Close()
					return Result{}, err
				}
				if i == writes.Len()-1 && tag.RowsAffected() != 1 {
					_ = results.Close()
					return Result{}, errors.New("audit target reservation not found")
				}
			}
			if err := results.Close(); err != nil {
				return Result{}, err
			}
			receipt, err := booking.Read(ctx, tx, userID, bookingID)
			if err != nil {
				return Result{}, err
			}
			body, err := json.Marshal(struct {
				Booking booking.Receipt `json:"booking"`
			}{receipt})
			if err != nil {
				return Result{}, err
			}
			result = Result{Status: 201, Body: body}
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE idempotency_records SET response_status = $3, response_body = $4
        WHERE user_id = $1 AND operation = 'confirm' AND key = $2`, userID, key, result.Status, result.Body); err != nil {
		return Result{}, err
	}
	return result, tx.Commit(ctx)
}
