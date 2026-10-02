package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ticker/internal/platform/config"
	"ticker/internal/platform/database"
	"ticker/internal/seed"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(); err != nil {
		logger.Error("seed failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	dataset := flag.String("dataset", "demo", "dataset to seed (demo)")
	value := flag.Int64("seed", 42, "non-negative seed for stable fixture IDs")
	flag.Parse()
	if *dataset != "demo" || *value < 0 || flag.NArg() != 0 {
		return errors.New("use --dataset demo --seed <non-negative integer>, with no positional arguments")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pool, err := database.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	summary, err := seed.Demo(ctx, pool, *value, os.Getenv("DEMO_PASSWORD"))
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(summary)
}
