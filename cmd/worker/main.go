package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"ticker/internal/platform/config"
	"ticker/internal/platform/database"
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
	logger.Info("worker infrastructure ready; expiration processing is not implemented")
	<-ctx.Done()
	logger.Info("worker shutting down")
	return nil
}
