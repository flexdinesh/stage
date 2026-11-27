package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/harness"
)

var testEnv = harness.EnvOptions{
	Keys:           []string{"HARNESS_BIN", "HARNESS_FAKE"},
	Removed:        []string{"HARNESS_URL", "HARNESS_PASSWORD"},
	WorkerTemplate: "# HARNESS_BIN=\n# HARNESS_FAKE=\n",
}

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
	cfg, err := Load(nil, testEnv)
	if err != nil || cfg.DataDir != filepath.Join(xdg, "stage") {
		t.Fatalf("default data dir %+v, %v", cfg, err)
	}
	dir := t.TempDir()
	t.Setenv("STAGE_DATA_DIR", dir)
	if cfg, err := Load(nil, testEnv); err != nil || cfg.DataDir != dir {
		t.Fatalf("STAGE_DATA_DIR %+v, %v", cfg, err)
	}
}

func TestLoadDevRefusesProd(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	t.Setenv("STAGE_ENV", "dev")
	t.Setenv("STAGE_DATA_DIR", "")
	if _, err := Load(nil, testEnv); err == nil {
		t.Fatal("dev config accepted the default (production) data dir")
	}
	// Spelled differently, same dir.
	t.Setenv("STAGE_DATA_DIR", filepath.Join(xdg, "x", "..", "stage"))
	if _, err := Load(nil, testEnv); err == nil {
		t.Fatal("dev config accepted the default data dir via STAGE_DATA_DIR")
	}
	t.Setenv("STAGE_DATA_DIR", t.TempDir())
	cfg, err := Load(nil, testEnv)
	if err != nil || cfg.Getenv("STAGE_ENV") != "dev" {
		t.Fatalf("dev config %+v, %v", cfg, err)
	}
	t.Setenv("STAGE_ENV", "")
	t.Setenv("STAGE_DATA_DIR", "")
	if cfg, err := Load(nil, testEnv); err != nil || cfg.Getenv("STAGE_ENV") != "" {
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
	if _, err := Load(nil, testEnv); err == nil {
		t.Fatal("Load without HOME, XDG_DATA_HOME or STAGE_DATA_DIR succeeded")
	}
	dir := t.TempDir()
	t.Setenv("STAGE_DATA_DIR", dir)
	for _, env := range []string{"", "dev"} {
		t.Setenv("STAGE_ENV", env)
		if cfg, err := Load(nil, testEnv); err != nil || cfg.DataDir != dir {
			t.Fatalf("STAGE_ENV=%q: %+v, %v", env, cfg, err)
		}
	}
}

// clearEnv unsets every variable Load reads besides the data dir ones.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"STAGE_ENV", "STAGE_LISTEN", "STAGE_PUBLIC_URL", "STAGE_ALLOWED_ORIGINS", "STAGE_MULTI_USER", DatabaseEnv} {
		t.Setenv(k, "")
	}
	for _, k := range append(append([]string{}, testEnv.Keys...), testEnv.Removed...) {
		t.Setenv(k, "")
	}
	t.Setenv("STAGE_DATA_DIR", t.TempDir())
}

func TestLoadMultiUser(t *testing.T) {
	clearEnv(t)
	cfg, err := Load(nil, testEnv)
	if err != nil || cfg.MultiUser != nil {
		t.Fatalf("unset access intent: %v %v", cfg.MultiUser, err)
	}
	for value, enabled := range map[string]bool{"1": true, "true": true, "0": false, "false": false} {
		t.Setenv("STAGE_MULTI_USER", value)
		cfg, err := Load(nil, testEnv)
		if err != nil || cfg.MultiUser == nil || *cfg.MultiUser != enabled {
			t.Errorf("STAGE_MULTI_USER=%s: %v %v", value, cfg.MultiUser, err)
		}
	}
	t.Setenv("STAGE_MULTI_USER", "unexpected-secret")
	if _, err := Load(nil, testEnv); err == nil || !strings.Contains(err.Error(), "STAGE_MULTI_USER") || strings.Contains(err.Error(), "unexpected-secret") {
		t.Fatalf("invalid access intent: %v", err)
	}
}

