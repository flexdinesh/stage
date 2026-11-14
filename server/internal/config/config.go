// Package config loads process configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

type Config struct {
	Host         string // APP_HOST, default 0.0.0.0
	Port         int    // APP_PORT, default 7380
	DataDir      string // STAGE_DATA_DIR, default DefaultDataDir(); holds stage.db. With APP_ENV=dev never the default.
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
	defaultDir, err := DefaultDataDir()
	if err != nil {
		return Config{}, err
	}
	cfg.DataDir = defaultDir
	if v := os.Getenv("STAGE_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if cfg.DataDir, err = filepath.Abs(cfg.DataDir); err != nil {
		return Config{}, fmt.Errorf("STAGE_DATA_DIR: %w", err)
	}
	if os.Getenv("APP_ENV") == "dev" && cfg.DataDir == defaultDir {
		return Config{}, fmt.Errorf("APP_ENV=dev refuses the default (production) data dir %s; set STAGE_DATA_DIR or use `mise run dev`", defaultDir)
	}
	return cfg, nil
}

// DefaultDataDir is where the production database lives:
// $XDG_DATA_HOME/stage, else ~/Library/Application Support/stage on macOS,
// else ~/.local/share/stage. Absolute.
func DefaultDataDir() (string, error) {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Abs(filepath.Join(x, "stage"))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("data dir: %w (set STAGE_DATA_DIR)", err)
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "stage"), nil
	}
	return filepath.Join(home, ".local", "share", "stage"), nil
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
