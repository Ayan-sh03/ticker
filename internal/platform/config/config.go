package config

import (
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	HTTPAddr     string
	DatabaseURL  string
	DBMaxConns   int32
	PublicOrigin string
	CookieSecure bool
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:     ":8080",
		DatabaseURL:  os.Getenv("DATABASE_URL"),
		DBMaxConns:   12,
		PublicOrigin: "http://localhost:8080",
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
	if origin := os.Getenv("PUBLIC_ORIGIN"); origin != "" {
		cfg.PublicOrigin = strings.TrimSuffix(origin, "/")
	}
	origin, err := url.Parse(cfg.PublicOrigin)
	if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Host == "" ||
		origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return Config{}, errors.New("PUBLIC_ORIGIN must be an http or https origin without a path")
	}
	cfg.CookieSecure = origin.Scheme == "https"
	if raw := os.Getenv("COOKIE_SECURE"); raw != "" {
		cfg.CookieSecure, err = strconv.ParseBool(raw)
		if err != nil {
			return Config{}, errors.New("COOKIE_SECURE must be true or false")
		}
	}
	if origin.Scheme == "https" && !cfg.CookieSecure {
		return Config{}, errors.New("COOKIE_SECURE must be true for an https PUBLIC_ORIGIN")
	}
	return cfg, nil
}
