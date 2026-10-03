package booking

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"ticker/internal/identity"
)

func RegisterRoutes(mux *http.ServeMux, store *Store, sessions *identity.Handler, logger *slog.Logger) {
	mux.Handle("GET /bookings/{bookingID}", sessions.RequireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		fail := func(status int, code, message string) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
		}
		value := r.PathValue("bookingID")
		var id pgtype.UUID
		if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' || id.Scan(value) != nil {
			fail(400, "invalid_booking_id", "booking ID must be a UUID")
			return
		}
		session, _ := identity.CurrentSession(r.Context())
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		receipt, err := store.Get(ctx, session.User.ID, id.String())
		if errors.Is(err, ErrNotFound) {
			fail(404, "booking_not_found", "booking not found")
			return
		}
		if err != nil {
			logger.Error("booking request failed", "path", r.URL.Path, "error", err)
			fail(503, "booking_unavailable", "booking service is temporarily unavailable")
			return
		}
		_ = json.NewEncoder(w).Encode(struct {
			Booking Receipt `json:"booking"`
		}{receipt})
	})))
}
