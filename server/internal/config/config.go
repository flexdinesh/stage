// Package config loads process configuration from environment variables.
package config

import (
	"errors"
	"net"
	"os"
	"strconv"
)

type Config struct {
	Host         string // APP_HOST, default 0.0.0.0
	Port         int    // APP_PORT, default 7380
	DatabaseURL  string // DATABASE_URL, required
	T3URL        string // T3_URL, default http://127.0.0.1:3773 (server-side MCP/OAuth calls)
	T3PublicURL  string // T3_PUBLIC_URL, default http://controlbox:3773 (browser "Open in T3" links)
	T3AdminToken string // T3_ADMIN_TOKEN, optional; enables OAuth auto-renew
	PublicURL    string // APP_PUBLIC_URL, default https://t3stage.local.deebox.xyz (PWA URL; VAPID subject)
}

func Load() (Config, error) {
	cfg := Config{
		Host:         env("APP_HOST", "0.0.0.0"),
		DatabaseURL:  os.Getenv("DATABASE_URL"),
		T3URL:        env("T3_URL", "http://127.0.0.1:3773"),
		T3PublicURL:  env("T3_PUBLIC_URL", "http://controlbox:3773"),
		T3AdminToken: os.Getenv("T3_ADMIN_TOKEN"),
		PublicURL:    env("APP_PUBLIC_URL", "https://t3stage.local.deebox.xyz"),
	}
	port, err := strconv.Atoi(env("APP_PORT", "7380"))
	if err != nil || port <= 0 || port > 65535 {
		return Config{}, errors.New("APP_PORT must be a valid port number")
	}
	cfg.Port = port
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	return cfg, nil
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