func TestLoadListen(t *testing.T) {
	clearEnv(t)
	cfg, err := Load(nil, testEnv)
	if err != nil || len(cfg.Listen) != 1 || cfg.Listen[0].Network != "tcp" || cfg.Listen[0].Address != "127.0.0.1:7380" {
		t.Fatalf("default listen %+v, %v", cfg.Listen, err)
	}
	t.Setenv("STAGE_LISTEN", "unix:/run/stage/stage.sock")
	if cfg, err := Load(nil, testEnv); err != nil || len(cfg.Listen) != 1 || cfg.Listen[0].Network != "unix" || cfg.Listen[0].Address != "/run/stage/stage.sock" {
		t.Fatalf("STAGE_LISTEN unix %+v, %v", cfg.Listen, err)
	}
	t.Setenv("STAGE_LISTEN", "unix, 127.0.0.1:7381")
	if cfg, err := Load(nil, testEnv); err != nil || len(cfg.Listen) != 2 || !cfg.Listen[0].Private || cfg.Listen[0].Address != "" || cfg.Listen[1].Address != "127.0.0.1:7381" {
		t.Fatalf("STAGE_LISTEN list %+v, %v", cfg.Listen, err)
	}
	// The flag wins, and an invalid STAGE_LISTEN does not matter then.
	t.Setenv("STAGE_LISTEN", "nope")
	if _, err := Load(nil, testEnv); err == nil || !strings.Contains(err.Error(), "STAGE_LISTEN") {
		t.Fatalf("invalid STAGE_LISTEN: %v", err)
	}
	if cfg, err := Load([]string{"0.0.0.0:9000"}, testEnv); err != nil || len(cfg.Listen) != 1 || cfg.Listen[0].Address != "0.0.0.0:9000" {
		t.Fatalf("--listen %+v, %v", cfg.Listen, err)
	}
	// Repeated flags and comma lists combine.
	if cfg, err := Load([]string{"unix", "127.0.0.1:1,127.0.0.1:2"}, testEnv); err != nil || len(cfg.Listen) != 3 {
		t.Fatalf("repeated --listen %+v, %v", cfg.Listen, err)
	}
	if _, err := Load([]string{"unix:rel.sock"}, testEnv); err == nil || !strings.Contains(err.Error(), "--listen") {
		t.Fatalf("invalid --listen: %v", err)
	}
	if _, err := Load([]string{"unix", "unix"}, testEnv); err == nil {
		t.Fatal("duplicate --listen accepted")
	}
}

func TestLoadURLDefaults(t *testing.T) {
	clearEnv(t)
	cfg, err := Load(nil, testEnv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicURL != "" || cfg.Getenv("HARNESS_BIN") != "" || len(cfg.RemovedEnv) != 0 || cfg.WarnRemovedEnv() != "" {
		t.Fatalf("defaults %+v", cfg)
	}
	t.Setenv("STAGE_PUBLIC_URL", "https://stage.example")
	t.Setenv("HARNESS_BIN", "/opt/harness/bin/tool")
	if cfg, _ := Load(nil, testEnv); cfg.PublicURL != "https://stage.example" || cfg.Getenv("HARNESS_BIN") != "/opt/harness/bin/tool" {
		t.Fatalf("explicit URLs %+v", cfg)
	}
}

// Harness connection settings are never read from the environment: a
// leftover one is only named in the startup warning.
func TestLoadRemovedHarnessEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("HARNESS_URL", "http://harness.test")
	t.Setenv("HARNESS_PASSWORD", "secret")
	cfg, err := Load(nil, testEnv)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.RemovedEnv) != 2 || cfg.RemovedEnv[0] != "HARNESS_URL" || cfg.RemovedEnv[1] != "HARNESS_PASSWORD" {
		t.Fatalf("removed %v", cfg.RemovedEnv)
	}
	w := cfg.WarnRemovedEnv()
	if !strings.Contains(w, "HARNESS_URL, HARNESS_PASSWORD") || !strings.Contains(w, "Settings > Connections") || strings.Contains(w, "secret") {
		t.Fatalf("warning %q", w)
	}
}

func TestLoadAllowedOrigins(t *testing.T) {
	clearEnv(t)
	if cfg, err := Load(nil, testEnv); err != nil || len(cfg.AllowedOrigins) != 0 {
		t.Fatalf("default origins %+v, %v", cfg.AllowedOrigins, err)
	}
	t.Setenv("STAGE_PUBLIC_URL", "https://Stage.Example.com:443/app")
	t.Setenv("STAGE_ALLOWED_ORIGINS", "https://m.tail.ts.net, http://stage.lan:7380,https://stage.example.com")
	cfg, err := Load(nil, testEnv)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://m.tail.ts.net", "http://stage.lan:7380", "https://stage.example.com"}
	if strings.Join(cfg.AllowedOrigins, " ") != strings.Join(want, " ") {
		t.Fatalf("origins %q, want %q", cfg.AllowedOrigins, want)
	}
	// A non-http(s) public URL adds nothing and is not an error.
	t.Setenv("STAGE_ALLOWED_ORIGINS", "")
	t.Setenv("STAGE_PUBLIC_URL", "mailto:me@example.com")
	if cfg, err := Load(nil, testEnv); err != nil || len(cfg.AllowedOrigins) != 0 {
		t.Fatalf("mailto public URL %+v, %v", cfg.AllowedOrigins, err)
	}
	for _, bad := range []string{"stage.lan", "https://x/path", "ftp://x", "https://u@x", "https://x?q", "https://", ","} {
		t.Setenv("STAGE_ALLOWED_ORIGINS", bad)
		if _, err := Load(nil, testEnv); err == nil || !strings.Contains(err.Error(), "STAGE_ALLOWED_ORIGINS") {
			t.Errorf("STAGE_ALLOWED_ORIGINS=%q: %v", bad, err)
		}
	}
}

