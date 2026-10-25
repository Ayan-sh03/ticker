package organizer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"ticker/internal/identity"
)

type Handler struct {
	store  *Store
	logger *slog.Logger
}

func RegisterRoutes(mux *http.ServeMux, store *Store, sessions *identity.Handler, logger *slog.Logger) {
	h := &Handler{store: store, logger: logger}
	mux.Handle("GET /organizer/tenants", sessions.RequireUser(http.HandlerFunc(h.listTenants)))
	mux.Handle("POST /organizer/tenants/{tenantID}/events", sessions.RequireUser(http.HandlerFunc(h.createEvent)))
	mux.Handle("GET /organizer/tenants/{tenantID}/events", sessions.RequireUser(http.HandlerFunc(h.listEvents)))
	mux.Handle("GET /organizer/tenants/{tenantID}/events/{eventID}", sessions.RequireUser(http.HandlerFunc(h.getEvent)))
	mux.Handle("PUT /organizer/tenants/{tenantID}/events/{eventID}", sessions.RequireUser(http.HandlerFunc(h.updateEvent)))
	mux.Handle("DELETE /organizer/tenants/{tenantID}/events/{eventID}", sessions.RequireUser(http.HandlerFunc(h.deleteEvent)))
	mux.Handle("POST /organizer/tenants/{tenantID}/events/{eventID}/publish", sessions.RequireUser(http.HandlerFunc(h.publishEvent)))
	mux.Handle("GET /organizer/tenants/{tenantID}/events/{eventID}/seats", sessions.RequireUser(http.HandlerFunc(h.listSeats)))
	mux.Handle("PATCH /organizer/tenants/{tenantID}/events/{eventID}/seats", sessions.RequireUser(http.HandlerFunc(h.setSeatStatus)))
}

func (h *Handler) listTenants(w http.ResponseWriter, r *http.Request) {
	session, _ := identity.CurrentSession(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	tenants, err := h.store.ListTenants(ctx, session.User.ID)
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenants": tenants})
}

func (h *Handler) createEvent(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := pathID(w, r, "tenantID")
	if !ok {
		return
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "use application/json")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	defer r.Body.Close()
	var payload *DraftInput
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || payload == nil || decoder.Decode(new(any)) != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_event_payload", "provide one JSON object containing event details")
		return
	}
	session, _ := identity.CurrentSession(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	event, err := h.store.CreateEvent(ctx, session.User.ID, tenantID, *payload)
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"event": event})
}

func (h *Handler) listEvents(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := pathID(w, r, "tenantID")
	if !ok {
		return
	}
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
	session, _ := identity.CurrentSession(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	events, err := h.store.ListEvents(ctx, session.User.ID, tenantID, limit, offset)
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "limit": limit, "offset": offset})
}

func (h *Handler) getEvent(w http.ResponseWriter, r *http.Request) { h.readOrPublish(w, r, false) }

func (h *Handler) updateEvent(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := pathID(w, r, "tenantID")
	if !ok {
		return
	}
	eventID, ok := pathID(w, r, "eventID")
	if !ok {
		return
	}
	var payload EditInput
	if !decodeMutation(w, r, &payload) {
		return
	}
	if payload.ExpectedVersion == nil || *payload.ExpectedVersion < 1 {
		writeError(w, http.StatusBadRequest, "invalid_event_version", "provide a positive expected_version from the event you loaded")
		return
	}
	session, _ := identity.CurrentSession(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	event, err := h.store.UpdateEvent(ctx, session.User.ID, tenantID, eventID, payload)
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"event": event})
}

func (h *Handler) deleteEvent(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := pathID(w, r, "tenantID")
	if !ok {
		return
	}
	eventID, ok := pathID(w, r, "eventID")
	if !ok {
		return
	}
	var payload DeleteInput
	if !decodeMutation(w, r, &payload) {
		return
	}
	if payload.ExpectedVersion == nil || *payload.ExpectedVersion < 1 {
		writeError(w, http.StatusBadRequest, "invalid_event_version", "provide a positive expected_version from the event you loaded")
		return
	}
	session, _ := identity.CurrentSession(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := h.store.DeleteEvent(ctx, session.User.ID, tenantID, eventID, payload); err != nil {
		h.storeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func decodeMutation(w http.ResponseWriter, r *http.Request, payload any) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "use application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(payload) != nil || decoder.Decode(new(any)) != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_event_payload", "provide one JSON object containing event details and expected_version")
		return false
	}
	return true
}

func (h *Handler) publishEvent(w http.ResponseWriter, r *http.Request) { h.readOrPublish(w, r, true) }

