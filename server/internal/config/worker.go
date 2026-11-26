package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/listen"
)

// WorkerEnvFile is the worker service's config file, in ServiceConfigDir
// (docs/service.md "Worker service"). The main server's service.env is
// separate.
const WorkerEnvFile = "worker.env"

// WorkerConfigEnv names the worker config file (worker.json) for `stage
// worker` commands.
const WorkerConfigEnv = "STAGE_WORKER_CONFIG"

// WorkerListenEnv is the worker's agent gateway listen list (STAGE_LISTEN
// syntax; `stage worker run --listen` overrides it). Default and required
// item: unix (docs/runtime.md "Agent gateway").
const WorkerListenEnv = "STAGE_WORKER_LISTEN"

// WorkerEnvTemplate is the worker.env `stage worker service start` creates.
func WorkerEnvTemplate(env harness.EnvOptions) string {
	return `# Stage worker service configuration (docs/service.md). Read by
# ` + "`stage worker run --managed`" + ` at start; apply changes with
# ` + "`stage worker service restart`" + `. KEY=VALUE per line. Only the keys
# below are allowed; the shell environment is never used.

# The worker config ` + "`stage worker join`" + ` wrote (absolute path). Default:
# worker/worker.json in this installation's default data dir.
# STAGE_WORKER_CONFIG=

# The agent gateway's listeners: agents on this machine reach Stage through
# them (STAGE_LISTEN syntax). unix (this installation's default socket, which
# the CLI and stage mcp find) is required; add host:port for HTTP MCP clients.
# STAGE_WORKER_LISTEN=unix,127.0.0.1:7381

` + env.WorkerTemplate
}

// ParseWorkerListen parses a gateway listen list (WorkerListenEnv, or
// `--listen` items joined by commas); empty means unix. It must include
// unix, which stays unresolved (Config.ResolveSockets resolves it).
func ParseWorkerListen(v string) ([]listen.Addr, error) {
	if strings.TrimSpace(v) == "" {
		v = listen.DefaultSocket
	}
	addrs, err := listen.ParseList(v)
	if err != nil {
		return nil, err
	}
	for _, a := range addrs {
		if a.Private {
			return addrs, nil
		}
	}
	return nil, fmt.Errorf("agent gateway listen %q must include unix (the default socket the CLI and stage mcp find)", v)
}

// WorkerEnvPath is ServiceConfigDir/worker.env.
func WorkerEnvPath(getenv func(string) string, goos string) (string, error) {
	d, err := ServiceConfigDir(getenv, goos)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, WorkerEnvFile), nil
}

// ParseWorkerEnv reads worker.env (same syntax as service.env).
func ParseWorkerEnv(r io.Reader, env harness.EnvOptions) (map[string]string, error) {
	keys := append([]string{WorkerConfigEnv, WorkerListenEnv}, env.Keys...)
	vals, err := parseEnvFile(r, WorkerEnvFile, keys, env.Removed, false)
	if err != nil {
		return nil, err
	}
	if v := vals[WorkerConfigEnv]; v != "" && !filepath.IsAbs(v) {
		return nil, fmt.Errorf("%s: %s must be an absolute path, got %q", WorkerEnvFile, WorkerConfigEnv, v)
	}
	if v, ok := vals[WorkerListenEnv]; ok {
		if _, err := ParseWorkerListen(v); err != nil {
			return nil, fmt.Errorf("%s: %s: %w", WorkerEnvFile, WorkerListenEnv, err)
		}
	}
	return vals, nil
}

// ReadWorkerEnv reads and validates the worker.env at path. A missing file
// means the defaults (no keys). A file readable or writable by group or
// others is refused.
func ReadWorkerEnv(path string, env harness.EnvOptions) (map[string]string, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
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
		return nil, fmt.Errorf("%s has mode %v; want 0600: chmod 600 %s", path, st.Mode().Perm(), path)
	}
	vals, err := ParseWorkerEnv(f, env)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return vals, nil
}