func TestParseOrigin(t *testing.T) {
	for in, want := range map[string]string{
		"https://X.ts.net":      "https://x.ts.net",
		"https://x.ts.net/":     "https://x.ts.net",
		"https://x:443":         "https://x",
		"http://x:80":           "http://x",
		"http://x:443":          "http://x:443",
		"http://[::1]:7380":     "http://[::1]:7380",
		"http://127.0.0.1:5173": "http://127.0.0.1:5173",
	} {
		if got, err := ParseOrigin(in); err != nil || got != want {
			t.Errorf("ParseOrigin(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func getenvMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaultSocket(t *testing.T) {
	home, run := t.TempDir(), t.TempDir()
	env := map[string]string{"HOME": home, "XDG_RUNTIME_DIR": run}
	def := filepath.Join(home, ".local", "share", "stage")
	other := filepath.Join(t.TempDir(), "dev")
	for _, c := range []struct {
		name, goos, dataDir, want string
		env                       map[string]string
	}{
		{"linux default", "linux", def, filepath.Join(run, "stage", "stage.sock"), env},
		{"linux other", "linux", other, filepath.Join(run, "stage", "stage-"+hash8(other)+".sock"), env},
		{"linux no runtime dir", "linux", other, filepath.Join(other, "run", "stage.sock"), map[string]string{"HOME": home}},
		{"darwin default", "darwin", filepath.Join(home, "Library", "Application Support", "stage"), filepath.Join(home, "Library", "Application Support", "stage", "run", "stage.sock"), map[string]string{"HOME": home}},
		{"darwin other", "darwin", other, filepath.Join(home, "Library", "Application Support", "stage", "run", "stage-"+hash8(other)+".sock"), map[string]string{"HOME": home}},
	} {
		got, err := defaultSocket(getenvMap(c.env), c.goos, c.dataDir)
		if err != nil || got != c.want {
			t.Errorf("%s: %q, %v; want %q", c.name, got, err, c.want)
		}
	}
	// Different data dirs never share a socket.
	a, _ := defaultSocket(getenvMap(env), "linux", other)
	b, _ := defaultSocket(getenvMap(env), "linux", other+"2")
	if a == b {
		t.Fatal("two data dirs share a default socket")
	}
	// A symlinked spelling of the data dir is the same instance.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(other, link); err == nil {
		if err := os.MkdirAll(other, 0o700); err != nil {
			t.Fatal(err)
		}
		x, _ := defaultSocket(getenvMap(env), "linux", link)
		y, _ := defaultSocket(getenvMap(env), "linux", other)
		if x != y {
			t.Fatalf("symlinked data dir socket %q != %q", x, y)
		}
	}
	// Over the OS limit: a clear error, not a truncated bind.
	deep := filepath.Join(other, strings.Repeat("d", 120))
	if _, err := defaultSocket(getenvMap(map[string]string{"HOME": home}), "linux", deep); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("long default socket: %v", err)
	}
}

func hash8(dir string) string {
	sum := sha256.Sum256([]byte(Canonical(dir)))
	return hex.EncodeToString(sum[:4])
}

func TestResolveSockets(t *testing.T) {
	clearEnv(t)
	run := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", run)
	t.Setenv("STAGE_LISTEN", "unix,unix:/tmp/x.sock,127.0.0.1:7380")
	cfg, err := Load(nil, testEnv)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.ResolveSockets(); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "linux" && !strings.HasPrefix(cfg.Listen[0].Address, filepath.Join(run, "stage")+"/") {
		t.Fatalf("default socket %q not under XDG_RUNTIME_DIR", cfg.Listen[0].Address)
	}
	if !cfg.Listen[0].Private || cfg.Listen[1].Address != "/tmp/x.sock" || cfg.Listen[1].Private {
		t.Fatalf("resolved %+v", cfg.Listen)
	}
}
