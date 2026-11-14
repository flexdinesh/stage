package config

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultDataDir(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	if got, err := DefaultDataDir(); err != nil || got != filepath.Join(xdg, "stage") {
		t.Fatalf("with XDG_DATA_HOME: %q, %v", got, err)
	}
	home := t.TempDir()
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", home)
	want := filepath.Join(home, ".local", "share", "stage")
	if runtime.GOOS == "darwin" {
		want = filepath.Join(home, "Library", "Application Support", "stage")
	}
	if got, err := DefaultDataDir(); err != nil || got != want {
		t.Fatalf("default: %q, %v; want %q", got, err, want)
	}
}

func TestLoadDataDir(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	t.Setenv("STAGE_ENV", "")
	t.Setenv("STAGE_DATA_DIR", "")
	cfg, err := Load("")
	if err != nil || cfg.DataDir != filepath.Join(xdg, "stage") {
		t.Fatalf("default data dir %+v, %v", cfg, err)
	}
	dir := t.TempDir()
	t.Setenv("STAGE_DATA_DIR", dir)
	if cfg, err := Load(""); err != nil || cfg.DataDir != dir {
		t.Fatalf("STAGE_DATA_DIR %+v, %v", cfg, err)
	}
}

func TestLoadDevRefusesProd(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	t.Setenv("STAGE_ENV", "dev")
	t.Setenv("STAGE_DATA_DIR", "")
	if _, err := Load(""); err == nil {
		t.Fatal("dev config accepted the default (production) data dir")
	}
	// Spelled differently, same dir.
	t.Setenv("STAGE_DATA_DIR", filepath.Join(xdg, "x", "..", "stage"))
	if _, err := Load(""); err == nil {
		t.Fatal("dev config accepted the default data dir via STAGE_DATA_DIR")
	}
	t.Setenv("STAGE_DATA_DIR", t.TempDir())
	cfg, err := Load("")
	if err != nil || cfg.T3ClientName != "stage dev" {
		t.Fatalf("dev config %+v, %v", cfg, err)
	}
	t.Setenv("STAGE_ENV", "")
	t.Setenv("STAGE_DATA_DIR", "")
	if cfg, err := Load(""); err != nil || cfg.T3ClientName != "stage" {
		t.Fatalf("prod config %+v, %v", cfg, err)
	}
}

// STAGE_DATA_DIR works when the default cannot be resolved (no HOME, no
// XDG_DATA_HOME), also under STAGE_ENV=dev; without it Load fails.
func TestLoadDataDirWithoutHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("STAGE_DATA_DIR", "")
	t.Setenv("STAGE_ENV", "")
	if _, err := Load(""); err == nil {
		t.Fatal("Load without HOME, XDG_DATA_HOME or STAGE_DATA_DIR succeeded")
	}
	dir := t.TempDir()
	t.Setenv("STAGE_DATA_DIR", dir)
	for _, env := range []string{"", "dev"} {
		t.Setenv("STAGE_ENV", env)
		if cfg, err := Load(""); err != nil || cfg.DataDir != dir {
			t.Fatalf("STAGE_ENV=%q: %+v, %v", env, cfg, err)
		}
	}
}

// clearEnv unsets every variable Load reads besides the data dir ones.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"STAGE_ENV", "STAGE_LISTEN", "STAGE_PUBLIC_URL", "T3_URL", "T3_PUBLIC_URL", "T3_ADMIN_TOKEN"} {
		t.Setenv(k, "")
	}
	t.Setenv("STAGE_DATA_DIR", t.TempDir())
}

func TestLoadListen(t *testing.T) {
	clearEnv(t)
	cfg, err := Load("")
	if err != nil || cfg.Listen.Network != "tcp" || cfg.Listen.Address != "127.0.0.1:7380" {
		t.Fatalf("default listen %+v, %v", cfg.Listen, err)
	}
	t.Setenv("STAGE_LISTEN", "unix:/run/stage/stage.sock")
	if cfg, err := Load(""); err != nil || cfg.Listen.Network != "unix" || cfg.Listen.Address != "/run/stage/stage.sock" {
		t.Fatalf("STAGE_LISTEN unix %+v, %v", cfg.Listen, err)
	}
	// The flag wins, and an invalid STAGE_LISTEN does not matter then.
	t.Setenv("STAGE_LISTEN", "nope")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "STAGE_LISTEN") {
		t.Fatalf("invalid STAGE_LISTEN: %v", err)
	}
	if cfg, err := Load("0.0.0.0:9000"); err != nil || cfg.Listen.Address != "0.0.0.0:9000" {
		t.Fatalf("--listen %+v, %v", cfg.Listen, err)
	}
	if _, err := Load("unix:rel.sock"); err == nil || !strings.Contains(err.Error(), "--listen") {
		t.Fatalf("invalid --listen: %v", err)
	}
}

func TestLoadURLDefaults(t *testing.T) {
	clearEnv(t)
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.T3URL != "http://127.0.0.1:3773" || cfg.T3PublicURL != cfg.T3URL || cfg.PublicURL != "" {
		t.Fatalf("defaults %+v", cfg)
	}
	t.Setenv("T3_URL", "http://host.docker.internal:3773")
	if cfg, _ := Load(""); cfg.T3PublicURL != "http://host.docker.internal:3773" {
		t.Fatalf("T3_PUBLIC_URL does not fall back to T3_URL: %+v", cfg)
	}
	t.Setenv("T3_PUBLIC_URL", "http://t3.lan:3773")
	t.Setenv("STAGE_PUBLIC_URL", "https://stage.example")
	if cfg, _ := Load(""); cfg.T3PublicURL != "http://t3.lan:3773" || cfg.PublicURL != "https://stage.example" {
		t.Fatalf("explicit URLs %+v", cfg)
	}
}
