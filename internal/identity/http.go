package identity

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const cookieName = "ticker_session"

type Options struct {
	PublicOrigin string
	CookieSecure bool
	// Signups is nil when Redis, the signup secret, or a mailer is missing.
	Signups *Signups
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
	mux.Handle("POST /signups/code", h.requireOrigin(http.HandlerFunc(h.requestCode)))
	mux.Handle("POST /signups", h.requireOrigin(http.HandlerFunc(h.signup)))
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
	var payload struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &payload, "invalid_credentials_payload", "provide one JSON object with email and password") {
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
	h.setCookie(w, session, token)
	writeJSON(w, http.StatusCreated, session)
}

// requestCode emails a verification code. Its answer never depends on whether
// the address already has an account, so it cannot be used to discover users.
func (h *Handler) requestCode(w http.ResponseWriter, r *http.Request) {
	signups := h.options.Signups
	if signups == nil {
		writeError(w, http.StatusServiceUnavailable, "signup_unavailable", "signup is not available right now")
		return
	}
	var payload struct {
		Email string `json:"email"`
	}
	if !readJSON(w, r, &payload, "invalid_signup_payload", "provide one JSON object with an email") {
		return
	}
	email, ok := normalizeEmail(payload.Email)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_email", "enter a valid email address")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var wait WaitError
	err := signups.RequestCode(ctx, email)
	switch {
	case errors.As(err, &wait):
		seconds := strconv.Itoa(int(wait.Wait.Round(time.Second).Seconds()))
		w.Header().Set("Retry-After", seconds)
		writeError(w, http.StatusTooManyRequests, "code_wait", "wait "+seconds+"s before requesting another code")
	case err != nil:
		h.logger.Error("signup code failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "signup_unavailable", "unable to send a code right now; try again shortly")
	default:
		writeJSON(w, http.StatusAccepted, map[string]int{
			"expires_in_seconds": int(codeTTL.Seconds()), "resend_after_seconds": int(codeCooldown.Seconds()),
		})
	}
}

// signup creates a customer account once the emailed code is correct, then
// signs it in like a login does.
func (h *Handler) signup(w http.ResponseWriter, r *http.Request) {
	signups := h.options.Signups
	if signups == nil {
		writeError(w, http.StatusServiceUnavailable, "signup_unavailable", "signup is not available right now")
		return
	}
	var payload struct {
		Email       string `json:"email"`
		Code        string `json:"code"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
	}
	if !readJSON(w, r, &payload, "invalid_signup_payload", "provide one JSON object with email, code, display_name, and password") {
		return
	}
	email, ok := normalizeEmail(payload.Email)
	name := strings.TrimSpace(payload.DisplayName)
	switch {
	case !ok:
		writeError(w, http.StatusBadRequest, "invalid_email", "enter a valid email address")
		return
	case !isCode(payload.Code):
		writeError(w, http.StatusBadRequest, "invalid_code", "enter the 6-digit code from the email")
		return
	case name == "" || utf8.RuneCountInString(name) > 80 || strings.IndexFunc(name, unicode.IsControl) >= 0:
		writeError(w, http.StatusBadRequest, "invalid_display_name", "enter a name of up to 80 characters")
		return
	case len(payload.Password) < 12 || len(payload.Password) > 72:
		writeError(w, http.StatusBadRequest, "invalid_password", "use a password of 12 to 72 bytes")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var wrong WrongCodeError
	if err := signups.Verify(ctx, email, payload.Code); errors.As(err, &wrong) {
		writeError(w, http.StatusBadRequest, "invalid_code", "that code is incorrect; "+plural(wrong.Left, "attempt")+" left")
		return
	} else if errors.Is(err, ErrCodeExpired) {
		writeError(w, http.StatusBadRequest, "code_expired", "this code has expired or was tried too many times; request a new one")
		return
	} else if err != nil {
		h.logger.Error("signup code check failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "signup_unavailable", "unable to check the code right now; try again shortly")
		return
	}
	session, token, err := h.service.CreateAccount(ctx, email, name, payload.Password, requestToken(r))
	if err != nil && !errors.Is(err, ErrAccount) {
		h.serviceError(w, r, err)
		return
	}
	// Either way the code has done its job: it proved the address is theirs.
	if err := signups.Consume(context.WithoutCancel(ctx), email); err != nil {
		h.logger.Warn("signup code not deleted; it expires on its own", "error", err)
	}
	if errors.Is(err, ErrAccount) {
		// Only the inbox owner gets this far, so saying so reveals nothing.
		writeError(w, http.StatusConflict, "account_exists", "an account already uses this email; log in instead")
		return
	}
	h.setCookie(w, session, token)
	writeJSON(w, http.StatusCreated, session)
}

func normalizeEmail(raw string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(raw))
	address, err := mail.ParseAddress(email)
	return email, err == nil && address.Name == "" && address.Address == email && len(email) <= 254
}

func isCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

// readJSON decodes exactly one small JSON object, writing the error response itself.
func readJSON(w http.ResponseWriter, r *http.Request, dst any, code, message string) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "use application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(dst) != nil || decoder.Decode(new(any)) != io.EOF {
		writeError(w, http.StatusBadRequest, code, message)
		return false
	}
	return true
}

func (h *Handler) setCookie(w http.ResponseWriter, session Session, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: token, Path: "/", HttpOnly: true, Secure: h.options.CookieSecure,
		SameSite: http.SameSiteLaxMode, Expires: session.ExpiresAt, MaxAge: 24 * 60 * 60,
	})
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
		writeError(w, http.StatusTooManyRequests, "login_busy", "the server is busy; try again shortly")
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
