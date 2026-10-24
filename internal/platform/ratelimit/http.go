package ratelimit

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ticker/internal/identity"
)

const deviceCookie = "ticker_device"

// class is the budget for one kind of request, per minute. A browser with a
// device cookie gets the device budget; a client without one, such as a script
// that drops cookies, shares a tighter fingerprint budget. The network ceiling
// is only a flood guard, high enough for an office behind one address.
type class struct {
	name                         string
	device, fingerprint, network int
	shared                       bool
}

var (
	// UI polling is about 60 reads per minute per open tab.
	reads  = class{name: "read", device: 300, fingerprint: 120, network: 6000}
	logins = class{name: "login", device: 10, fingerprint: 5, network: 300, shared: true}
	// A code request sends an email, so signup gets the tightest budget.
	signups = class{name: "signup", device: 5, fingerprint: 3, network: 60, shared: true}
	writes  = class{name: "write", device: 60, fingerprint: 20, network: 1200, shared: true}
	// Reserve and confirm are also limited per account, across all devices.
	accountWrites = 20
)

// Middleware limits every request except health and metrics checks.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	if l == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz", "/metrics":
			next.ServeHTTP(w, r)
			return
		}
		c := reads
		if r.Method == http.MethodPost && r.URL.Path == "/sessions" {
			c = logins
		} else if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/signups") {
			c = signups
		} else if r.Method != http.MethodGet && r.Method != http.MethodHead {
			c = writes
		}
		client, limit := "fp:"+l.fingerprint(r), c.fingerprint
		if id, ok := l.device(w, r); ok {
			client, limit = "dev:"+id, c.device
		}
		keys := []key{
			{rule: c.name, id: client, limit: limit, window: time.Minute},
			{rule: c.name, id: "net:" + l.network(r), limit: c.network, window: time.Minute},
		}
		if wait, ok := l.allow(r.Context(), c.shared, keys); !ok {
			reject(w, wait)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Account limits an authenticated handler per account. It must run inside
// identity.RequireUser so the session is known.
func (l *Limiter) Account(rule string, next http.HandlerFunc) http.HandlerFunc {
	if l == nil {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		session, _ := identity.CurrentSession(r.Context())
		k := key{rule: rule, id: "acct:" + session.User.ID, limit: accountWrites, window: time.Minute}
		if wait, ok := l.allow(r.Context(), true, []key{k}); !ok {
			reject(w, wait)
			return
		}
		next(w, r)
	}
}

// device returns the signed device ID, issuing a new cookie when it is missing
// or forged. Only server-issued IDs earn the device budget, so a client cannot
// mint fresh budgets by inventing cookie values.
func (l *Limiter) device(w http.ResponseWriter, r *http.Request) (string, bool) {
	if cookie, err := r.Cookie(deviceCookie); err == nil {
		if id, tag, ok := strings.Cut(cookie.Value, "."); ok && hmac.Equal([]byte(tag), []byte(l.sign(id))) {
			return id, true
		}
	}
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	id := base64.RawURLEncoding.EncodeToString(raw)
	http.SetCookie(w, &http.Cookie{
		Name: deviceCookie, Value: id + "." + l.sign(id), Path: "/", MaxAge: 365 * 24 * 3600,
		HttpOnly: true, Secure: l.secure, SameSite: http.SameSiteLaxMode,
	})
	// This request has not proved it can keep a cookie yet.
	return "", false
}

func (l *Limiter) sign(value string) string {
	mac := hmac.New(sha256.New, l.secret)
	mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:16])
}

// fingerprint groups cookie-less clients by address and browser traits, so two
// different browsers behind one office address get separate budgets.
func (l *Limiter) fingerprint(r *http.Request) string {
	mac := hmac.New(sha256.New, l.secret)
	for _, part := range []string{l.clientIP(r).String(), r.UserAgent(), r.Header.Get("Accept-Language"),
		r.Header.Get("Sec-CH-UA"), r.Header.Get("Sec-CH-UA-Platform")} {
		mac.Write([]byte(part))
		mac.Write([]byte{0})
	}
	return hex.EncodeToString(mac.Sum(nil)[:12])
}

// network is the flood-ceiling bucket. IPv6 clients can rotate addresses inside
// a /64, so they share one bucket per /64.
func (l *Limiter) network(r *http.Request) string {
	ip := l.clientIP(r)
	if ip.To4() == nil {
		ip = ip.Mask(net.CIDRMask(64, 128))
	}
	return ip.String()
}

// clientIP trusts only the header that the deployment's own proxy sets: nginx
// in Compose and the Vercel edge both overwrite X-Real-IP. Without a configured
// header, the TCP peer address is used.
func (l *Limiter) clientIP(r *http.Request) net.IP {
	if l.ipHead != "" {
		if ip := net.ParseIP(strings.TrimSpace(r.Header.Get(l.ipHead))); ip != nil {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(host); err == nil && ip != nil {
		return ip
	}
	return net.IPv4zero
}

func reject(w http.ResponseWriter, wait time.Duration) {
	seconds := int(math.Ceil(wait.Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
		"code": "rate_limited", "message": "Too many requests. Try again in " + strconv.Itoa(seconds) + "s.",
	}})
}
