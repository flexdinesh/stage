package service

import (
	"fmt"
	"path/filepath"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/instance"
)

// Role is what a service runs (docs/service.md): the main server
// (`stage service`) or a worker (`stage worker service`). Under one
// identity a machine runs one role's service, never both.
type Role string

const (
	RoleServer Role = ""
	RoleWorker Role = "worker"
)

// Other is the role that may not be installed alongside r.
func (r Role) Other() Role {
	if r == RoleWorker {
		return RoleServer
	}
	return RoleWorker
}

// Command is the CLI verb that manages r's service.
func (r Role) Command() string {
	if r == RoleWorker {
		return "stage worker service"
	}
	return "stage service"
}

// KV is an environment variable baked into the definition.
type KV struct{ Key, Value string }

// PinnableKeys are the CLI's variables pinned into the definition at first
// registration when they differ from the manager's environment, so the
// service resolves the same data and config dirs as the shell that
// registered it (launchd agents never have XDG_*).
var PinnableKeys = []string{"XDG_DATA_HOME", "XDG_CONFIG_HOME"}

// Paths are everything one installation's service touches.
type Paths struct {
	Role     Role
	GOOS     string
	Identity string
	Label    string // launchd label
	Unit     string // systemd unit name
	// Name is the service's name for its manager: Unit or Label.
	Name string

	DataDir   string // the service's data dir (identity default)
	DataLock  string // DataDir/stage.lock
	ConfigDir string
	// ServiceEnv is the role's env file: service.env (server) or
	// worker.env (worker).
	ServiceEnv  string
	ServiceLock string // serializes `stage service` and `stage worker service` commands
	Socket      string // the private default socket of DataDir

	Definition string // unit file or canonical plist
	LoginCopy  string // macOS ~/Library/LaunchAgents/<label>.plist; "" on Linux
	// OtherDefinition is the other role's definition (role_conflict).
	OtherDefinition string

	LogDir     string // macOS only
	LogFile    string // macOS only: the server's own rotated log
	StderrFile string // macOS only: launchd stdout/stderr (panics)

	Pinned []KV
}

// UnitName is the systemd unit of this installation's server service.
func UnitName() string { return UnitNameFor(RoleServer) }

// UnitNameFor is the systemd unit of role: <id>.service, or
// <id>-worker.service.
func UnitNameFor(r Role) string {
	if r == RoleWorker {
		return buildinfo.Identity + "-worker.service"
	}
	return buildinfo.Identity + ".service"
}

// LabelFor is the launchd label of role: buildinfo.Label(), or that plus
// ".worker".
func LabelFor(r Role) string {
	if r == RoleWorker {
		return buildinfo.Label() + ".worker"
	}
	return buildinfo.Label()
}

// Name is the server service's name on goos: the unit (Linux) or launchd
// label.
func Name(goos string) string { return NameFor(goos, RoleServer) }

// NameFor is role's service name on goos.
func NameFor(goos string, r Role) string {
	if goos == "darwin" {
		return LabelFor(r)
	}
	return UnitNameFor(r)
}

// ManagerKind is the service manager on goos.
func ManagerKind(goos string) string {
	if goos == "darwin" {
		return "launchd"
	}
	return "systemd"
}

// ResolvePaths derives the service's paths. The service's environment is
// managerEnv overlaid with pinned; unitDir is the systemd user unit dir
// (ignored on macOS). Pure apart from canonicalizing existing directories.
func ResolvePaths(goos string, managerEnv func(string) string, pinned []KV, unitDir string) (Paths, error) {
	return ResolvePathsFor(RoleServer, goos, managerEnv, pinned, unitDir)
}

// ResolvePathsFor is ResolvePaths for role.
func ResolvePathsFor(role Role, goos string, managerEnv func(string) string, pinned []KV, unitDir string) (Paths, error) {
	svcEnv := func(k string) string {
		for _, kv := range pinned {
			if kv.Key == k {
				return kv.Value
			}
		}
		return managerEnv(k)
	}
	p := Paths{
		Role: role, GOOS: goos, Identity: buildinfo.Identity, Label: LabelFor(role), Unit: UnitNameFor(role),
		Name: NameFor(goos, role), Pinned: pinned,
	}
	dd, err := config.DataDirFor(svcEnv, goos)
	if err != nil {
		return Paths{}, err
	}
	p.DataDir = config.Canonical(dd)
	p.DataLock = filepath.Join(p.DataDir, instance.LockFile)
	if p.Socket, err = config.DefaultSocketFor(svcEnv, goos, p.DataDir); err != nil {
		return Paths{}, err
	}
	if p.ConfigDir, err = config.ServiceConfigDir(svcEnv, goos); err != nil {
		return Paths{}, err
	}
	p.ServiceEnv = filepath.Join(p.ConfigDir, config.ServiceEnvFile)
	if role == RoleWorker {
		p.ServiceEnv = filepath.Join(p.ConfigDir, config.WorkerEnvFile)
	}
	p.ServiceLock = filepath.Join(p.ConfigDir, "service.lock")
	if goos == "darwin" {
		home := svcEnv("HOME")
		p.Definition = filepath.Join(p.ConfigDir, p.Label+".plist")
		p.OtherDefinition = filepath.Join(p.ConfigDir, LabelFor(role.Other())+".plist")
		p.LoginCopy = filepath.Join(home, "Library", "LaunchAgents", p.Label+".plist")
		p.LogDir = config.ServiceLogDir(svcEnv, goos)
		p.LogFile = filepath.Join(p.LogDir, LogFileName(role))
		p.StderrFile = filepath.Join(p.LogDir, "launchd.stderr.log")
		if role == RoleWorker {
			p.StderrFile = filepath.Join(p.LogDir, "worker.launchd.stderr.log")
		}
		return p, nil
	}
	if unitDir == "" {
		return Paths{}, fmt.Errorf("systemd user unit dir unknown")
	}
	p.Definition = filepath.Join(unitDir, p.Unit)
	p.OtherDefinition = filepath.Join(unitDir, UnitNameFor(role.Other()))
	return p, nil
}

// LogFileName is the macOS log file a managed role writes itself.
func LogFileName(r Role) string {
	if r == RoleWorker {
		return "worker.log"
	}
	return "stage.log"
}

// PinsFor are the PinnableKeys set (absolute) in cliEnv and different from
// managerEnv.
func PinsFor(cliEnv, managerEnv func(string) string) []KV {
	var out []KV
	for _, k := range PinnableKeys {
		v := cliEnv(k)
		if v != "" && filepath.IsAbs(v) && filepath.Clean(v) != filepath.Clean(managerEnv(k)) {
			out = append(out, KV{k, filepath.Clean(v)})
		}
	}
	return out
}

func samePins(a, b []KV) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
