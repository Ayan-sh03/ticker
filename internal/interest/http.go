package interest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"ticker/internal/identity"
)

// RegisterRoutes adds GET, PUT, and DELETE /events/{eventID}/interest. Each
// returns {"interested": bool}; PUT and DELETE take an empty body.
func RegisterRoutes(mux *http.ServeMux, store *Store, sessions *identity.Handler, logger *slog.Logger) {
	handle := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fail := func(status int, code, message string) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
		}
		value := r.PathValue("eventID")
		var id pgtype.UUID
		if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' || id.Scan(value) != nil {
			fail(http.StatusBadRequest, "invalid_event_id", "event ID must be a UUID")
			return
		}
		// Only PUT and DELETE write. GET and HEAD, which Go routes to GET, read.
		write := r.Method == http.MethodPut || r.Method == http.MethodDelete
		if write {
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			if body, err := io.ReadAll(r.Body); err != nil || len(body) != 0 {
				fail(http.StatusBadRequest, "invalid_interest_payload", "interest changes take an empty body")
				return
			}
		}
		session, _ := identity.CurrentSession(r.Context())
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		interested := r.Method == http.MethodPut
		var err error
		if write {
			err = store.Set(ctx, session.User.ID, id.String(), interested)
		} else {
			interested, err = store.Get(ctx, session.User.ID, id.String())
		}
		if errors.Is(err, ErrNotFound) {
			fail(http.StatusNotFound, "event_not_found", "event not found")
			return
		}
		if err != nil {
			logger.Error("interest request failed", "path", r.URL.Path, "error", err)
			fail(http.StatusServiceUnavailable, "interest_unavailable", "interest is temporarily unavailable")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"interested": interested})
	}
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		mux.Handle(method+" /events/{eventID}/interest", sessions.RequireUser(http.HandlerFunc(handle)))
	}
}
