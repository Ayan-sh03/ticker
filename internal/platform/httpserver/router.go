package httpserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"ticker/internal/booking"
	"ticker/internal/catalog"
	"ticker/internal/identity"
	"ticker/internal/organizer"
	"ticker/internal/platform/ratelimit"
	"ticker/internal/reservation"
)

// Router limits requests before any handler touches PostgreSQL. A nil limiter disables limits.
func Router(pool *pgxpool.Pool, logger *slog.Logger, options identity.Options, metrics bool, limiter *ratelimit.Limiter) (http.Handler, error) {
	sessions, err := identity.NewService(pool)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, "ok")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			respond(w, http.StatusServiceUnavailable, "unavailable")
			return
		}
		respond(w, http.StatusOK, "ready")
	})
	if metrics {
		mux.Handle("GET /metrics", promhttp.Handler())
	}
	catalog.RegisterRoutes(mux, catalog.NewStore(pool), logger)
	identityHandler := identity.RegisterRoutes(mux, sessions, logger, options)
	organizer.RegisterRoutes(mux, organizer.NewStore(pool), identityHandler, logger)
	reservation.RegisterRoutes(mux, reservation.NewStore(pool), identityHandler, limiter, logger)
	booking.RegisterRoutes(mux, booking.NewStore(pool), identityHandler, logger)
	return limiter.Middleware(mux), nil
}

func respond(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": message})
}
