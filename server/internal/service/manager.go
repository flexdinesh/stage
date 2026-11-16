package service

import (
	"context"
	"io"
	"strconv"
	"strings"
)

// Phase is the service's lifecycle phase as its manager reports it.
type Phase string

const (
	PhaseStopped  Phase = "stopped"
	PhaseStarting Phase = "starting"
	PhaseRunning  Phase = "running"
	PhaseStopping Phase = "stopping"
	PhaseFailed   Phase = "failed"
)

// RunState is the manager's view of the service.
type RunState struct {
	Loaded   bool // the manager knows the definition (systemd: not "not-found"; launchd: bootstrapped)
	Phase    Phase
	PID      int
	Restarts int // automatic restarts so far (systemd NRestarts, launchd runs-1)
	LastExit *int
	Result   string // systemd Result (success, exit-code, start-limit-hit, ...)
	RunID    string // systemd InvocationID
	// AutoRestart: systemd is waiting to restart the process after it exited.
	AutoRestart bool
	NeedsReload bool
	Masked      bool
	LoadedFrom  string // the definition file the manager loaded
	DropIns     []string
	Enabled     bool // systemd UnitFileState=enabled
}

// ManagerInfo describes a reachable manager.
type ManagerInfo struct {
	Kind    string
	Version string
	// Env is the manager's environment (systemd show-environment; launchd:
	// HOME only, which is what an agent gets beyond PATH/USER/TMPDIR).
	Env     map[string]string
	UnitDir string // systemd user unit dir
	Linger  *bool  // systemd only
}

// UnavailableError: no usable per-user service manager (exit 6).
type UnavailableError struct{ Msg, Fix string }

func (e *UnavailableError) Error() string { return e.Msg }

// LogsInfo says where the service's logs are.
type LogsInfo struct {
	Kind       string `json:"kind"` // journald | file
	Unit       string `json:"unit,omitempty"`
	Path       string `json:"path,omitempty"`
	StderrPath string `json:"stderrPath,omitempty"`
}

// Manager drives one OS service manager. Every stop goes through it: no
// process is ever signalled by pid.
type Manager interface {
	Kind() string
	Probe(ctx context.Context) (ManagerInfo, error)
	State(ctx context.Context, p Paths) (RunState, error)
	// Reload makes the manager see the definition on disk (systemd
	// daemon-reload when wrote or it reports NeedDaemonReload).
	Reload(ctx context.Context, p Paths, wrote bool, rs RunState) (bool, error)
	// Start starts a service that is not running; defChanged lets launchd
	// reload a loaded job's plist first.
	Start(ctx context.Context, p Paths, rs RunState, defChanged bool) error
	Stop(ctx context.Context, p Paths) error
	// RestartInPlace restarts a running service (systemd). false: the
	// controller stops, waits and starts instead (launchd).
	RestartInPlace(ctx context.Context, p Paths, rs RunState) (bool, error)
	LoginStartup(ctx context.Context, p Paths, rs RunState) bool
	SetLoginStartup(ctx context.Context, p Paths, on bool, def []byte) error
	// Unregister forgets a removed definition (after Stop and file removal).
	Unregister(ctx context.Context, p Paths) error
	LogTail(ctx context.Context, p Paths, rs RunState, n int) []string
	Logs(ctx context.Context, p Paths, w io.Writer, n int, follow bool) error
	LogsInfo(p Paths) LogsInfo
}

func newManager(d Deps) Manager {
	if d.GOOS == "darwin" {
		return &launchd{r: d.Runner, uid: d.UID, cliEnv: d.Getenv}
	}
	return &systemd{r: d.Runner, uid: d.UID, cliEnv: d.Getenv}
}

// parseProps parses `systemctl show` KEY=VALUE output (any order).
func parseProps(s string) map[string]string {
	out := map[string]string{}
	for line := range strings.SplitSeq(s, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			out[k] = v
		}
	}
	return out
}

func lines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
