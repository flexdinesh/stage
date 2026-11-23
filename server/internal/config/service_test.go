package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseServiceEnv(t *testing.T) {
	in := "\ufeff# comment\r\nSTAGE_LISTEN=unix,127.0.0.1:7380\r\n\nSTAGE_PUBLIC_URL = \"http://stage:7380/?a=b\"\nT3_BIN='x=y'\n"
	got, err := ParseServiceEnv(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"STAGE_LISTEN": "unix,127.0.0.1:7380", "STAGE_PUBLIC_URL": "http://stage:7380/?a=b", "T3_BIN": "x=y"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s = %q, want %q", k, got[k], v)
		}
	}
	for _, bad := range []string{"STAGE_DATA_DIR=/x\n", "STAGE_ENV=dev\n", "HOME=/x\n", "noequals\n", "T3_BIN=a\nT3_BIN=b\n", "T3_URL=http://t3:3773\n", "T3_ADMIN_TOKEN=x\n", "OPENCODE_URL=http://oc\n"} {
		if _, err := ParseServiceEnv(strings.NewReader(bad)); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestParseServiceEnvTemplate(t *testing.T) {
	got, err := ParseServiceEnv(strings.NewReader(ServiceEnvTemplate))
	if err != nil || got["STAGE_LISTEN"] != "unix" || len(got) != 1 {
		t.Fatalf("template: %v %v", got, err)
	}
}

func TestReadServiceEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ServiceEnvFile)
	if v, err := ReadServiceEnv(path); err != nil || v["STAGE_LISTEN"] != "unix" {
		t.Fatalf("missing file: %v %v", v, err)
	}
	if err := os.WriteFile(path, []byte("T3_BIN=/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadServiceEnv(path); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("0644 accepted: %v", err)
	}
	_ = os.Chmod(path, 0o600)
	if v, err := ReadServiceEnv(path); err != nil || v["STAGE_LISTEN"] != "unix" || v["T3_BIN"] != "/x" {
		t.Fatalf("defaults: %v %v", v, err)
	}
}

func TestLoadManaged(t *testing.T) {
	home := t.TempDir()
	inherited := map[string]string{
		"HOME": home, "STAGE_DATA_DIR": "/elsewhere", "STAGE_ENV": "dev", "STAGE_LISTEN": "127.0.0.1:1",
		"T3_BIN": "/inherited", "T3_FAKE": "1", "STAGE_ALLOWED_ORIGINS": "bogus",
	}
	getenv := func(k string) string { return inherited[k] }
	cfg, err := LoadManaged(getenv, map[string]string{"STAGE_LISTEN": "unix", "T3_BIN": "/file"})
	if err != nil {
		t.Fatal(err)
	}
	def, _ := defaultDataDir(getenv, runtime.GOOS)
	if cfg.DataDir != def {
		t.Fatalf("data dir %s, want default %s", cfg.DataDir, def)
	}
	if cfg.T3Bin != "/file" || cfg.T3Fake || len(cfg.AllowedOrigins) != 0 || cfg.T3ClientName != "stage" {
		t.Fatalf("inherited values leaked: %+v", cfg)
	}
	if len(cfg.Listen) != 1 || cfg.Listen[0].Network != "unix" || !cfg.Listen[0].Private {
		t.Fatalf("listen %+v", cfg.Listen)
	}
	if _, err := LoadManaged(getenv, map[string]string{"STAGE_LISTEN": "127.0.0.1:7380"}); err == nil || !strings.Contains(err.Error(), "must include unix") {
		t.Fatalf("TCP-only accepted: %v", err)
	}
	if _, err := LoadManaged(getenv, map[string]string{"STAGE_LISTEN": "unix,127.0.0.1:7380"}); err != nil {
		t.Fatalf("unix+tcp: %v", err)
	}
}

func TestServiceConfigDir(t *testing.T) {
	env := map[string]string{"HOME": "/h", "XDG_CONFIG_HOME": "/x"}
	get := func(k string) string { return env[k] }
	cases := []struct{ goos, want string }{{"linux", "/x/stage"}, {"darwin", "/h/Library/Application Support/stage"}}
	for _, c := range cases {
		if got, _ := ServiceConfigDir(get, c.goos); got != c.want {
			t.Fatalf("%s: %s, want %s", c.goos, got, c.want)
		}
	}
	env["XDG_CONFIG_HOME"] = "relative"
	if got, _ := ServiceConfigDir(get, "linux"); got != "/h/.config/stage" {
		t.Fatalf("relative XDG_CONFIG_HOME: %s", got)
	}
	if got := ServiceLogDir(get, "darwin"); got != "/h/Library/Logs/stage" {
		t.Fatalf("log dir %s", got)
	}
	if got := ServiceLogDir(get, "linux"); got != "" {
		t.Fatalf("linux log dir %s", got)
	}
}
