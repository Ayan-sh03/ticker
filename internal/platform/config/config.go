package config

import (
	"errors"
	"os"
	"strconv"
)

type Config struct {
	HTTPAddr    string
	DatabaseURL string
	DBMaxConns  int32
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:    ":8080",
		DatabaseURL: os.Getenv("DATABASE_URL"),
		DBMaxConns:  12,
	}
	if addr := os.Getenv("HTTP_ADDR"); addr != "" {
		cfg.HTTPAddr = addr
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if raw := os.Getenv("DB_MAX_CONNS"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || value < 1 {
			return Config{}, errors.New("DB_MAX_CONNS must be a positive integer")
		}
		cfg.DBMaxConns = int32(value)
	}
	return cfg, nil
}
