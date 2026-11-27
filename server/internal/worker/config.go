// Package worker is the remote-machine side of Stage (`stage worker
// join|run|status|leave`, docs/runtime.md "Worker process", ADR 0002): a
// thin process with no database that dials out to the main server and
// answers its calls over the worker channel (internal/workerchan).
//
// Everything it keeps lives in one directory, by default worker/ in the
// identity's data dir: worker.json (the config and machine token, 0600),
// worker.lock (one worker per directory, with its holder record) and
// worker.state.json (what the running worker last reported).
package worker

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/config"
)

// File names in the worker directory.
const (
	ConfigFile = "worker.json"
	LockFile   = "worker.lock"
	StateFile  = "worker.state.json"
	// DirName is the worker directory inside a data dir.
	DirName = "worker"
)

// ConfigSchema versions worker.json.
const ConfigSchema = 1

// Config is worker.json: which main server this machine joined, as which
// machine, with which token.
type Config struct {
	Schema int `json:"schema"`
	// URL is the main server's base URL (http or https).
	URL       string    `json:"url"`
	MachineID int64     `json:"machineId"`
	Label     string    `json:"label"`
	Token     string    `json:"token"`
	JoinedAt  time.Time `json:"joinedAt"`
}

func (c Config) cacheIdentity() string {
	input := fmt.Sprintf("%q %d %s %q", c.URL, c.MachineID, c.JoinedAt.UTC().Format(time.RFC3339Nano), c.Token)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(input)))
}

// ErrNotJoined: no worker.json at the config path.
var ErrNotJoined = errors.New("this machine has not joined a Stage server: run `stage worker join <link>` (from Settings > Machines or `stage machine pair`)")

// DefaultConfigPath is worker/worker.json in the data dir getenv selects
// (STAGE_DATA_DIR, else the identity default).
func DefaultConfigPath(getenv func(string) string) (string, error) {
	dd, err := config.DataDirFrom(getenv)
	if err != nil {
		return "", err
	}
	return filepath.Join(dd, DirName, ConfigFile), nil
}

// ManagedConfigPath is the config path of the worker service: worker.env's
// STAGE_WORKER_CONFIG, else worker/worker.json in the identity's default
// data dir (STAGE_DATA_DIR and STAGE_WORKER_CONFIG in getenv are ignored,
// as for the managed server).
func ManagedConfigPath(getenv func(string) string, goos string, workerEnv map[string]string) (string, error) {
	if v := workerEnv[config.WorkerConfigEnv]; v != "" {
		return filepath.Clean(v), nil
	}
	dd, err := config.DataDirFor(getenv, goos)
	if err != nil {
		return "", err
	}
	return filepath.Join(config.Canonical(dd), DirName, ConfigFile), nil
}

// ResolveConfigPath is the config path of a foreground command: flag
// (--config), else STAGE_WORKER_CONFIG, else DefaultConfigPath. Absolute.
func ResolveConfigPath(flag string, getenv func(string) string) (string, error) {
	p := flag
	if p == "" {
		p = getenv(config.WorkerConfigEnv)
	}
	if p == "" {
		return DefaultConfigPath(getenv)
	}
	return filepath.Abs(p)
}

// Dir is the worker directory of a config path (lock and state live next
// to the config).
func Dir(configPath string) string { return filepath.Dir(configPath) }

// LoadConfig reads worker.json. ErrNotJoined when it is missing; a file
// readable or writable by group or others is refused (it holds the token).
func LoadConfig(path string) (Config, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, ErrNotJoined
	}
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Config{}, err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return Config{}, fmt.Errorf("%s has mode %v; want 0600 (it holds the machine token): chmod 600 %s", path, st.Mode().Perm(), path)
	}
	var c Config
	dec := json.NewDecoder(f)
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	switch {
	case c.Schema != ConfigSchema:
		return Config{}, fmt.Errorf("%s: schema %d, want %d", path, c.Schema, ConfigSchema)
	case c.URL == "" || c.Token == "" || c.MachineID <= 0:
		return Config{}, fmt.Errorf("%s: incomplete (url, machineId and token are required)", path)
	}
	if _, err := baseURL(c.URL); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// SaveConfig writes worker.json atomically (0600, directory 0700).
func SaveConfig(path string, c Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(b, '\n'), 0o600)
}

// ParseLink splits a pairing link (<server>/join#<code>, docs/api.md) into
// the server's base URL and the code.
func ParseLink(link string) (base, code string, err error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil {
		return "", "", fmt.Errorf("pairing link: %w", err)
	}
	code = u.Fragment
	if code == "" {
		return "", "", errors.New("pairing link has no code: want <server>/join#<code> from Settings > Machines or `stage machine pair`")
	}
	path := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(path, "/join") {
		return "", "", errors.New("pairing link must end in /join#<code>")
	}
	u.Path, u.RawPath, u.Fragment, u.RawFragment, u.RawQuery = strings.TrimSuffix(path, "/join"), "", "", "", ""
	base, err = baseURL(u.String())
	return base, code, err
}

// baseURL validates a main server URL: http(s) with a host, no query,
// fragment or credentials. The result has no trailing slash.
func baseURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("server URL: %w", err)
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return "", fmt.Errorf("server URL %q: want http or https", raw)
	case u.Host == "":
		return "", fmt.Errorf("server URL %q has no host", raw)
	case u.User != nil:
		return "", fmt.Errorf("server URL %q must not carry credentials", raw)
	case u.RawQuery != "" || u.Fragment != "":
		return "", fmt.Errorf("server URL %q must not have a query or fragment", raw)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// writeFileAtomic writes b to path (mode perm) via a temp file and rename,
// creating the directory 0700.
func writeFileAtomic(path string, b []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(b)
	if werr == nil {
		werr = f.Chmod(perm)
	}
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp, path)
	}
	if werr != nil {
		_ = os.Remove(tmp)
	}
	return werr
}
