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
	// BasePath is stripped from request paths when a host forwards /api unchanged.
	BasePath string
	// Metrics exposes /metrics; serverless hosts cannot be scraped, so they disable it.
	Metrics bool
	// RateLimits enables request limits. RedisURL shares write limits across
	// instances; without it, limits are counted in process memory.
	RateLimits      bool
	RedisURL        string
	RateLimitSecret string
	// ClientIPHeader names the header the deployment's own proxy sets to the client address.
	ClientIPHeader string
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:     ":8080",
		DatabaseURL:  os.Getenv("DATABASE_URL"),
		DBMaxConns:   12,
		PublicOrigin: "http://localhost:8080",
		Metrics:      true,
		RateLimits:   true,
		RedisURL:     os.Getenv("REDIS_URL"),
		// Used only to sign device cookies and hash fingerprints.
		RateLimitSecret: os.Getenv("RATE_LIMIT_SECRET"),
		ClientIPHeader:  os.Getenv("CLIENT_IP_HEADER"),
	}
	// Serverless hosts such as Vercel assign the port through PORT.
	if addr := os.Getenv("HTTP_ADDR"); addr != "" {
		cfg.HTTPAddr = addr
	} else if port := os.Getenv("PORT"); port != "" {
		cfg.HTTPAddr = ":" + port
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
	if raw := os.Getenv("METRICS_ENABLED"); raw != "" {
		if cfg.Metrics, err = strconv.ParseBool(raw); err != nil {
			return Config{}, errors.New("METRICS_ENABLED must be true or false")
		}
	}
	if raw := os.Getenv("RATE_LIMITS_ENABLED"); raw != "" {
		if cfg.RateLimits, err = strconv.ParseBool(raw); err != nil {
			return Config{}, errors.New("RATE_LIMITS_ENABLED must be true or false")
		}
	}
	cfg.BasePath = strings.TrimSuffix(os.Getenv("HTTP_BASE_PATH"), "/")
	if cfg.BasePath != "" && !strings.HasPrefix(cfg.BasePath, "/") {
		return Config{}, errors.New("HTTP_BASE_PATH must start with /")
	}
	if origin.Scheme == "https" && !cfg.CookieSecure {
		return Config{}, errors.New("COOKIE_SECURE must be true for an https PUBLIC_ORIGIN")
	}
	return cfg, nil
}