func (h *Handler) readOrPublish(w http.ResponseWriter, r *http.Request, publish bool) {
	tenantID, ok := pathID(w, r, "tenantID")
	if !ok {
		return
	}
	eventID, ok := pathID(w, r, "eventID")
	if !ok {
		return
	}
	if publish {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) != 0 {
			writeError(w, http.StatusBadRequest, "invalid_publication_payload", "publication requires an empty body")
			return
		}
	}
	session, _ := identity.CurrentSession(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	var event Event
	var err error
	if publish {
		event, err = h.store.PublishEvent(ctx, session.User.ID, tenantID, eventID)
	} else {
		event, err = h.store.GetEvent(ctx, session.User.ID, tenantID, eventID)
	}
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"event": event})
}

func (h *Handler) storeError(w http.ResponseWriter, r *http.Request, err error) {
	var conflict *SeatConflictError
	switch {
	case errors.Is(err, ErrForbidden):
		writeError(w, http.StatusForbidden, "organizer_required", "organizer membership for this tenant is required")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "event_not_found", "event not found")
	case errors.Is(err, ErrInvalidEvent):
		writeError(w, http.StatusUnprocessableEntity, "invalid_event", "provide a title and venue up to 200 characters, description up to 5000, a nonnegative safe integer price, three-letter uppercase currency, sales before a future start, and 1–100 rows and columns")
	case errors.Is(err, ErrLayoutExists):
		writeError(w, http.StatusConflict, "draft_layout_exists", "draft already has seats and cannot be published by this operation")
	case errors.Is(err, ErrVersionConflict):
		writeError(w, http.StatusConflict, "event_version_conflict", "event changed since it was loaded; reload the latest event before saving")
	case errors.Is(err, ErrFrozenLayout):
		writeError(w, http.StatusConflict, "published_layout_frozen", "price, currency, and layout are locked after the first reservation")
	case errors.Is(err, ErrLayoutConflict):
		writeError(w, http.StatusConflict, "event_layout_conflict", "layout change conflicts with existing seats")
	case errors.As(err, &conflict):
		writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": "seat_unavailable",
			"message": "sold or held seats cannot be blocked or unblocked", "seat_ids": conflict.SeatIDs}})
	case errors.Is(err, ErrNotPublished):
		writeError(w, http.StatusConflict, "event_not_published", "publish the event before changing seat availability")
	case errors.Is(err, ErrSeatNotFound):
		writeError(w, http.StatusNotFound, "seat_not_found", "every seat must belong to this event")
	case errors.Is(err, ErrHasReservations):
		writeError(w, http.StatusConflict, "event_has_reservations", "events with reservation history cannot be deleted")
	default:
		h.logger.Error("organizer request failed", "path", r.URL.Path, "error", err)
		writeError(w, http.StatusServiceUnavailable, "organizer_unavailable", "organizer service is temporarily unavailable")
	}
}

func pathID(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	value := r.PathValue(name)
	var id pgtype.UUID
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' || id.Scan(value) != nil {
		writeError(w, http.StatusBadRequest, "invalid_path_id", "tenant and event IDs must be UUIDs")
		return "", false
	}
	return id.String(), true
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
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (h *Handler) listSeats(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := pathID(w, r, "tenantID")
	if !ok {
		return
	}
	eventID, ok := pathID(w, r, "eventID")
	if !ok {
		return
	}
	session, _ := identity.CurrentSession(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	seats, err := h.store.ListSeats(ctx, session.User.ID, tenantID, eventID)
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"seats": seats})
}

// setSeatStatus blocks or unblocks up to 200 seats at once, all or none.
func (h *Handler) setSeatStatus(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := pathID(w, r, "tenantID")
	if !ok {
		return
	}
	eventID, ok := pathID(w, r, "eventID")
	if !ok {
		return
	}
	var payload SeatInput
	if !decodeMutation(w, r, &payload) {
		return
	}
	valid := (payload.Status == "BLOCKED" || payload.Status == "AVAILABLE") && len(payload.SeatIDs) >= 1 && len(payload.SeatIDs) <= 200
	seen := make(map[string]bool, len(payload.SeatIDs))
	for i, value := range payload.SeatIDs {
		var id pgtype.UUID
		if !valid || len(value) != 36 || id.Scan(value) != nil || seen[id.String()] {
			valid = false
			break
		}
		payload.SeatIDs[i] = id.String()
		seen[payload.SeatIDs[i]] = true
	}
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_seat_payload", "provide status BLOCKED or AVAILABLE and 1–200 distinct seat_ids")
		return
	}
	session, _ := identity.CurrentSession(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	seats, err := h.store.SetSeatStatus(ctx, session.User.ID, tenantID, eventID, payload)
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"seats": seats})
}
