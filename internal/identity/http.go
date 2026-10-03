package identity

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"
)

const cookieName = "ticker_session"

type Options struct {
	PublicOrigin string
	CookieSecure bool
}

type Handler struct {
	service *Service
	logger  *slog.Logger
	options Options
}

type sessionKey struct{}

func CurrentSession(ctx context.Context) (Session, bool) {
	session, ok := ctx.Value(sessionKey{}).(Session)
	return session, ok
}

func RegisterRoutes(mux *http.ServeMux, service *Service, logger *slog.Logger, options Options) *Handler {
	h := &Handler{service: service, logger: logger, options: options}
	mux.Handle("POST /sessions", h.requireOrigin(http.HandlerFunc(h.login)))
	mux.Handle("DELETE /sessions/current", h.requireOrigin(http.HandlerFunc(h.logout)))
	mux.Handle("GET /me", h.RequireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, _ := CurrentSession(r.Context())
		writeJSON(w, http.StatusOK, session)
	})))
	return h
}

func (h *Handler) RequireUser(next http.Handler) http.Handler {
	protected := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		session, err := h.service.Lookup(ctx, requestToken(r))
		if err != nil {
			if errors.Is(err, ErrSession) {
				h.clearCookie(w)
			}
			h.serviceError(w, r, err)
			return
		}
		// This optional assertion restricts the cookie's actor; it never grants
		// access. It prevents another tab's login from changing a UI request owner.
		if expected := r.Header.Get("X-Ticker-User"); expected != "" && expected != session.User.ID {
			writeError(w, http.StatusUnauthorized, "account_changed", "the signed-in account changed; reload before continuing")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		// The lookup timeout does not shorten the downstream handler's request budget.
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, session)))
	})
	return h.requireOrigin(protected)
}

func (h *Handler) requireOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			origin := r.Header.Get("Origin")
			if r.Header.Get("X-Ticker-Request") != "1" ||
				(origin != "" && origin != h.options.PublicOrigin) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				writeError(w, http.StatusForbidden, "invalid_request_origin", "request origin or application header is invalid")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "use application/json")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	defer r.Body.Close()
	var payload struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || decoder.Decode(new(any)) != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_credentials_payload", "provide one JSON object with email and password")
		return
	}
	payload.Email = strings.ToLower(strings.TrimSpace(payload.Email))
	if len(payload.Email) == 0 || len(payload.Email) > 254 || len(payload.Password) == 0 || len(payload.Password) > 72 {
		writeError(w, http.StatusBadRequest, "invalid_credentials_payload", "email and password are required and must fit their length limits")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	session, token, err := h.service.Login(ctx, payload.Email, payload.Password, requestToken(r))
	if err != nil {
		h.serviceError(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: token, Path: "/", HttpOnly: true, Secure: h.options.CookieSecure,
		SameSite: http.SameSiteLaxMode, Expires: session.ExpiresAt, MaxAge: 24 * 60 * 60,
	})
	writeJSON(w, http.StatusCreated, session)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if expected := r.Header.Get("X-Ticker-User"); expected != "" {
		session, err := h.service.Lookup(ctx, requestToken(r))
		if errors.Is(err, ErrSession) {
			h.clearCookie(w)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err != nil {
			h.serviceError(w, r, err)
			return
		}
		if session.User.ID != expected {
			writeError(w, http.StatusUnauthorized, "account_changed", "the signed-in account changed; reload before continuing")
			return
		}
	}
	if err := h.service.Revoke(ctx, requestToken(r)); err != nil {
		h.serviceError(w, r, err)
		return
	}
	h.clearCookie(w)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", HttpOnly: true, Secure: h.options.CookieSecure,
		SameSite: http.SameSiteLaxMode, Expires: time.Unix(1, 0), MaxAge: -1,
	})
}

func (h *Handler) serviceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrCredentials):
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "email or password is incorrect")
	case errors.Is(err, ErrSession):
		writeError(w, http.StatusUnauthorized, "authentication_required", "a valid session is required")
	case errors.Is(err, ErrBusy):
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests, "login_busy", "try logging in again shortly")
	default:
		h.logger.Error("identity request failed", "path", r.URL.Path, "error", err)
		writeError(w, http.StatusServiceUnavailable, "identity_unavailable", "identity service is temporarily unavailable")
	}
}

func requestToken(r *http.Request) string {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
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
