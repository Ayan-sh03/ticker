package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ticker/internal/platform/config"
	"ticker/internal/platform/database"
	"ticker/internal/reservation"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("worker stopped", "error", err)
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
	store := reservation.NewStore(pool)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	logger.Info("reservation expiration worker ready")
	for {
		select {
		case <-ctx.Done():
			logger.Info("worker shutting down")
			return nil
		case <-ticker.C:
			for count := 0; count < 100; count++ {
				operationCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
				expired, err := store.ExpireOne(operationCtx)
				cancel()
				if ctx.Err() != nil {
					return nil
				}
				if err != nil {
					logger.Error("reservation expiration failed", "error", err)
					break
				}
				if !expired {
					break
				}
			}
		}
	}
}
