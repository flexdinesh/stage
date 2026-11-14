// Package config loads process configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Host         string // APP_HOST, default 0.0.0.0
	Port         int    // APP_PORT, default 7380
	DatabaseURL  string // DATABASE_URL, required; with APP_ENV=dev never the prod db (see ProdDatabase)
	T3URL        string // T3_URL, default http://127.0.0.1:3773 (server-side MCP/OAuth calls)
	T3PublicURL  string // T3_PUBLIC_URL, default http://controlbox:3773 (browser "Open in T3" links)
	T3AdminToken string // T3_ADMIN_TOKEN, optional; enables OAuth auto-renew
	PublicURL    string // APP_PUBLIC_URL, default https://stage.local.deebox.xyz (PWA URL; VAPID subject)
	// T3ClientName names this app's T3 session (OAuth client, pairing
	// label): "stage", or "stage dev" with APP_ENV=dev.
	T3ClientName string
}

func Load() (Config, error) {
	cfg := Config{
		Host:         env("APP_HOST", "0.0.0.0"),
		DatabaseURL:  os.Getenv("DATABASE_URL"),
		T3URL:        env("T3_URL", "http://127.0.0.1:3773"),
		T3PublicURL:  env("T3_PUBLIC_URL", "http://controlbox:3773"),
		T3AdminToken: os.Getenv("T3_ADMIN_TOKEN"),
		PublicURL:    env("APP_PUBLIC_URL", "https://stage.local.deebox.xyz"),
		T3ClientName: "stage",
	}
	if os.Getenv("APP_ENV") == "dev" {
		cfg.T3ClientName = "stage dev"
	}
	port, err := strconv.Atoi(env("APP_PORT", "7380"))
	if err != nil || port <= 0 || port > 65535 {
		return Config{}, errors.New("APP_PORT must be a valid port number")
	}
	cfg.Port = port
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if os.Getenv("APP_ENV") == "dev" && ProdDatabase(cfg.DatabaseURL) {
		return Config{}, fmt.Errorf("APP_ENV=dev refuses the production database (port %s or name %q); use `make dev`", ProdPort, ProdDBName)
	}
	return cfg, nil
}

// The production database (compose.yaml): published on ProdPort, named
// ProdDBName. Dev and tests use compose.dev.yaml (5440).
const (
	ProdPort   = "5439"
	ProdDBName = "stage"
)

// ProdDatabase reports whether a Postgres URL points at the production
// port or database name. Unparseable URLs count as production.
func ProdDatabase(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return true
	}
	return u.Port() == ProdPort || strings.TrimPrefix(u.Path, "/") == ProdDBName
}

// Addr is the HTTP listen address.
func (c Config) Addr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
