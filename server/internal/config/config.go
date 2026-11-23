// Package config loads process configuration from environment variables
// and derives the per-user paths of an installation (docs/runtime.md).
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/listen"
)

// DefaultT3URL is the T3 server address without a saved setting.
const DefaultT3URL = "http://127.0.0.1:3773"

type Config struct {
	// Listen: STAGE_LISTEN (comma list) or `stage serve --listen` (repeatable),
	// default listen.Default. A listen.DefaultSocket item stays unresolved
	// (Address "") until ResolveSockets.
	Listen    []listen.Addr
	DataDir   string // STAGE_DATA_DIR, default DefaultDataDir(); holds stage.db. With STAGE_ENV=dev never the default.
	PublicURL string // STAGE_PUBLIC_URL, optional (PWA URL; VAPID subject when https)
	// T3Bin (T3_BIN) is the t3 CLI auto-pair runs; "" = look it up.
	T3Bin string
	// AllowedOrigins are the browser origins trusted besides loopback ones
	// (normalized scheme://host[:port]): STAGE_ALLOWED_ORIGINS plus the
	// origin of STAGE_PUBLIC_URL. Their hosts pass the Host check.
	AllowedOrigins []string
	// T3ClientName names this app's T3 session (OAuth client, pairing
	// label): the installation identity ("stage"), plus " dev" with
	// STAGE_ENV=dev.
	T3ClientName string
	// T3Fake (T3_FAKE=1) uses the in-memory fake T3 client; T3FakeSeed
	// (T3_FAKE_SEED=1) seeds it with demo threads.
	T3Fake, T3FakeSeed bool
	// RemovedEnv are the RemovedHarnessEnvKeys set in the environment
	// (ignored; startup warns).
	RemovedEnv []string
}

// Load reads the process environment. listenFlags (`stage serve
// --listen`, each a comma list), when given, override STAGE_LISTEN.
func Load(listenFlags []string) (Config, error) { return LoadFrom(os.Getenv, listenFlags) }

// LoadFrom is Load over getenv.
func LoadFrom(getenv func(string) string, listenFlags []string) (Config, error) {
	cfg := HarnessFrom(getenv)
	cfg.PublicURL = getenv("STAGE_PUBLIC_URL")
	dev := getenv("STAGE_ENV") == "dev"
	var err error
	if cfg.Listen, err = loadListen(getenv, listenFlags); err != nil {
		return Config{}, err
	}
	if cfg.AllowedOrigins, err = loadOrigins(getenv("STAGE_ALLOWED_ORIGINS"), cfg.PublicURL); err != nil {
		return Config{}, err
	}
	// The default is only needed when STAGE_DATA_DIR is unset, or for the dev
	// guard; one that cannot be resolved cannot be STAGE_DATA_DIR's target.
	defaultDir, defaultErr := defaultDataDir(getenv, runtime.GOOS)
	if v := getenv("STAGE_DATA_DIR"); v != "" {
		if cfg.DataDir, err = filepath.Abs(v); err != nil {
			return Config{}, fmt.Errorf("STAGE_DATA_DIR: %w", err)
		}
	} else if defaultErr != nil {
		return Config{}, defaultErr
	} else {
		cfg.DataDir = defaultDir
	}
	if dev && defaultErr == nil && Canonical(cfg.DataDir) == Canonical(defaultDir) {
		return Config{}, fmt.Errorf("STAGE_ENV=dev refuses the default (production) data dir %s; set STAGE_DATA_DIR or use `mise run dev`", defaultDir)
	}
	return cfg, nil
}

// HarnessEnvKeys are the harness options a process takes from its
// environment (HarnessFrom): the main server's for its local machine, a
// worker's for its machine. Connection settings (URLs, credentials) are
// never taken from the environment: they are set up in Settings.
var HarnessEnvKeys = []string{"T3_FAKE", "T3_FAKE_SEED", "T3_BIN"}

