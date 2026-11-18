package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
)

// ServiceEnvFile is the managed service's config file name, in
// ServiceConfigDir (docs/service.md).
const ServiceEnvFile = "service.env"

// ServiceEnvKeys are the keys service.env may set. In managed mode
// (`stage serve --managed`) these, plus STAGE_DATA_DIR and STAGE_ENV, are never taken
// from the inherited environment: only from service.env.
var ServiceEnvKeys = []string{
	"STAGE_LISTEN", "STAGE_PUBLIC_URL", "STAGE_ALLOWED_ORIGINS",
	"T3_URL", "T3_PUBLIC_URL", "T3_ADMIN_TOKEN", "T3_FAKE", "T3_FAKE_SEED", "T3_BIN",
	"OPENCODE_URL", "OPENCODE_PUBLIC_URL", "OPENCODE_WEB_URL", "OPENCODE_USERNAME", "OPENCODE_PASSWORD",
}

// managedFixedKeys are ignored when inherited in managed mode and refused in
// service.env: the service's data dir is always the identity default.
var managedFixedKeys = []string{"STAGE_DATA_DIR", "STAGE_ENV"}

// InvalidError is a configuration error (exit 78 from `stage serve`): a
// restart cannot fix it.
type InvalidError struct{ Err error }

func (e *InvalidError) Error() string { return e.Err.Error() }
func (e *InvalidError) Unwrap() error { return e.Err }

// Invalid wraps err as an *InvalidError (nil stays nil).
func Invalid(err error) error {
	if err == nil {
		return nil
	}
	var ie *InvalidError
	if errors.As(err, &ie) {
		return err
	}
	return &InvalidError{Err: err}
}

// ServiceConfigDir holds service.env (and the service's definition and
// lock on macOS): Linux $XDG_CONFIG_HOME/<identity> (absolute), else
// ~/.config/<identity>; macOS ~/Library/Application Support/<identity>.
func ServiceConfigDir(getenv func(string) string, goos string) (string, error) {
	id := buildinfo.Identity
	home := getenv("HOME")
	if goos == "darwin" {
		if home == "" {
			return "", errors.New("config dir: $HOME is not set")
		}
		return filepath.Join(home, "Library", "Application Support", id), nil
	}
	if x := getenv("XDG_CONFIG_HOME"); filepath.IsAbs(x) {
		return filepath.Join(x, id), nil
	}
	if home == "" {
		return "", errors.New("config dir: $HOME is not set")
	}
	return filepath.Join(home, ".config", id), nil
}

// ServiceEnvPath is ServiceConfigDir/service.env.
func ServiceEnvPath(getenv func(string) string, goos string) (string, error) {
	d, err := ServiceConfigDir(getenv, goos)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, ServiceEnvFile), nil
}

// ServiceLogDir is where the managed server writes its own log on macOS
// (~/Library/Logs/<identity>). Linux logs go to journald: "".
func ServiceLogDir(getenv func(string) string, goos string) string {
	home := getenv("HOME")
	if goos != "darwin" || home == "" {
		return ""
	}
	return filepath.Join(home, "Library", "Logs", buildinfo.Identity)
}

// ServiceEnvTemplate is the service.env `stage service start` creates.
const ServiceEnvTemplate = `# Stage service configuration (docs/service.md). Read by ` + "`stage serve --managed`" + `
# at start; apply changes with ` + "`stage service restart`" + `. KEY=VALUE per line.
# Only the keys below are allowed. The data dir is always this installation's
# default; the shell environment is never used.

# Listeners: must include unix (the private socket desktop and CLI use).
# Add TCP for a browser, e.g. unix,127.0.0.1:7380
STAGE_LISTEN=unix

# T3_URL=http://127.0.0.1:3773
# T3_PUBLIC_URL=http://localhost:3773
# STAGE_PUBLIC_URL=
# STAGE_ALLOWED_ORIGINS=
# Optional: lets Stage renew its T3 access itself; otherwise pair in Settings.
# T3_ADMIN_TOKEN=
`

// ParseServiceEnv reads service.env: KEY=VALUE lines, '#' comments, blank
// lines; a value may be wrapped in matching single or double quotes (no
// escapes). Keys outside ServiceEnvKeys, STAGE_DATA_DIR, STAGE_ENV and
// duplicates are errors.
func ParseServiceEnv(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if n == 1 {
			line = strings.TrimPrefix(line, "\ufeff")
		}
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, fmt.Errorf("service.env line %d: want KEY=VALUE", n)
		}
		for _, f := range managedFixedKeys {
			if k == f {
				return nil, fmt.Errorf("service.env line %d: %s cannot be set for the service (its data dir is always this installation's default)", n, k)
			}
		}
		if !allowedServiceKey(k) {
			keys := append([]string(nil), ServiceEnvKeys...)
			sort.Strings(keys)
			return nil, fmt.Errorf("service.env line %d: unknown key %s (allowed: %s)", n, k, strings.Join(keys, ", "))
		}
		if _, dup := out[k]; dup {
			return nil, fmt.Errorf("service.env line %d: %s set twice", n, k)
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		out[k] = v
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("service.env: %w", err)
	}
	return out, nil
}

func allowedServiceKey(k string) bool {
	for _, a := range ServiceEnvKeys {
		if k == a {
			return true
		}
	}
	return false
}

// ReadServiceEnv reads and validates the service.env at path. A missing
// file means the template defaults (STAGE_LISTEN=unix). A file readable or
// writable by group or others is refused (it may hold T3_ADMIN_TOKEN).
func ReadServiceEnv(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{"STAGE_LISTEN": "unix"}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s has mode %v; want 0600 (it may hold T3_ADMIN_TOKEN): chmod 600 %s", path, st.Mode().Perm(), path)
	}
	vals, err := ParseServiceEnv(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if vals["STAGE_LISTEN"] == "" {
		vals["STAGE_LISTEN"] = "unix"
	}
	return vals, nil
}

// LoadManaged is the config of `stage serve --managed`: the keys of
// ServiceEnvKeys come only from file (service.env), STAGE_DATA_DIR and
// STAGE_ENV are ignored (the data dir is the identity default), everything
// else (HOME, XDG_*) from getenv. The listeners must include the private
// default socket (`unix`).
func LoadManaged(getenv func(string) string, file map[string]string) (Config, error) {
	overlay := func(k string) string {
		if allowedServiceKey(k) {
			return file[k]
		}
		for _, f := range managedFixedKeys {
			if k == f {
				return ""
			}
		}
		return getenv(k)
	}
	cfg, err := LoadFrom(overlay, nil)
	if err != nil {
		return Config{}, fmt.Errorf("service.env: %w", err)
	}
	for _, a := range cfg.Listen {
		if a.Network == "unix" && a.Private {
			return cfg, nil
		}
	}
	return Config{}, errors.New("service.env: STAGE_LISTEN must include unix (the private socket the desktop and CLI use)")
}
