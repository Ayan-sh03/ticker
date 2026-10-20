package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"ticker/internal/identity"
	"ticker/internal/platform/config"
	"ticker/internal/platform/database"
	"ticker/internal/platform/httpserver"
	"ticker/internal/platform/ratelimit"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("api stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pool, err := database.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	limiter, closeLimiter, err := newLimiter(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer closeLimiter()
	router, err := httpserver.Router(pool, logger, identity.Options{
		PublicOrigin: cfg.PublicOrigin, CookieSecure: cfg.CookieSecure,
	}, cfg.Metrics, limiter)
	if err != nil {
		return err
	}
	if cfg.BasePath != "" {
		router = http.StripPrefix(cfg.BasePath, router)
	}
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("api listening", "address", cfg.HTTPAddr)
		serverErrors <- server.ListenAndServe()
	}()
	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		logger.Info("api shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return err
		}
		return nil
	}
}

// newLimiter returns nil when limits are disabled, for example during benchmarks.
// An unreachable Redis is logged, not fatal: shared limits then fall back to memory.
func newLimiter(ctx context.Context, cfg config.Config, logger *slog.Logger) (*ratelimit.Limiter, func(), error) {
	if !cfg.RateLimits {
		logger.Warn("rate limits disabled")
		return nil, func() {}, nil
	}
	if len(cfg.RateLimitSecret) < 32 {
		return nil, nil, errors.New("RATE_LIMIT_SECRET must be at least 32 characters when rate limits are enabled")
	}
	var client *redis.Client
	if cfg.RedisURL != "" {
		options, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			return nil, nil, errors.New("REDIS_URL is not a valid Redis URL")
		}
		options.PoolSize = 4
		options.DialTimeout = time.Second
		options.ReadTimeout = 150 * time.Millisecond
		options.WriteTimeout = 150 * time.Millisecond
		client = redis.NewClient(options)
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err := client.Ping(pingCtx).Err(); err != nil {
			logger.Warn("redis unreachable; shared rate limits will use memory until it recovers", "error", err)
		}
	} else {
		logger.Warn("REDIS_URL not set; rate limits are per process")
	}
	limiter := ratelimit.New(client, []byte(cfg.RateLimitSecret), cfg.ClientIPHeader, cfg.CookieSecure, logger)
	return limiter, func() {
		if client != nil {
			_ = client.Close()
		}
	}, nil
}