// RemovedHarnessEnvKeys are harness connection settings that were once
// read from the environment and no longer are (set them up in Settings >
// Connections). Startup warns about each one still set.
var RemovedHarnessEnvKeys = []string{
	"T3_URL", "T3_PUBLIC_URL", "T3_ADMIN_TOKEN",
	"OPENCODE_URL", "OPENCODE_PUBLIC_URL", "OPENCODE_WEB_URL", "OPENCODE_USERNAME", "OPENCODE_PASSWORD",
}

// WarnRemovedEnv is the startup warning for RemovedEnv ("" when none).
func (c Config) WarnRemovedEnv() string {
	if len(c.RemovedEnv) == 0 {
		return ""
	}
	return strings.Join(c.RemovedEnv, ", ") + " no longer read; set the connection up in Settings > Connections"
}

// RemovedHarnessEnv lists the RemovedHarnessEnvKeys set in getenv.
func RemovedHarnessEnv(getenv func(string) string) []string {
	var out []string
	for _, k := range RemovedHarnessEnvKeys {
		if getenv(k) != "" {
			out = append(out, k)
		}
	}
	return out
}

// HarnessFrom is the harness part of a Config (HarnessEnvKeys, and
// STAGE_ENV=dev for the T3 client name) from getenv.
func HarnessFrom(getenv func(string) string) Config {
	cfg := Config{
		T3Bin:        getenv("T3_BIN"),
		T3ClientName: buildinfo.Identity,
		T3Fake:       getenv("T3_FAKE") == "1",
		T3FakeSeed:   getenv("T3_FAKE_SEED") == "1",
		RemovedEnv:   RemovedHarnessEnv(getenv),
	}
	if getenv("STAGE_ENV") == "dev" {
		cfg.T3ClientName += " dev"
	}
	return cfg
}

func loadListen(getenv func(string) string, flags []string) ([]listen.Addr, error) {
	if len(flags) > 0 {
		addrs, err := listen.ParseList(strings.Join(flags, ","))
		if err != nil {
			return nil, fmt.Errorf("--listen: %w", err)
		}
		return addrs, nil
	}
	addrs, err := listen.ParseList(envOr(getenv, "STAGE_LISTEN", listen.Default))
	if err != nil {
		return nil, fmt.Errorf("STAGE_LISTEN: %w", err)
	}
	return addrs, nil
}

// loadOrigins parses STAGE_ALLOWED_ORIGINS (comma list; invalid entries are
// errors) and adds publicURL's origin when it is an http(s) URL.
func loadOrigins(list, publicURL string) ([]string, error) {
	var out []string
	add := func(o string) {
		for _, e := range out {
			if e == o {
				return
			}
		}
		out = append(out, o)
	}
	if strings.TrimSpace(list) != "" {
		for item := range strings.SplitSeq(list, ",") {
			o, err := ParseOrigin(strings.TrimSpace(item))
			if err != nil {
				return nil, fmt.Errorf("STAGE_ALLOWED_ORIGINS: %w", err)
			}
			add(o)
		}
	}
	if u, err := url.Parse(publicURL); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
		if o, err := ParseOrigin(u.Scheme + "://" + u.Host); err == nil {
			add(o)
		}
	}
	return out, nil
}

// ParseOrigin validates a browser origin (http(s)://host[:port], no path,
// credentials, query or fragment) and normalizes it the way browsers send
// Origin: lowercase, default port dropped.
func ParseOrigin(s string) (string, error) {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("origin %q: want http(s)://host[:port]", s)
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("origin %q: want only scheme://host[:port], no path, credentials or query", s)
	}
	scheme, host := strings.ToLower(u.Scheme), strings.ToLower(u.Host)
	if p := u.Port(); (scheme == "https" && p == "443") || (scheme == "http" && p == "80") {
		host = strings.TrimSuffix(host, ":"+p)
	}
	return scheme + "://" + host, nil
}

// ResolveSockets fills the path of each listen.DefaultSocket address from
// DataDir (which serve has created and canonicalized).
func (c *Config) ResolveSockets() error {
	for i, a := range c.Listen {
		if a.Network != "unix" || a.Address != "" {
			continue
		}
		p, err := DefaultSocket(os.Getenv, c.DataDir)
		if err != nil {
			return err
		}
		c.Listen[i].Address = p
	}
	return nil
}

