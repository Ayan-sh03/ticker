package reservation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/rand/v2"
	"mime"
	"net/http"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"ticker/internal/booking"
	"ticker/internal/identity"
	"ticker/internal/platform/ratelimit"
)

type Handler struct {
	store  *Store
	logger *slog.Logger
}

func RegisterRoutes(mux *http.ServeMux, store *Store, sessions *identity.Handler, limiter *ratelimit.Limiter, logger *slog.Logger) {
	h := &Handler{store: store, logger: logger}
	mux.Handle("POST /reservations", sessions.RequireUser(limiter.Account("reserve", h.reserve)))
	mux.Handle("GET /reservations/{reservationID}", sessions.RequireUser(http.HandlerFunc(h.get)))
	mux.Handle("DELETE /reservations/{reservationID}", sessions.RequireUser(http.HandlerFunc(h.cancel)))
	mux.Handle("POST /reservations/{reservationID}/confirm", sessions.RequireUser(limiter.Account("confirm", h.confirm)))
	mux.Handle("POST /bookings/{bookingID}/cancel", sessions.RequireUser(limiter.Account("cancel_booking", h.cancelBooking)))
}

// cancelBooking needs no request key: repeating it changes nothing.
func (h *Handler) cancelBooking(w http.ResponseWriter, r *http.Request) {
	id, ok := canonicalID(r.PathValue("bookingID"))
	if !ok {
		writeResult(w, failure(400, "invalid_booking_id", "booking ID must be a UUID"))
		return
	}
	session, _ := identity.CurrentSession(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	receipt, err := retryLocks(ctx, func() (booking.Receipt, error) { return h.store.CancelOwnBooking(ctx, session.User.ID, id) })
	switch {
	case errors.Is(err, booking.ErrNotFound):
		writeResult(w, failure(404, "booking_not_found", "booking not found"))
	case errors.Is(err, ErrEventStarted):
		writeResult(w, failure(409, "event_started", "bookings cannot be cancelled once the event has started"))
	case err != nil:
		h.logger.Error("booking cancellation failed", "path", r.URL.Path, "error", err)
		w.Header().Set("Retry-After", "1")
		writeResult(w, failure(503, "booking_unavailable", "the cancellation may not have been saved; retrying is safe"))
	default:
		body, _ := json.Marshal(struct {
			Booking booking.Receipt `json:"booking"`
		}{receipt})
		writeResult(w, Result{Status: 200, Body: body})
	}
}

func (h *Handler) confirm(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if len(r.Header.Values("Idempotency-Key")) != 1 || len(key) == 0 || len(key) > 128 {
		writeResult(w, failure(400, "invalid_request_key", "provide one Idempotency-Key header containing 1–128 visible ASCII characters"))
		return
	}
	for _, char := range key {
		if char < '!' || char > '~' {
			writeResult(w, failure(400, "invalid_request_key", "request key must contain only visible ASCII characters"))
			return
		}
	}
	id, ok := canonicalID(r.PathValue("reservationID"))
	if !ok {
		writeResult(w, failure(400, "invalid_reservation_id", "reservation ID must be a UUID"))
		return
	}
	// Confirmation has no client-supplied ownership or pricing fields. The
	// normalized reservation path is the complete idempotency payload.
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil || len(body) != 0 {
		writeResult(w, failure(400, "invalid_confirmation_payload", "confirmation requires an empty body"))
		return
	}
	session, _ := identity.CurrentSession(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	result, err := retryLocks(ctx, func() (Result, error) { return h.store.Confirm(ctx, session.User.ID, key, id) })
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	writeResult(w, result)
}

func (h *Handler) reserve(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if len(r.Header.Values("Idempotency-Key")) != 1 || len(key) == 0 || len(key) > 128 {
		writeResult(w, failure(400, "invalid_request_key", "provide one Idempotency-Key header containing 1–128 visible ASCII characters"))
		return
	}
	for _, char := range key {
		if char < '!' || char > '~' {
			writeResult(w, failure(400, "invalid_request_key", "request key must contain only visible ASCII characters"))
			return
		}
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeResult(w, failure(415, "invalid_content_type", "use application/json"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	defer r.Body.Close()
	var payload struct {
		EventID string   `json:"event_id"`
		SeatIDs []string `json:"seat_ids"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || decoder.Decode(new(any)) != io.EOF {
		writeResult(w, failure(400, "invalid_reservation_payload", "provide one JSON object with event_id and seat_ids"))
		return
	}
	eventID, ok := canonicalID(payload.EventID)
	if !ok || len(payload.SeatIDs) < 1 || len(payload.SeatIDs) > 6 {
		writeResult(w, failure(400, "invalid_reservation_payload", "provide an event_id UUID and 1–6 seat_ids"))
		return
	}
	seatIDs := make([]string, len(payload.SeatIDs))
	for i, value := range payload.SeatIDs {
		if seatIDs[i], ok = canonicalID(value); !ok || slices.Contains(seatIDs[:i], seatIDs[i]) {
			writeResult(w, failure(400, "invalid_reservation_payload", "seat_ids must be distinct UUIDs"))
			return
		}
	}
	session, _ := identity.CurrentSession(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	result, err := retryLocks(ctx, func() (Result, error) { return h.store.Reserve(ctx, session.User.ID, key, eventID, seatIDs) })
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	writeResult(w, result)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) { h.readOrCancel(w, r, false) }

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) { h.readOrCancel(w, r, true) }

func (h *Handler) readOrCancel(w http.ResponseWriter, r *http.Request, cancelHold bool) {
	id, ok := canonicalID(r.PathValue("reservationID"))
	if !ok {
		writeResult(w, failure(400, "invalid_reservation_id", "reservation ID must be a UUID"))
		return
	}
	session, _ := identity.CurrentSession(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	var reservation Reservation
	var err error
	if cancelHold {
		reservation, err = retryLocks(ctx, func() (Reservation, error) { return h.store.Cancel(ctx, session.User.ID, id) })
	} else {
		reservation, err = h.store.Get(ctx, session.User.ID, id)
	}
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	writeResult(w, success(200, reservation))
}

func (h *Handler) storeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrNotFound) {
		writeResult(w, failure(404, "reservation_not_found", "reservation not found"))
		return
	}
	h.logger.Error("reservation request failed", "path", r.URL.Path, "error", err)
	w.Header().Set("Retry-After", "1")
	writeResult(w, failure(503, "reservation_unavailable", "retry with the same request key; reservation service is temporarily unavailable"))
}

func canonicalID(value string) (string, bool) {
	var id pgtype.UUID
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' || id.Scan(value) != nil {
		return "", false
	}
	return id.String(), true
}

func writeResult(w http.ResponseWriter, result Result) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(result.Status)
	_, _ = w.Write(result.Body)
}

// retryLocks reruns a transaction that lost a lock wait or a deadlock. The whole
// transaction rolled back, including its request-key row, so rerunning it with
// the same key is safe. By the retry the lock holder has usually committed, so
// the caller gets the real outcome, often a 409, instead of a 503.
func retryLocks[T any](ctx context.Context, run func() (T, error)) (T, error) {
	for attempt := 1; ; attempt++ {
		value, err := run()
		var pgErr *pgconn.PgError
		if err == nil || attempt > 2 || !errors.As(err, &pgErr) ||
			(pgErr.Code != "55P03" && pgErr.Code != "40P01") { // lock_not_available, deadlock_detected
			return value, err
		}
		// Jitter keeps retrying contenders from colliding again in lockstep.
		delay := time.Duration(attempt*25+rand.IntN(25)) * time.Millisecond
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return value, err
		}
	}
}
