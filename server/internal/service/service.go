// Package service manages the per-user OS service that supervises the
// Stage server: a systemd user unit on Linux, a launchd LaunchAgent on
// macOS (docs/service.md). The server stays a foreground process
// (`stage serve --managed`); this package only writes the definition and
// drives the manager. It never signals a process by pid.
package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/filelock"
	"github.com/flexdinesh/stage/server/internal/instance"
	"github.com/flexdinesh/stage/server/internal/listen"
	"github.com/flexdinesh/stage/server/internal/worker"
)

// Default bounded waits.
const (
	DefaultStartWait   = 30 * time.Second
	DefaultStopWait    = 2 * time.Minute // > the 110s stop timeout
	DefaultRestartWait = 150 * time.Second
)

// Deps are the controller's environment (tests substitute fakes).
type Deps struct {
	// Role is the service this controller manages: the server (zero) or
	// the worker (`stage worker service`).
	Role   Role
	GOOS   string
	UID    int
	Root   bool // effective uid 0
	Getenv func(string) string
	Runner Runner
	// Health fetches /api/health from a unix:// URL.
	Health func(ctx context.Context, url string) (client.Health, error)
	// Probe runs `<exe> version --json`.
	Probe    func(ctx context.Context, exe string) (BuildInfo, error)
	Self     func() string
	TempDirs []string
	Poll     time.Duration
}

// OSDeps is the real environment.
func OSDeps() Deps {
	return Deps{
		GOOS: runtime.GOOS, UID: os.Getuid(), Root: os.Geteuid() == 0, Getenv: os.Getenv,
		Runner: ExecRunner{},
		Health: func(ctx context.Context, url string) (client.Health, error) {
			ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			return client.New(url).Health(ctx)
		},
		Probe: ProbeExe, Self: SelfPath, TempDirs: DefaultTempDirs(), Poll: 200 * time.Millisecond,
	}
}

// Controller runs one `stage service` command.
type Controller struct {
	d        Deps
	mgr      Manager
	info     ManagerInfo
	probeErr error
	p        Paths
	prepared bool
	unitDir  string

	def       []byte
	defExists bool
	marker    Marker
	cliPins   []KV
}

// New returns a controller over d.
func New(d Deps) *Controller {
	if d.Poll <= 0 {
		d.Poll = 200 * time.Millisecond
	}
	return &Controller{d: d}
}

// managerEnv is the manager's environment (the shell's when the manager
// is unreachable, for diagnostics only).
func (c *Controller) managerEnv(k string) string {
	if c.info.Env != nil {
		return c.info.Env[k]
	}
	return c.d.Getenv(k)
}

// svcEnv is the service's environment: managerEnv plus pinned variables.
func (c *Controller) svcEnv(k string) string {
	for _, kv := range c.p.Pinned {
		if kv.Key == k {
			return kv.Value
		}
	}
	return c.managerEnv(k)
}

// prepare probes the manager and resolves paths from the existing
// definition's pins (else this shell's). A probe failure is kept in
// probeErr (status still reports what it can).
func (c *Controller) prepare(ctx context.Context) error {
	if c.prepared {
		return nil
	}
	c.mgr = newManager(c.d)
	info, err := c.mgr.Probe(ctx)
	unitDir := info.UnitDir
	if err != nil {
		c.probeErr = err
		base := filepath.Join(c.d.Getenv("HOME"), ".config")
		if x := c.d.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(x) {
			base = x
		}
		unitDir = filepath.Join(base, "systemd", "user")
	} else {
		c.info = info
	}
	c.unitDir = unitDir
	c.cliPins = PinsFor(c.d.Getenv, c.managerEnv)
	if err := c.loadDefinition(); err != nil {
		return err
	}
	c.prepared = true
	return nil
}

// loadDefinition (re)reads the definition and resolves paths from its pins
// (else this shell's). Mutating commands call it again under the command
// lock, so they never act on a definition another command just changed.
func (c *Controller) loadDefinition() error {
	p0, err := ResolvePathsFor(c.d.Role, c.d.GOOS, c.managerEnv, nil, c.unitDir)
	if err != nil {
		return &Error{Code: CodeInvalid, Msg: err.Error()}
	}
	c.def, c.defExists, err = readMaybe(p0.Definition)
	if err != nil {
		return err
	}
	c.marker = Marker{}
	pins := c.cliPins
	if c.defExists {
		c.marker = ParseDefinition(c.d.GOOS, c.def)
		if c.marker.Generated {
			pins = c.marker.Env
		}
	}
	if c.p, err = ResolvePathsFor(c.d.Role, c.d.GOOS, c.managerEnv, pins, c.unitDir); err != nil {
		return &Error{Code: CodeInvalid, Msg: err.Error()}
	}
	return nil
}

// foreign refuses a service the manager loaded from a definition
// `stage service` did not write (a packaged or hand-written unit or plist
// with this name): stop, uninstall, disable and start leave it alone.
func (c *Controller) foreign(ctx context.Context, rs RunState) error {
	if !rs.Loaded || rs.LoadedFrom == "" || rs.LoadedFrom == c.p.Definition {
		return nil
	}
	if c.d.GOOS == "darwin" && rs.LoadedFrom == c.p.LoginCopy && c.mgr.LoginStartup(ctx, c.p, rs) {
		return nil
	}
	return &Error{Code: CodeInvalid, Msg: fmt.Sprintf("%s is loaded from %s, which `stage service` did not write; leaving it alone", c.p.Name, rs.LoadedFrom), Fix: "remove or rename that definition (or manage it yourself)"}
}

func (c *Controller) requireManager() error {
	var ue *UnavailableError
	if errors.As(c.probeErr, &ue) {
		return &Error{Code: CodeUnavailable, Msg: ue.Msg, Fix: ue.Fix}
	}
	if c.probeErr != nil {
		return &Error{Code: CodeUnavailable, Msg: c.probeErr.Error()}
	}
	return nil
}