// DataDirFrom is the data dir getenv selects: STAGE_DATA_DIR (absolute),
// else the identity's default. Canonical when it exists.
func DataDirFrom(getenv func(string) string) (string, error) {
	if v := getenv("STAGE_DATA_DIR"); v != "" {
		abs, err := filepath.Abs(v)
		if err != nil {
			return "", fmt.Errorf("STAGE_DATA_DIR: %w", err)
		}
		return Canonical(abs), nil
	}
	d, err := defaultDataDir(getenv, runtime.GOOS)
	if err != nil {
		return "", err
	}
	return Canonical(d), nil
}

// DefaultDataDir is where the production database lives, named by the
// installation identity (buildinfo.Identity, "stage"): $XDG_DATA_HOME/stage,
// else ~/Library/Application Support/stage on macOS, else
// ~/.local/share/stage. Absolute.
func DefaultDataDir() (string, error) { return defaultDataDir(os.Getenv, runtime.GOOS) }

// DataDirFor is the identity's default data dir under getenv on goos (not
// canonicalized).
func DataDirFor(getenv func(string) string, goos string) (string, error) {
	return defaultDataDir(getenv, goos)
}

func defaultDataDir(getenv func(string) string, goos string) (string, error) {
	id := buildinfo.Identity
	if x := getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Abs(filepath.Join(x, id))
	}
	home := getenv("HOME")
	if home == "" {
		return "", errors.New("data dir: $HOME is not set (set STAGE_DATA_DIR)")
	}
	if goos == "darwin" {
		return filepath.Join(home, "Library", "Application Support", id), nil
	}
	return filepath.Join(home, ".local", "share", id), nil
}

// Canonical resolves symlinks in an absolute path when it exists, so one
// data dir has one identity however it is spelled; otherwise it returns the
// cleaned path.
func Canonical(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// DefaultSocket is the private default socket path for dataDir (the
// `unix` listen keyword, and where clients look first):
//   - Linux: $XDG_RUNTIME_DIR/<identity>/; without XDG_RUNTIME_DIR,
//     <dataDir>/run/.
//   - macOS: ~/Library/Application Support/<identity>/run/.
//
// In the shared per-user dir the file is stage.sock for the identity's
// default data dir and stage-<8 hex of sha256(dataDir)>.sock for any other,
// so instances on different data dirs never collide; under <dataDir>/run it
// is always stage.sock. A path over the OS limit is an error.
func DefaultSocket(getenv func(string) string, dataDir string) (string, error) {
	return defaultSocket(getenv, runtime.GOOS, dataDir)
}

// DefaultSocketFor is DefaultSocket for goos.
func DefaultSocketFor(getenv func(string) string, goos, dataDir string) (string, error) {
	return defaultSocket(getenv, goos, dataDir)
}

func defaultSocket(getenv func(string) string, goos, dataDir string) (string, error) {
	dataDir = Canonical(dataDir)
	var shared string
	if goos == "darwin" {
		if home := getenv("HOME"); home != "" {
			shared = filepath.Join(home, "Library", "Application Support", buildinfo.Identity, "run")
		}
	} else if x := getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(x) {
		shared = filepath.Join(x, buildinfo.Identity)
	}
	var p string
	if shared == "" {
		p = filepath.Join(dataDir, "run", "stage.sock")
	} else {
		name := "stage.sock"
		if def, err := defaultDataDir(getenv, goos); err != nil || Canonical(def) != dataDir {
			sum := sha256.Sum256([]byte(dataDir))
			name = "stage-" + hex.EncodeToString(sum[:4]) + ".sock"
		}
		p = filepath.Join(shared, name)
	}
	if err := listen.CheckSocketPathFor(goos, p); err != nil {
		return "", fmt.Errorf("default socket: %w", err)
	}
	return p, nil
}

func envOr(getenv func(string) string, key, fallback string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return fallback
}
