package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

type Handler struct {
	store  *Store
	logger *slog.Logger
}

func RegisterRoutes(mux *http.ServeMux, store *Store, logger *slog.Logger) {
	h := &Handler{store: store, logger: logger}
	mux.HandleFunc("GET /events", h.listEvents)
	mux.HandleFunc("GET /events/{eventID}", h.getEvent)
	mux.HandleFunc("GET /events/{eventID}/seats", h.listSeats)
}

func (h *Handler) listEvents(w http.ResponseWriter, r *http.Request) {
	limit, err := pageNumber(r, "limit", 20, 1, 100)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_pagination", "limit must be an integer from 1 to 100")
		return
	}
	offset, err := pageNumber(r, "offset", 0, 0, 10000)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_pagination", "offset must be an integer from 0 to 10000")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	events, err := h.store.ListEvents(ctx, limit, offset)
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Events []Event `json:"events"`
		Limit  int     `json:"limit"`
		Offset int     `json:"offset"`
	}{events, limit, offset})
}

func (h *Handler) getEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := eventID(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	event, err := h.store.GetEvent(ctx, id)
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Event Event `json:"event"`
	}{event})
}

func (h *Handler) listSeats(w http.ResponseWriter, r *http.Request) {
	id, ok := eventID(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	seats, err := h.store.ListSeats(ctx, id)
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	// Published events always have seats, so an empty snapshot means the event
	// is missing or unpublished; this replaces a separate existence lookup.
	if len(seats) == 0 {
		h.storeError(w, r, ErrNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		EventID string `json:"event_id"`
		Seats   []Seat `json:"seats"`
	}{id, seats})
}

func (h *Handler) storeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "event_not_found", "published event not found")
		return
	}
	h.logger.Error("catalog request failed", "path", r.URL.Path, "error", err)
	writeError(w, http.StatusServiceUnavailable, "catalog_unavailable", "catalog is temporarily unavailable")
}

func eventID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("eventID")
	var uuid pgtype.UUID
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' || uuid.Scan(id) != nil {
		writeError(w, http.StatusBadRequest, "invalid_event_id", "event ID must be a UUID")
		return "", false
	}
	return id, true
}

func pageNumber(r *http.Request, name string, fallback, minimum, maximum int) (int, error) {
	values, present := r.URL.Query()[name]
	if !present {
		return fallback, nil
	}
	if len(values) != 1 {
		return 0, errors.New("duplicate pagination parameter")
	}
	value, err := strconv.Atoi(values[0])
	if err != nil || value < minimum || value > maximum {
		return 0, errors.New("invalid pagination value")
	}
	return value, nil
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
