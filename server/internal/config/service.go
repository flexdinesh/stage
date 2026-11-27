package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// ServiceEnvFile is the managed service's config file name, in
// ServiceConfigDir (docs/service.md).
const ServiceEnvFile = "service.env"

func serviceEnvKeys(env harness.EnvOptions) []string {
	return append([]string{"STAGE_LISTEN", "STAGE_PUBLIC_URL", "STAGE_ALLOWED_ORIGINS", "STAGE_MULTI_USER"}, env.Keys...)
}

// managedFixedKeys are ignored when inherited in managed mode and refused in
// service.env, each with its reason: the service's data dir is always the
// identity default, and its database is always SQLite in it.
var managedFixedKeys = map[string]string{
	"STAGE_DATA_DIR": "its data dir is always this installation's default",
	"STAGE_ENV":      "its data dir is always this installation's default",
	DatabaseEnv:      "the service always uses SQLite in its data dir",
}

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

# STAGE_PUBLIC_URL=
# STAGE_ALLOWED_ORIGINS=
# STAGE_MULTI_USER=1
# Harness connections are set up in Settings > Connections.
`

// ParseServiceEnv reads service.env: KEY=VALUE lines, '#' comments, blank
// lines; a value may be wrapped in matching single or double quotes (no
// escapes). Unknown keys, managedFixedKeys and duplicates are errors.
func ParseServiceEnv(r io.Reader, env harness.EnvOptions) (map[string]string, error) {
	return parseEnvFile(r, ServiceEnvFile, serviceEnvKeys(env), env.Removed, true)
}

// parseEnvFile parses a KEY=VALUE env file named name (for errors) that
// may set only allowed; fixed refuses managedFixedKeys with their reason.
func parseEnvFile(r io.Reader, name string, allowed, removed []string, fixed bool) (map[string]string, error) {
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
			return nil, fmt.Errorf("%s line %d: want KEY=VALUE", name, n)
		}
		if fixed {
			if why, ok := managedFixedKeys[k]; ok {
				return nil, fmt.Errorf("%s line %d: %s cannot be set for the service (%s)", name, n, k, why)
			}
		}
		if slices.Contains(removed, k) {
			return nil, fmt.Errorf("%s line %d: %s is no longer read; remove it and set the connection up in Settings > Connections", name, n, k)
		}
		if !slices.Contains(allowed, k) {
			keys := append([]string(nil), allowed...)
			sort.Strings(keys)
			return nil, fmt.Errorf("%s line %d: unknown key %s (allowed: %s)", name, n, k, strings.Join(keys, ", "))
		}
		if _, dup := out[k]; dup {
			return nil, fmt.Errorf("%s line %d: %s set twice", name, n, k)
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		out[k] = v
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return out, nil
}

// ReadServiceEnv reads and validates the service.env at path. A missing
// file means the template defaults (STAGE_LISTEN=unix). A file readable or
// writable by group or others is refused (it may hold secrets).
func ReadServiceEnv(path string, env harness.EnvOptions) (map[string]string, error) {
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
		return nil, fmt.Errorf("%s has mode %v; want 0600 (it may hold secrets): chmod 600 %s", path, st.Mode().Perm(), path)
	}
	vals, err := ParseServiceEnv(f, env)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if vals["STAGE_LISTEN"] == "" {
		vals["STAGE_LISTEN"] = "unix"
	}
	return vals, nil
}

// LoadManaged is the config of `stage serve --managed`. Supported service.env
// keys come from file. STAGE_DATA_DIR, STAGE_ENV and STAGE_DATABASE_URL are
// ignored, so the service uses SQLite in the identity's default data dir.
// Other variables (HOME, XDG_*) come from getenv. The listeners must include
// the private default socket (`unix`).
func LoadManaged(getenv func(string) string, file map[string]string, env harness.EnvOptions) (Config, error) {
	keys := serviceEnvKeys(env)
	overlay := func(k string) string {
		if slices.Contains(keys, k) {
			return file[k]
		}
		if _, ok := managedFixedKeys[k]; ok {
			return ""
		}
		return getenv(k)
	}
	cfg, err := LoadFrom(overlay, nil, env)
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
