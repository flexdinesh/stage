package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// WorkerEnvFile is the worker service's config file, in ServiceConfigDir
// (docs/service.md "Worker service"). The main server's service.env is
// separate.
const WorkerEnvFile = "worker.env"

// WorkerConfigEnv names the worker config file (worker.json) for `stage
// worker` commands, and is the only key worker.env may set.
const WorkerConfigEnv = "STAGE_WORKER_CONFIG"

// WorkerEnvKeys are the keys worker.env may set.
var WorkerEnvKeys = []string{WorkerConfigEnv}

// WorkerEnvTemplate is the worker.env `stage worker service start` creates.
const WorkerEnvTemplate = `# Stage worker service configuration (docs/service.md). Read by
# ` + "`stage worker run --managed`" + ` at start; apply changes with
# ` + "`stage worker service restart`" + `. KEY=VALUE per line. Only the key
# below is allowed; the shell environment is never used.

# The worker config ` + "`stage worker join`" + ` wrote (absolute path). Default:
# worker/worker.json in this installation's default data dir.
# STAGE_WORKER_CONFIG=
`

// WorkerEnvPath is ServiceConfigDir/worker.env.
func WorkerEnvPath(getenv func(string) string, goos string) (string, error) {
	d, err := ServiceConfigDir(getenv, goos)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, WorkerEnvFile), nil
}

// ParseWorkerEnv reads worker.env (same syntax as service.env).
func ParseWorkerEnv(r io.Reader) (map[string]string, error) {
	vals, err := parseEnvFile(r, WorkerEnvFile, WorkerEnvKeys, false)
	if err != nil {
		return nil, err
	}
	if v := vals[WorkerConfigEnv]; v != "" && !filepath.IsAbs(v) {
		return nil, fmt.Errorf("%s: %s must be an absolute path, got %q", WorkerEnvFile, WorkerConfigEnv, v)
	}
	return vals, nil
}

// ReadWorkerEnv reads and validates the worker.env at path. A missing file
// means the defaults (no keys). A file readable or writable by group or
// others is refused.
func ReadWorkerEnv(path string) (map[string]string, error) {
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
	vals, err := ParseWorkerEnv(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return vals, nil
}
