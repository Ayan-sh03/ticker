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
	client, err := newRedis(ctx, cfg, logger)
	if err != nil {
		return err
	}
	if client != nil {
		defer client.Close()
	}
	limiter, err := newLimiter(cfg, client, logger)
	if err != nil {
		return err
	}
	router, err := httpserver.Router(pool, logger, identity.Options{
		PublicOrigin: cfg.PublicOrigin, CookieSecure: cfg.CookieSecure, Signups: newSignups(cfg, client, logger),
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

// newRedis returns nil without REDIS_URL. An unreachable Redis is logged, not
// fatal: shared limits fall back to memory and signup waits for it to recover.
func newRedis(ctx context.Context, cfg config.Config, logger *slog.Logger) (*redis.Client, error) {
	if cfg.RedisURL == "" {
		return nil, nil
	}
	options, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, errors.New("REDIS_URL is not a valid Redis URL")
	}
	options.PoolSize = 4
	options.DialTimeout = time.Second
	options.ReadTimeout = 150 * time.Millisecond
	options.WriteTimeout = 150 * time.Millisecond
	client := redis.NewClient(options)
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		logger.Warn("redis unreachable; rate limits use memory and signup is unavailable until it recovers", "error", err)
	}
	return client, nil
}

// newLimiter returns nil when limits are disabled, for example during benchmarks.
func newLimiter(cfg config.Config, client *redis.Client, logger *slog.Logger) (*ratelimit.Limiter, error) {
	if !cfg.RateLimits {
		logger.Warn("rate limits disabled")
		return nil, nil
	}
	if len(cfg.RateLimitSecret) < 32 {
		return nil, errors.New("RATE_LIMIT_SECRET must be at least 32 characters when rate limits are enabled")
	}
	if client == nil {
		logger.Warn("REDIS_URL not set; rate limits are per process")
	}
	return ratelimit.New(client, []byte(cfg.RateLimitSecret), cfg.ClientIPHeader, cfg.CookieSecure, logger), nil
}

// newSignups returns nil, which turns signup off, when a requirement is
// missing. Codes go to the log only on plain-HTTP development origins.
func newSignups(cfg config.Config, client *redis.Client, logger *slog.Logger) *identity.Signups {
	var mailer identity.Mailer
	switch {
	case client == nil || len(cfg.SignupSecret) < 32:
		logger.Warn("signup disabled: it needs REDIS_URL and a SIGNUP_SECRET of at least 32 characters")
		return nil
	case cfg.ResendAPIKey != "":
		mailer = identity.NewResendMailer(cfg.ResendAPIKey, cfg.EmailFrom)
	case !cfg.CookieSecure:
		logger.Warn("RESEND_API_KEY not set; signup codes are written to this log")
		mailer = identity.LogMailer{Logger: logger}
	default:
		logger.Warn("signup disabled: RESEND_API_KEY is required on https origins")
		return nil
	}
	return identity.NewSignups(client, []byte(cfg.SignupSecret), mailer)
}
