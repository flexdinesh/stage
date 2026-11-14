package config

import (
	"path/filepath"
	"runtime"
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
	t.Setenv("APP_ENV", "")
	t.Setenv("STAGE_DATA_DIR", "")
	cfg, err := Load()
	if err != nil || cfg.DataDir != filepath.Join(xdg, "stage") {
		t.Fatalf("default data dir %+v, %v", cfg, err)
	}
	dir := t.TempDir()
	t.Setenv("STAGE_DATA_DIR", dir)
	if cfg, err := Load(); err != nil || cfg.DataDir != dir {
		t.Fatalf("STAGE_DATA_DIR %+v, %v", cfg, err)
	}
}

func TestLoadDevRefusesProd(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	t.Setenv("APP_ENV", "dev")
	t.Setenv("STAGE_DATA_DIR", "")
	if _, err := Load(); err == nil {
		t.Fatal("dev config accepted the default (production) data dir")
	}
	// Spelled differently, same dir.
	t.Setenv("STAGE_DATA_DIR", filepath.Join(xdg, "x", "..", "stage"))
	if _, err := Load(); err == nil {
		t.Fatal("dev config accepted the default data dir via STAGE_DATA_DIR")
	}
	t.Setenv("STAGE_DATA_DIR", t.TempDir())
	cfg, err := Load()
	if err != nil || cfg.T3ClientName != "stage dev" {
		t.Fatalf("dev config %+v, %v", cfg, err)
	}
	t.Setenv("APP_ENV", "")
	t.Setenv("STAGE_DATA_DIR", "")
	if cfg, err := Load(); err != nil || cfg.T3ClientName != "stage" {
		t.Fatalf("prod config %+v, %v", cfg, err)
	}
}

// STAGE_DATA_DIR works when the default cannot be resolved (no HOME, no
// XDG_DATA_HOME), also under APP_ENV=dev; without it Load fails.
func TestLoadDataDirWithoutHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("STAGE_DATA_DIR", "")
	t.Setenv("APP_ENV", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load without HOME, XDG_DATA_HOME or STAGE_DATA_DIR succeeded")
	}
	dir := t.TempDir()
	t.Setenv("STAGE_DATA_DIR", dir)
	for _, env := range []string{"", "dev"} {
		t.Setenv("APP_ENV", env)
		if cfg, err := Load(); err != nil || cfg.DataDir != dir {
			t.Fatalf("APP_ENV=%q: %+v, %v", env, cfg, err)
		}
	}
}