// begin is the common preamble of a mutating command: preflight, probe,
// paths, then the command lock (released by the returned func).
func (c *Controller) begin(ctx context.Context, wait time.Duration) (func(), error) {
	if c.d.Root || c.d.Getenv("SUDO_USER") != "" {
		return nil, &Error{Code: CodeInvalid, Msg: "stage service manages a per-user service: run it as your own user, not as root or through sudo"}
	}
	if !buildinfo.ValidIdentity() {
		return nil, &Error{Code: CodeInvalid, Msg: fmt.Sprintf("identity %q cannot name a service (want [a-z0-9][a-z0-9._-]*)", buildinfo.Identity)}
	}
	if err := c.prepare(ctx); err != nil {
		return nil, err
	}
	if err := c.requireManager(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(c.p.ConfigDir, 0o700); err != nil {
		return nil, err
	}
	lctx, cancel := context.WithTimeout(ctx, wait+2*time.Minute)
	defer cancel()
	l, err := filelock.AcquireContext(lctx, c.p.ServiceLock, 0o600)
	if errors.Is(err, filelock.ErrLocked) {
		return nil, &Error{Code: CodeTimeout, Msg: "another `stage service` command is still running (" + c.p.ServiceLock + ")"}
	}
	if err != nil {
		return nil, err
	}
	if err := c.loadDefinition(); err != nil {
		l.Release()
		return nil, err
	}
	return l.Release, nil
}

func newReport(cmd string) Report {
	return Report{Schema: ReportSchema, Command: cmd, Warnings: []string{}, Notes: []string{}, LogTail: []string{}}
}

// finish fills the outcome and the final status.
func (c *Controller) finish(ctx context.Context, rep *Report, err error) Report {
	if err != nil {
		var e *Error
		if !errors.As(err, &e) {
			var ue *UnavailableError
			if errors.As(err, &ue) {
				e = &Error{Code: CodeUnavailable, Msg: ue.Msg, Fix: ue.Fix}
			} else {
				e = &Error{Code: CodeFailed, Msg: err.Error()}
			}
		}
		rep.Code, rep.Message, rep.Fix = e.Code, e.Msg, e.Fix
		if len(e.LogTail) > 0 {
			rep.LogTail = e.LogTail
		}
	} else {
		rep.OK = true
	}
	if c.prepared {
		st := c.status(ctx)
		rep.Status = &st
	}
	return *rep
}

// resolveExe picks and validates the executable the definition runs.
func (c *Controller) resolveExe(ctx context.Context, explicit string, rep *Report) (string, error) {
	exe := explicit
	if exe == "" {
		exe = c.d.Self()
	}
	if err := ValidateExe(exe, c.d.TempDirs); err != nil {
		return "", &Error{Code: CodeInvalid, Msg: err.Error()}
	}
	exe = filepath.Clean(exe)
	if self := c.d.Self(); explicit == "" || self == exe {
		return exe, nil
	}
	bi, err := c.d.Probe(ctx, exe)
	if err != nil {
		return "", &Error{Code: CodeInvalid, Msg: err.Error()}
	}
	if bi.Identity != buildinfo.Identity {
		return "", &Error{Code: CodeInvalid, Msg: fmt.Sprintf("%s is installation %q, not %q: each installation manages only its own service", exe, bi.Identity, buildinfo.Identity)}
	}
	if bi.Version != buildinfo.Version {
		rep.Notes = append(rep.Notes, fmt.Sprintf("%s is version %s; this CLI is %s", exe, bi.Version, buildinfo.Version))
	}
	return exe, nil
}

// owner is the process holding the role's lock: the data dir (server) or
// the worker dir (worker).
type owner struct {
	held    bool
	known   bool // the holder record was readable
	pid     int
	managed bool
	// key identifies the process instance (server instance id, worker
	// start time), so a restart can tell a new one.
	key     string
	version string
}

func (c *Controller) owner() owner {
	if c.d.Role == RoleWorker {
		held, h, _ := worker.ReadHolder(c.workerDir())
		o := owner{held: held}
		if h != nil {
			o.known, o.pid, o.managed, o.key, o.version = true, h.PID, h.Managed, h.StartedAt.String(), h.Version
		}
		return o
	}
	held, h, _ := instance.ReadHolder(c.p.DataDir)
	o := owner{held: held}
	if h != nil {
		o.known, o.pid, o.managed, o.key, o.version = true, h.PID, h.Managed, h.InstanceID, h.Version
	}
	return o
}

// lockedMsg is the conflict message for a lock held by another process.
func (c *Controller) lockedMsg() (string, string) {
	if c.d.Role == RoleWorker {
		dir := c.workerDir()
		_, h, _ := worker.ReadHolder(dir)
		return (&worker.LockedError{Dir: dir, Holder: h}).Error(), "stop that worker first: the worker service and a foreground `stage worker run` cannot share a worker dir"
	}
	_, holder, _ := instance.ReadHolder(c.p.DataDir)
	return (&instance.LockedError{DataDir: c.p.DataDir, Holder: holder}).Error(), "stop that server first: the service and a foreground `stage serve` cannot share a data dir"
}

// conflict refuses a role lock held by anything but the service itself.
func (c *Controller) conflict(rs RunState) error {
	o := c.owner()
	if !o.held {
		return nil
	}
	switch {
	case o.known && rs.PID != 0 && o.pid == rs.PID:
		return nil
	case !o.known && (rs.Phase == PhaseStarting || rs.Phase == PhaseRunning):
		return nil
	case rs.Phase == PhaseStopping:
		return nil
	}
	msg, fix := c.lockedMsg()
	return &Error{Code: CodeConflict, Msg: msg, Fix: fix}
}

// roleConflict refuses installing this role's service while the other
// role's (generated) definition exists: one machine runs the main server
// or a worker under an identity, never both.
func (c *Controller) roleConflict() error {
	b, ok, _ := readMaybe(c.p.OtherDefinition)
	if !ok || !ParseDefinition(c.d.GOOS, b).Generated {
		return nil
	}
	other := c.d.Role.Other()
	return &Error{Code: CodeInvalid, Msg: fmt.Sprintf("the %s service is installed (%s): a machine runs the Stage server or a worker under one identity, not both", roleNoun(other), c.p.OtherDefinition),
		Fix: "`" + other.Command() + " uninstall` first"}
}

func roleNoun(r Role) string {
	if r == RoleWorker {
		return "worker"
	}
	return "server"
}

// workerEnv is worker.env's values (empty when missing or invalid).
func (c *Controller) workerEnv() map[string]string {
	vals, err := config.ReadWorkerEnv(c.p.ServiceEnv)
	if err != nil {
		return map[string]string{}
	}
	return vals
}

// workerConfig is the worker config path the worker service uses.
func (c *Controller) workerConfig() string {
	p, err := worker.ManagedConfigPath(c.svcEnv, c.d.GOOS, c.workerEnv())
	if err != nil {
		return ""
	}
	return p
}

// workerDir is the worker service's directory (lock, state).
func (c *Controller) workerDir() string { return worker.Dir(c.workerConfig()) }

// ensureServiceEnv creates the service.env template (never overwriting)
// and validates the file the server will read.
func (c *Controller) ensureServiceEnv(rep *Report) error {
	if c.d.Role == RoleWorker {
		return c.ensureWorkerEnv(rep)
	}
	f, err := os.OpenFile(c.p.ServiceEnv, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	switch {
	case err == nil:
		_, werr := f.WriteString(config.ServiceEnvTemplate)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return werr
		}
		rep.Changes.ServiceEnvCreated = true
		rep.Notes = append(rep.Notes, "created "+c.p.ServiceEnv+" (edit it to set T3_URL and other settings, then `stage service restart`)")
	case !errors.Is(err, fs.ErrExist):
		return err
	}
	vals, err := config.ReadServiceEnv(c.p.ServiceEnv)
	if err == nil {
		_, err = config.LoadManaged(c.svcEnv, vals)
	}
	if err != nil {
		return &Error{Code: CodeInvalid, Msg: err.Error(), Fix: "fix " + c.p.ServiceEnv}
	}
	if c.p.LogDir != "" {
		if err := os.MkdirAll(c.p.LogDir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// ensureWorkerEnv creates the worker.env template (never overwriting),
// validates it, and requires the worker config it names (joined).
func (c *Controller) ensureWorkerEnv(rep *Report) error {
	f, err := os.OpenFile(c.p.ServiceEnv, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	switch {
	case err == nil:
		_, werr := f.WriteString(config.WorkerEnvTemplate)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return werr
		}
		rep.Changes.ServiceEnvCreated = true
		rep.Notes = append(rep.Notes, "created "+c.p.ServiceEnv+" (set STAGE_WORKER_CONFIG there to use another worker config, then `stage worker service restart`)")
	case !errors.Is(err, fs.ErrExist):
		return err
	}
	vals, err := config.ReadWorkerEnv(c.p.ServiceEnv)
	if err != nil {
		return &Error{Code: CodeInvalid, Msg: err.Error(), Fix: "fix " + c.p.ServiceEnv}
	}
	path, err := worker.ManagedConfigPath(c.svcEnv, c.d.GOOS, vals)
	if err == nil {
		_, err = worker.LoadConfig(path)
	}
	if err != nil {
		fix := "fix " + path
		if errors.Is(err, worker.ErrNotJoined) {
			fix = "run `stage worker join <link>` first (the service reads " + path + ")"
		}
		return &Error{Code: CodeInvalid, Msg: err.Error(), Fix: fix}
	}
	if c.p.LogDir != "" {
		if err := os.MkdirAll(c.p.LogDir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func (c *Controller) render(exe string) []byte {
	s := Spec{Identity: c.p.Identity, Name: c.p.Name, Exe: exe, Env: c.p.Pinned, StderrPath: c.p.StderrFile, Role: c.d.Role}
	if c.d.GOOS == "darwin" {
		return RenderPlist(s)
	}
	return RenderUnit(s)
}

// ensureDefinition writes the definition when missing, or rewrites a stale
// one of ours that runs the same executable (or when --exe was given). A
// foreign file is never touched.
func (c *Controller) ensureDefinition(ctx context.Context, exe string, explicit bool, rep *Report) (bool, error) {
	if c.defExists && c.marker.Generated && !samePins(c.cliPins, c.marker.Env) {
		rep.Warnings = append(rep.Warnings, "this shell's XDG_DATA_HOME/XDG_CONFIG_HOME differ from those pinned in the service definition; the service keeps its own (to rebind: `"+c.d.Role.Command()+" uninstall`, then `"+c.d.Role.Command()+" start`)")
	}
	want := c.render(exe)
	switch {
	case !c.defExists:
	case !c.marker.Generated:
		return false, &Error{Code: CodeInvalid, Msg: c.p.Definition + " exists but was not written by `stage service`; leaving it untouched", Fix: "remove or rename it, then run the command again"}
	case bytes.Equal(c.def, want):
		return false, nil
	case c.marker.Exe != exe && !explicit:
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("the service runs %s, not this CLI (%s); kept it (pass --exe %s to switch)", c.marker.Exe, exe, exe))
		return false, nil
	}
	dirPerm := fs.FileMode(0o755)
	if c.d.GOOS == "darwin" {
		dirPerm = 0o700
	}
	if err := writeFileAtomic(c.p.Definition, want, 0o644, dirPerm); err != nil {
		return false, err
	}
	rep.Changes.DefinitionWritten = true
	c.def, c.defExists, c.marker = want, true, ParseDefinition(c.d.GOOS, want)
	if c.d.GOOS == "darwin" && c.mgr.LoginStartup(ctx, c.p, RunState{}) {
		if err := c.mgr.SetLoginStartup(ctx, c.p, true, want); err != nil {
			rep.Warnings = append(rep.Warnings, err.Error())
		}
	}
	return true, nil
}

// reload makes the manager see the definition and checks what it loaded.
func (c *Controller) reload(ctx context.Context, wrote bool, rs RunState, rep *Report) (RunState, error) {
	reloaded, err := c.mgr.Reload(ctx, c.p, wrote, rs)
	if err != nil {
		return rs, err
	}
	rep.Changes.ManagerReloaded = reloaded
	if rs, err = c.mgr.State(ctx, c.p); err != nil {
		return rs, err
	}
	if rs.Masked {
		return rs, &Error{Code: CodeInvalid, Msg: c.p.Unit + " is masked", Fix: "systemctl --user unmask " + c.p.Unit}
	}
	if c.d.GOOS != "darwin" && rs.LoadedFrom != "" && rs.LoadedFrom != c.p.Definition {
		return rs, &Error{Code: CodeInvalid, Msg: fmt.Sprintf("systemd loads %s from %s, not %s", c.p.Unit, rs.LoadedFrom, c.p.Definition), Fix: "remove the other unit file, or run `systemctl --user daemon-reload` if it is stale"}
	}
	return rs, nil
}

// StartOptions configure Start and Restart.
type StartOptions struct {
	Exe  string        // --exe: the executable the service runs (default this CLI)
	Wait time.Duration // readiness wait; 0 returns once the start is requested
}

// Start registers or updates the definition, starts the service unless it
// runs, and waits for readiness. It never restarts a running server.
func (c *Controller) Start(ctx context.Context, o StartOptions) Report {
	return c.startOrRestart(ctx, "start", o, false)
}

// Restart is Start that also restarts a running server.
func (c *Controller) Restart(ctx context.Context, o StartOptions) Report {
	return c.startOrRestart(ctx, "restart", o, true)
}

func (c *Controller) startOrRestart(ctx context.Context, cmd string, o StartOptions, restart bool) Report {
	rep := newReport(cmd)
	err := func() error {
		release, err := c.begin(ctx, o.Wait)
		if err != nil {
			return err
		}
		locked := true
		defer func() {
			if locked {
				release()
			}
		}()
		exe, err := c.resolveExe(ctx, o.Exe, &rep)
		if err != nil {
			return err
		}
		rs, err := c.mgr.State(ctx, c.p)
		if err != nil {
			return err
		}
		if err := c.foreign(ctx, rs); err != nil {
			return err
		}
		if err := c.roleConflict(); err != nil {
			return err
		}
		if err := c.conflict(rs); err != nil {
			return err
		}
		if err := c.ensureServiceEnv(&rep); err != nil {
			return err
		}
		changed, err := c.ensureDefinition(ctx, exe, o.Exe != "", &rep)
		if err != nil {
			return err
		}
		if rs, err = c.reload(ctx, changed, rs, &rep); err != nil {
			return err
		}
		deadline := time.Now().Add(o.Wait)
		// Stopping first (restart on launchd, or a draining service) is
		// bounded by the stop budget even with --wait 0.
		stopWait := o.Wait
		if stopWait <= 0 {
			stopWait = DefaultStopWait
		}
		oldInstance, started := "", false
		active := rs.Phase == PhaseRunning || rs.Phase == PhaseStarting
		switch {
		case restart && active:
			if c.d.Role == RoleWorker {
				if o := c.owner(); o.known {
					oldInstance = o.key
				}
			} else if h, err := c.d.Health(ctx, "unix://"+c.p.Socket); err == nil {
				oldInstance = h.InstanceID
			}
			inPlace, err := c.mgr.RestartInPlace(ctx, c.p, rs)
			if err != nil {
				return err
			}
			if !inPlace {
				if err := c.mgr.Stop(ctx, c.p); err != nil {
					return err
				}
				if err := c.waitStopped(ctx, rs.PID, stopWait); err != nil {
					return err
				}
				if rs, err = c.mgr.State(ctx, c.p); err != nil {
					return err
				}
				if err := c.mgr.Start(ctx, c.p, rs, false); err != nil {
					return err
				}
			}
			started = true
		case rs.Phase == PhaseRunning:
			if changed {
				rep.Changes.PendingRestart = true
				rep.Notes = append(rep.Notes, "the definition changed; `"+c.d.Role.Command()+" restart` applies it")
			}
		case rs.Phase == PhaseStarting:
		default:
			if rs.Phase == PhaseStopping {
				if err := c.waitStopped(ctx, rs.PID, stopWait); err != nil {
					return err
				}
				if rs, err = c.mgr.State(ctx, c.p); err != nil {
					return err
				}
			}
			if err := c.mgr.Start(ctx, c.p, rs, changed); err != nil {
				return err
			}
			started = true
		}
		rep.Changes.Started = started
		base, err := c.mgr.State(ctx, c.p)
		if err != nil {
			return err
		}
		release()
		locked = false
		if o.Wait <= 0 {
			rep.Message = cmd + " requested"
			return nil
		}
		return c.waitReady(ctx, base, oldInstance, deadline, &rep)
	}()
	return c.finish(ctx, &rep, err)
}

func (c *Controller) sleep(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(c.d.Poll):
		return nil
	}
}

// ready reports whether the running service answers on its socket as the
// data dir's holder (not a foreground server racing for the socket).
func (c *Controller) ready(ctx context.Context, rs RunState) (client.Health, bool) {
	h, err := c.d.Health(ctx, "unix://"+c.p.Socket)
	if err != nil {
		return h, false
	}
	_, holder, _ := instance.ReadHolder(c.p.DataDir)
	return h, holder != nil && holder.PID == rs.PID && holder.InstanceID == h.InstanceID
}

// workerReady reports whether the running worker service holds its worker
// dir (it locks it, loads its config, then notifies readiness); the channel
// being offline does not make it unready.
func (c *Controller) workerReady(rs RunState) (owner, bool) {
	o := c.owner()
	return o, o.held && o.known && o.pid == rs.PID
}

// workerNotes are the notes of a ready worker service: its version and its
// channel state when not online.
func (c *Controller) workerNotes(o owner) []string {
	var notes []string
	if o.version != buildinfo.Version {
		notes = append(notes, fmt.Sprintf("the worker is version %s; this CLI is %s", o.version, buildinfo.Version))
	}
	if st, _ := worker.ReadState(c.workerDir()); st != nil && st.PID == o.pid && st.State != worker.StateOnline {
		msg := "the worker is running but its channel is " + st.State
		if st.LastError != "" {
			msg += ": " + st.LastError
		}
		notes = append(notes, msg+" (`stage worker status`)")
	}
	return notes
}

// waitReady waits until the service is running and healthy (and, after a
// restart, a new instance), failing fast when the process exits.
func (c *Controller) waitReady(ctx context.Context, base RunState, oldInstance string, deadline time.Time, rep *Report) error {
	t0 := time.Now()
	for {
		rs, err := c.mgr.State(ctx, c.p)
		if err != nil {
			return err
		}
		switch {
		case rs.Phase == PhaseFailed:
			return c.failed(ctx, rs, "the service failed")
		case rs.AutoRestart || rs.Restarts > base.Restarts:
			return c.failed(ctx, rs, fmt.Sprintf("the process exited while starting; %s keeps restarting it (`"+c.d.Role.Command()+" stop` stops it)", c.mgr.Kind()))
		case rs.Phase == PhaseStopped && (time.Since(t0) > 2*time.Second || (c.d.GOOS == "darwin" && rs.Loaded && rs.LastExit != nil)):
			// launchd: a loaded job that is not running and has an exit
			// status ran and exited (bootout unloads, so it is from this start).
			return c.failed(ctx, rs, "the service is not running (the server exited)")
		case rs.Phase == PhaseRunning && rs.PID > 0 && c.d.Role == RoleWorker:
			if o, ok := c.workerReady(rs); ok && (oldInstance == "" || o.key != oldInstance) {
				rep.Message = fmt.Sprintf("stage worker %s ready (pid %d), worker config %s", o.version, rs.PID, c.workerConfig())
				rep.Notes = append(rep.Notes, c.workerNotes(o)...)
				if l := c.info.Linger; l != nil && !*l {
					rep.Notes = append(rep.Notes, "lingering is off: the service stops when you log out (`loginctl enable-linger` keeps it running)")
				}
				return nil
			}
		case rs.Phase == PhaseRunning && rs.PID > 0:
			if h, ok := c.ready(ctx, rs); ok && (oldInstance == "" || h.InstanceID != oldInstance) {
				rep.Message = fmt.Sprintf("stage %s ready (pid %d) at unix://%s", h.Version, rs.PID, c.p.Socket)
				if h.Version != buildinfo.Version {
					rep.Notes = append(rep.Notes, fmt.Sprintf("the server is version %s; this CLI is %s", h.Version, buildinfo.Version))
				}
				for _, hs := range offline(harnessStatuses(h)) {
					rep.Notes = append(rep.Notes, offlineMessage("Stage is ready; ", hs)+" (check its settings in "+c.p.ServiceEnv+" or Settings)")
				}
				if l := c.info.Linger; l != nil && !*l {
					rep.Notes = append(rep.Notes, "lingering is off: the service stops when you log out (`loginctl enable-linger` keeps it running)")
				}
				return nil
			}
		}
		if time.Now().After(deadline) {
			return &Error{Code: CodeTimeout, Msg: fmt.Sprintf("not ready in time (state %s); it may still be starting: `%s status`", rs.Phase, c.d.Role.Command()), LogTail: c.tail(ctx, rs)}
		}
		if err := c.sleep(ctx); err != nil {
			return err
		}
	}
}

func (c *Controller) tail(ctx context.Context, rs RunState) []string {
	t := c.mgr.LogTail(ctx, c.p, rs, 20)
	if len(t) == 0 {
		time.Sleep(300 * time.Millisecond) // journald lags
		t = c.mgr.LogTail(ctx, c.p, rs, 20)
	}
	return t
}

func (c *Controller) failed(ctx context.Context, rs RunState, msg string) error {
	e := &Error{Code: CodeFailed, Msg: msg, LogTail: c.tail(ctx, rs)}
	// On macOS a managed server exits 0 instead of 75 (launchd would
	// respawn it): a holder that is not the service means a conflict.
	if c.d.GOOS == "darwin" && (rs.LastExit == nil || *rs.LastExit == 0) {
		if o := c.owner(); o.held && o.known && !o.managed {
			conflict := ExitConflict
			rs.LastExit = &conflict
		}
	}
	switch {
	case rs.LastExit != nil && *rs.LastExit == ExitConflict:
		e.Code = CodeConflict
		e.Msg, e.Fix = c.lockedMsg()
	case rs.LastExit != nil && *rs.LastExit == ExitConfig && c.d.Role == RoleWorker:
		e.Code, e.Msg, e.Fix = CodeInvalid, "the worker refused its configuration (not joined, an invalid worker config, or its machine was removed)", "see the log lines and `stage worker status`; fix "+c.p.ServiceEnv+" or join again"
	case rs.LastExit != nil && *rs.LastExit == ExitConfig:
		e.Code, e.Msg, e.Fix = CodeInvalid, "the server refused its configuration", "see the log lines; fix "+c.p.ServiceEnv
	case rs.Result == "start-limit-hit":
		e.Msg += ": systemd stopped restarting it (start limit hit); fix the cause, then `" + c.d.Role.Command() + " start`"
	case rs.LastExit != nil:
		e.Msg += fmt.Sprintf(" (exit status %d)", *rs.LastExit)
	}
	return e
}

// stopped reports whether the manager no longer runs the service.
func (c *Controller) stopped(rs RunState) bool {
	if c.d.GOOS == "darwin" {
		return !rs.Loaded
	}
	return rs.Phase == PhaseStopped || rs.Phase == PhaseFailed
}

// waitStopped waits until the service is stopped and the data dir lock is
// free (or held by another pid): the lock proves the process exited.
func (c *Controller) waitStopped(ctx context.Context, oldPID int, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		rs, err := c.mgr.State(ctx, c.p)
		if err != nil {
			return err
		}
		o := c.owner()
		if c.stopped(rs) && (!o.held || (o.known && o.pid != oldPID)) {
			return nil
		}
		if time.Now().After(deadline) {
			if c.d.Role == RoleWorker {
				return &Error{Code: CodeTimeout, Msg: fmt.Sprintf("still stopping (state %s); the manager kills the worker at %ds", rs.Phase, WorkerStopTimeoutSec)}
			}
			return &Error{Code: CodeTimeout, Msg: fmt.Sprintf("still stopping (state %s): the server drains requests for up to 100s; the manager kills it at %ds", rs.Phase, StopTimeoutSec)}
		}
		if err := c.sleep(ctx); err != nil {
			return err
		}
	}
}

// Stop stops the service through its manager and waits until the server
// exited. A foreground server is never touched.
func (c *Controller) Stop(ctx context.Context, wait time.Duration) Report {
	rep := newReport("stop")
	err := func() error {
		release, err := c.begin(ctx, wait)
		if err != nil {
			return err
		}
		defer release()
		return c.stopLocked(ctx, wait, &rep)
	}()
	return c.finish(ctx, &rep, err)
}

func (c *Controller) stopLocked(ctx context.Context, wait time.Duration, rep *Report) error {
	rs, err := c.mgr.State(ctx, c.p)
	if err != nil {
		return err
	}
	if err := c.foreign(ctx, rs); err != nil {
		return err
	}
	switch {
	case !c.defExists && !rs.Loaded:
		rep.Message = "not installed"
	case c.stopped(rs):
		rep.Message = "not running"
	default:
		if err := c.mgr.Stop(ctx, c.p); err != nil {
			return err
		}
		rep.Changes.Stopped = true
		if err := c.waitStopped(ctx, rs.PID, wait); err != nil {
			return err
		}
		rep.Message = "stopped"
	}
	if o := c.owner(); o.held && o.known && !o.managed {
		if c.d.Role == RoleWorker {
			rep.Notes = append(rep.Notes, fmt.Sprintf("a foreground stage worker (pid %d) holds the worker dir; not touched", o.pid))
		} else {
			rep.Notes = append(rep.Notes, fmt.Sprintf("a foreground stage server (pid %d) holds the data dir; not touched", o.pid))
		}
	}
	return nil
}

// Enable turns login startup on (registering the definition, not starting).
func (c *Controller) Enable(ctx context.Context, exe string) Report {
	rep := newReport("enable")
	err := func() error {
		release, err := c.begin(ctx, 0)
		if err != nil {
			return err
		}
		defer release()
		e, err := c.resolveExe(ctx, exe, &rep)
		if err != nil {
			return err
		}
		if err := c.roleConflict(); err != nil {
			return err
		}
		if err := c.ensureServiceEnv(&rep); err != nil {
			return err
		}
		changed, err := c.ensureDefinition(ctx, e, exe != "", &rep)
		if err != nil {
			return err
		}
		rs, err := c.mgr.State(ctx, c.p)
		if err != nil {
			return err
		}
		if _, err := c.reload(ctx, changed, rs, &rep); err != nil {
			return err
		}
		if err := c.mgr.SetLoginStartup(ctx, c.p, true, c.def); err != nil {
			return err
		}
		on := true
		rep.Changes.LoginStartup = &on
		rep.Message = "login startup on (current state unchanged; `" + c.d.Role.Command() + " start` starts it now)"
		switch l := c.info.Linger; {
		case c.d.GOOS == "darwin":
			rep.Notes = append(rep.Notes, "macOS may show a Background Items notice; the agent loads when you log in and stops when you log out")
		case l != nil && *l:
			rep.Notes = append(rep.Notes, "lingering is on: it also starts at boot and keeps running after logout")
		default:
			rep.Notes = append(rep.Notes, "starts when you log in and stops when you log out (lingering is off)")
		}
		return nil
	}()
	return c.finish(ctx, &rep, err)
}

// Disable turns login startup off; it never stops the service.
func (c *Controller) Disable(ctx context.Context) Report {
	rep := newReport("disable")
	err := func() error {
		release, err := c.begin(ctx, 0)
		if err != nil {
			return err
		}
		defer release()
		rs, err := c.mgr.State(ctx, c.p)
		if err != nil {
			return err
		}
		if err := c.foreign(ctx, rs); err != nil {
			return err
		}
		off := false
		rep.Changes.LoginStartup = &off
		rep.Message = "login startup off (current state unchanged)"
		if !c.defExists || !c.mgr.LoginStartup(ctx, c.p, rs) {
			return nil
		}
		return c.mgr.SetLoginStartup(ctx, c.p, false, nil)
	}()
	return c.finish(ctx, &rep, err)
}

// Uninstall stops the service and removes its definition (and login
// startup); data, service.env and logs are kept.
func (c *Controller) Uninstall(ctx context.Context, wait time.Duration) Report {
	rep := newReport("uninstall")
	err := func() error {
		release, err := c.begin(ctx, wait)
		if err != nil {
			return err
		}
		defer release()
		if c.defExists && !c.marker.Generated {
			return &Error{Code: CodeInvalid, Msg: c.p.Definition + " was not written by `stage service`; leaving it untouched"}
		}
		if err := c.stopLocked(ctx, wait, &rep); err != nil {
			return err
		}
		rs, err := c.mgr.State(ctx, c.p)
		if err != nil {
			return err
		}
		if c.mgr.LoginStartup(ctx, c.p, rs) {
			if err := c.mgr.SetLoginStartup(ctx, c.p, false, nil); err != nil {
				rep.Warnings = append(rep.Warnings, err.Error())
			}
		}
		if c.defExists {
			if err := os.Remove(c.p.Definition); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			rep.Changes.DefinitionRemoved = true
			c.defExists, c.def, c.marker = false, nil, Marker{}
		}
		if err := c.mgr.Unregister(ctx, c.p); err != nil {
			return err
		}
		if c.d.Role == RoleWorker {
			rep.Message = fmt.Sprintf("uninstalled; kept the worker config %s and %s", c.workerConfig(), c.p.ServiceEnv)
		} else {
			rep.Message = fmt.Sprintf("uninstalled; kept the data dir %s and %s", c.p.DataDir, c.p.ServiceEnv)
		}
		return nil
	}()
	return c.finish(ctx, &rep, err)
}

// Logs writes the service's recent log lines (and follows them).
func (c *Controller) Logs(ctx context.Context, w io.Writer, n int, follow bool) error {
	if err := c.prepare(ctx); err != nil {
		return err
	}
	return c.mgr.Logs(ctx, c.p, w, n, follow)
}

// Status reports the service, its definition, the data dir holder, Stage's
// health and T3 separately, plus problems, with its exit code.
func (c *Controller) Status(ctx context.Context) (Status, int) {
	if err := c.prepare(ctx); err != nil {
		var e *Error
		code := CodeFailed
		if errors.As(err, &e) {
			code = e.Code
		}
		return Status{Schema: ReportSchema, Identity: buildinfo.Identity, State: "unknown", Problems: []Problem{{Code: "paths", Message: err.Error()}}}, code
	}
	st := c.status(ctx)
	switch {
	case !st.Manager.Reachable:
		return st, CodeUnavailable
	case st.Ready:
		return st, CodeOK
	case st.State == "not-installed" || st.State == "stopped":
		return st, CodeNotRunning
	case st.State == "starting" || st.State == "stopping":
		return st, CodeTimeout
	}
	return st, CodeFailed
}

func (c *Controller) status(ctx context.Context) Status {
	p := c.p
	st := Status{
		Schema: ReportSchema, Identity: p.Identity, DataDir: p.DataDir, Problems: []Problem{},
		Manager: ManagerStatus{Kind: ManagerKind(c.d.GOOS), Reachable: c.probeErr == nil, Version: c.info.Version, Linger: c.info.Linger},
	}
	add := func(code, msg, fix string) { st.Problems = append(st.Problems, Problem{code, msg, fix}) }
	rs := RunState{Phase: PhaseStopped}
	if c.probeErr != nil {
		st.Manager.Error = c.probeErr.Error()
		fix := ""
		var ue *UnavailableError
		if errors.As(c.probeErr, &ue) {
			fix = ue.Fix
		}
		add("manager_unavailable", c.probeErr.Error(), fix)
	} else if s, err := c.mgr.State(ctx, p); err != nil {
		st.Manager.Error = err.Error()
	} else {
		rs = s
	}
	st.Logs = LogsInfo{Kind: "journald", Unit: p.Unit}
	if c.mgr != nil {
		st.Logs = c.mgr.LogsInfo(p)
	}

	d := DefinitionStatus{Name: p.Name, Path: p.Definition, Present: c.defExists, LoginCopy: p.LoginCopy,
		LoadedFrom: rs.LoadedFrom, DropIns: rs.DropIns, NeedsReload: rs.NeedsReload}
	for _, kv := range p.Pinned {
		d.Pinned = append(d.Pinned, KVJSON{kv.Key, kv.Value})
	}
	if c.defExists {
		d.Generated, d.Schema, d.Exe = c.marker.Generated, c.marker.Schema, c.marker.Exe
		if fi, err := os.Stat(d.Exe); err == nil && fi.Mode().IsRegular() {
			d.ExeExists = true
		}
		d.Current = d.Generated && bytes.Equal(c.def, c.render(c.marker.Exe))
		switch {
		case !d.Generated:
			add("definition_foreign", p.Definition+" was not written by `stage service`", "remove or rename it to let `"+c.d.Role.Command()+" start` manage the service")
		case !d.Current:
			add("definition_stale", "the definition is from another Stage version or layout", "`"+c.d.Role.Command()+" start` (stopped) or `"+c.d.Role.Command()+" restart` rewrites it")
		}
		if d.Generated && !d.ExeExists {
			add("exe_missing", "the service's executable "+d.Exe+" does not exist", "reinstall Stage, or `"+c.d.Role.Command()+" start --exe PATH`")
		}
		if self := c.d.Self(); d.Generated && self != "" && d.Exe != self {
			add("exe_differs", fmt.Sprintf("the service runs %s; this CLI is %s", d.Exe, self), "`"+c.d.Role.Command()+" restart --exe "+self+"` switches it")
		}
		if d.Generated && !samePins(c.cliPins, c.marker.Env) {
			add("env_mismatch", "this shell's XDG_DATA_HOME/XDG_CONFIG_HOME differ from those pinned in the service definition", "the service keeps its own; to rebind: `"+c.d.Role.Command()+" uninstall`, then `"+c.d.Role.Command()+" start`")
		}
	}
	if c.mgr != nil && c.probeErr == nil {
		d.LoginStartup = c.mgr.LoginStartup(ctx, p, rs)
	}
	if rs.Masked {
		add("unit_masked", p.Unit+" is masked", "systemctl --user unmask "+p.Unit)
	}
	if c.mgr != nil && c.foreign(ctx, rs) != nil {
		add("definition_shadowed", p.Name+" is loaded from "+rs.LoadedFrom+", which `stage service` did not write", "remove or rename the other definition")
	}
	if rs.NeedsReload {
		add("needs_reload", "systemd has not loaded the current unit file", "`"+c.d.Role.Command()+" start` or `systemctl --user daemon-reload`")
	}
	if rs.Result == "start-limit-hit" {
		add("start_limit_hit", "systemd stopped restarting the service (start limit hit)", "fix the cause (see `"+c.d.Role.Command()+" logs`), then `"+c.d.Role.Command()+" start`")
	}
	st.Definition = d
	st.Process = ProcessStatus{PID: rs.PID, Restarts: rs.Restarts, LastExitStatus: rs.LastExit, Result: rs.Result}

	st.ServiceEnv = FileStatus{Path: p.ServiceEnv}
	if _, err := os.Stat(p.ServiceEnv); err == nil {
		st.ServiceEnv.Present = true
	}
	if b, ok, _ := readMaybe(p.OtherDefinition); ok && ParseDefinition(c.d.GOOS, b).Generated && c.defExists {
		add("role_conflict", fmt.Sprintf("the %s service is also installed (%s): a machine runs the Stage server or a worker under one identity, not both", roleNoun(c.d.Role.Other()), p.OtherDefinition), "`"+c.d.Role.Other().Command()+" uninstall` or `"+c.d.Role.Command()+" uninstall`")
	}
	if c.d.Role == RoleWorker {
		c.workerStatus(&st, rs, add)
		return st
	}
	vals, err := config.ReadServiceEnv(p.ServiceEnv)
	if err == nil {
		_, err = config.LoadManaged(c.svcEnv, vals)
	}
	if err != nil {
		st.ServiceEnv.Error = err.Error()
		add("service_env_invalid", err.Error(), "fix "+p.ServiceEnv)
	}

	st.Socket = SocketStatus{Path: p.Socket}
	if _, err := os.Lstat(p.Socket); err == nil {
		st.Socket.Exists = true
	}
	st.Socket.Locked, _ = filelock.Held(listen.LockPath(p.Socket))

	held, holder, _ := instance.ReadHolder(p.DataDir)
	st.Lock = LockStatus{Held: held, Holder: holder, OwnedByService: holder != nil && rs.PID != 0 && holder.PID == rs.PID}
	if held && holder != nil && !st.Lock.OwnedByService && !holder.Managed {
		add("foreground_holder", fmt.Sprintf("a foreground stage server (pid %d) holds the data dir", holder.PID), "stop it before starting the service")
	}

	st.Stage = StageStatus{URL: "unix://" + p.Socket}
	if st.Socket.Locked {
		h, err := c.d.Health(ctx, st.Stage.URL)
		if h.Service != "" || h.Status != "" {
			st.Stage.Reachable = true
			st.Stage.Version, st.Stage.APIVersion, st.Stage.InstanceID, st.Stage.DB = h.Version, h.APIVersion, h.InstanceID, h.DB
			st.Harnesses = harnessStatuses(h)
		}
		if err != nil {
			st.Stage.Error = err.Error()
		} else {
			st.Stage.Ready = true
		}
		if st.Stage.Reachable && h.Version != buildinfo.Version {
			add("version_differs", fmt.Sprintf("the server is version %s; this CLI is %s", h.Version, buildinfo.Version), "`stage service restart` after updating runs the new version")
		}
		if h.APIVersion != 0 && h.APIVersion != buildinfo.APIVersion {
			add("api_mismatch", fmt.Sprintf("the server speaks API v%d; this CLI v%d", h.APIVersion, buildinfo.APIVersion), "install the CLI and server from the same release")
		}
		for _, hs := range offline(st.Harnesses) {
			add("harness_offline", offlineMessage("Stage is running; ", hs), "start "+hs.Kind+" or check its settings in "+p.ServiceEnv+" or Settings (`stage harness "+hs.Kind+"`)")
		}
	}

	switch {
	case c.probeErr != nil:
		st.State = "unknown"
	case !c.defExists && !rs.Loaded:
		st.State = "not-installed"
	case c.d.GOOS == "darwin" && !rs.Loaded:
		st.State = string(PhaseStopped)
	case rs.Phase == PhaseStarting && (rs.AutoRestart || (rs.PID == 0 && rs.Restarts > 0)):
		st.State = string(PhaseFailed) // crash loop: the manager is waiting to restart it
	default:
		st.State = string(rs.Phase)
	}
	st.Ready = st.State == string(PhaseRunning) && st.Stage.Ready && st.Lock.OwnedByService
	if l := st.Manager.Linger; l != nil && !*l {
		add("linger_off", "lingering is off: the service stops when you log out", "`loginctl enable-linger` keeps your user services running after logout")
	}
	st.CLI = CLIStatus{Version: buildinfo.Version, APIVersion: buildinfo.APIVersion, Exe: c.d.Self(), AgentResolvesService: c.agentResolves()}
	if !st.CLI.AgentResolvesService {
		add("agent_socket_mismatch", "agent commands in this shell do not reach the service socket "+p.Socket,
			"export XDG_RUNTIME_DIR=/run/user/$(id -u) (Linux), or STAGE_URL=unix://"+p.Socket)
	}
	return st
}

// workerStatus fills the worker role's status (instead of the socket,
// data dir lock, Stage health and harnesses).
func (c *Controller) workerStatus(st *Status, rs RunState, add func(code, msg, fix string)) {
	p := c.p
	st.Role = string(RoleWorker)
	if _, err := config.ReadWorkerEnv(p.ServiceEnv); err != nil {
		st.ServiceEnv.Error = err.Error()
		add("service_env_invalid", err.Error(), "fix "+p.ServiceEnv)
	}
	path := c.workerConfig()
	w := &WorkerStatus{Config: path}
	st.Worker = w
	cfg, err := worker.LoadConfig(path)
	switch {
	case err == nil:
		w.Joined, w.URL, w.MachineID = true, cfg.URL, cfg.MachineID
	case errors.Is(err, worker.ErrNotJoined):
		add("worker_not_joined", "no worker config at "+path, "`stage worker join <link>`, then `stage worker service start`")
	default:
		w.Error = err.Error()
		add("worker_config_invalid", err.Error(), "fix "+path+" or join again")
	}
	dir := worker.Dir(path)
	held, h, _ := worker.ReadHolder(dir)
	w.Holder = h
	w.OwnedByService = h != nil && rs.PID != 0 && h.PID == rs.PID
	if held && h != nil && !w.OwnedByService && !h.Managed {
		add("foreground_holder", fmt.Sprintf("a foreground stage worker (pid %d) holds the worker dir", h.PID), "stop it before starting the service")
	}
	if ws, _ := worker.ReadState(dir); ws != nil && (h == nil || ws.PID == h.PID) {
		w.State, w.LastError = ws.State, ws.LastError
	}
	switch w.State {
	case worker.StateRevoked:
		add("worker_revoked", "the Stage server refused this machine's token: the machine was removed", "join again with a new pairing link (`stage worker leave --local`, then `stage worker join <link>`)")
	case worker.StateNeedsUpdate:
		add("api_mismatch", "the Stage server speaks another API version than this worker", "install the same Stage version on both machines")
	case worker.StateConnecting:
		if w.OwnedByService {
			msg := "the worker is running but not connected to " + w.URL
			if w.LastError != "" {
				msg += ": " + w.LastError
			}
			add("worker_offline", msg, "check that the server is up and reachable (`stage worker status`)")
		}
	}
	if h != nil && w.OwnedByService && h.Version != buildinfo.Version {
		add("version_differs", fmt.Sprintf("the worker is version %s; this CLI is %s", h.Version, buildinfo.Version), "`stage worker service restart` after updating runs the new version")
	}

	switch {
	case c.probeErr != nil:
		st.State = "unknown"
	case !c.defExists && !rs.Loaded:
		st.State = "not-installed"
	case c.d.GOOS == "darwin" && !rs.Loaded:
		st.State = string(PhaseStopped)
	case rs.Phase == PhaseStarting && (rs.AutoRestart || (rs.PID == 0 && rs.Restarts > 0)):
		st.State = string(PhaseFailed)
	default:
		st.State = string(rs.Phase)
	}
	st.Ready = st.State == string(PhaseRunning) && w.OwnedByService
	if l := st.Manager.Linger; l != nil && !*l {
		add("linger_off", "lingering is off: the service stops when you log out", "`loginctl enable-linger` keeps your user services running after logout")
	}
	st.CLI = CLIStatus{Version: buildinfo.Version, APIVersion: buildinfo.APIVersion, Exe: c.d.Self(), AgentResolvesService: true}
}

// agentResolves reports whether this shell's client resolution
// (docs/runtime.md) reaches the service socket.
func (c *Controller) agentResolves() bool {
	env := c.d.Getenv
	if u := env("STAGE_URL"); u != "" {
		return strings.TrimRight(u, "/") == "unix://"+c.p.Socket
	}
	var dir string
	if v := env("STAGE_DATA_DIR"); v != "" {
		abs, err := filepath.Abs(v)
		if err != nil {
			return false
		}
		dir = config.Canonical(abs)
	} else {
		d, err := config.DataDirFor(env, c.d.GOOS)
		if err != nil {
			return false
		}
		dir = config.Canonical(d)
	}
	sock, err := config.DefaultSocketFor(env, c.d.GOOS, dir)
	return err == nil && sock == c.p.Socket
}
