// Package launch ensures and supervises the desktop app's connection to
// the active Stage server (docs/desktop.md): for this machine's server it
// runs the bundled stage CLI (`stage service start --json`,
// docs/service.md) to start the per-user service and checks readiness over
// the Unix socket (GET /api/health); a remote server is only checked and
// watched, never managed. It produces the State the UI shows and switches
// servers. It does not import MyGo: the CLI, the HTTP client, the clock
// and the login-item calls are injected, so it is unit-tested with fake
// CLIs.
package launch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/flexdinesh/stage/desktop/app/target"
)

// ErrBusy is returned by an action while another one runs.
var ErrBusy = errors.New("another start, restart or retry is running")

// ErrRemote is returned by actions on this machine's service (start,
// restart, login startup) while a remote server is active.
var ErrRemote = errors.New("not available while connected to a remote Stage server; switch to this machine first")

// Timing bounds the launcher's waits.
type Timing struct {
	Version time.Duration // `stage version --json`
	Start   time.Duration // hard bound of `service start --wait 30s`
	Restart time.Duration // hard bound of `service restart --wait 30s`
	Status  time.Duration // `service status --json`
	Command time.Duration // logs, enable, disable
	// StartPoll bounds polling status after a start's --wait expired
	// (exit 3); StartPollEvery is its interval.
	StartPoll, StartPollEvery time.Duration
	// Ready bounds the readiness check (GET /api/health).
	Ready time.Duration
	// HealthEvery is the monitor's interval while ready, HealthTimeout a
	// single check's bound.
	HealthEvery, HealthTimeout time.Duration
	// BackoffMin and BackoffMax bound the retries while reconnecting.
	BackoffMin, BackoffMax time.Duration
	// ReconnectWindow is how long the server may be unreachable before
	// the launcher asks the service what happened (stopped or failed).
	ReconnectWindow time.Duration
}

// DefaultTiming is the production timing (docs/desktop.md).
func DefaultTiming() Timing {
	return Timing{
		Version: 10 * time.Second, Start: 40 * time.Second, Restart: 45 * time.Second,
		Status: 15 * time.Second, Command: 30 * time.Second,
		StartPoll: 120 * time.Second, StartPollEvery: 2 * time.Second,
		Ready:       10 * time.Second,
		HealthEvery: 5 * time.Second, HealthTimeout: 3 * time.Second,
		BackoffMin: time.Second, BackoffMax: 5 * time.Second,
		ReconnectWindow: 30 * time.Second,
	}
}

// Config wires a Launcher.
type Config struct {
	// CLI is the bundled stage CLI, by absolute path.
	CLI string
	// AppName and AppVersion describe the desktop app.
	AppName, AppVersion string
	// Dev is mygo.IsDev(): only a development build honors DevSocket.
	Dev bool
	// DevSocket (STAGE_DESKTOP_SOCKET) points a development build at a
	// foreground dev server; no service command runs.
	DevSocket string
	// TempDirs are refused as the app's location (nil: DefaultTempDirs).
	TempDirs []string
	Run      Runner // default ExecRunner
	Get      Getter // default TargetGetter
	Now      func() time.Time
	Timing   Timing // zero: DefaultTiming
	// OnChange receives every new state (coalesced, latest wins), off the
	// caller's goroutine.
	OnChange func(State)
	// Servers is the saved server list; its active one is the first
	// target (nil: this machine only).
	Servers *target.Store
	// OnTarget receives the target to proxy to: the active server each
	// time it becomes ready, a zero Target when a switch leaves none.
	OnTarget func(target.Target)
	// OnServer receives a server whose address is known or which became
	// ready again (the active one, or this machine's while a remote is
	// active), to reconnect its notification stream.
	OnServer func(target.Target)
	// OpenAtLogin and SetOpenAtLogin are MyGo's login item.
	OpenAtLogin    func() bool
	SetOpenAtLogin func(bool) error
	Logf           func(format string, args ...any)
}

// Launcher is the connection state machine. Its methods are safe for
// concurrent use.
type Launcher struct {
	cfg Config
	t   Timing

	mu        sync.Mutex
	st        State
	info      *buildInfo    // the bundled CLI, once read
	target    target.Target // the active server
	connected target.Target // the last ready target, zero before one
	local     string        // this machine's socket, once known
	monStop   context.CancelFunc
	life      context.Context
	stop      context.CancelFunc
	// gen counts switches; opGen is the gen of the ensure holding op, and
	// state changes of an older gen are dropped.
	gen, opGen uint64
	opCancel   context.CancelFunc

	op     sync.Mutex // one ensure at a time
	cbMu   sync.Mutex // orders OnTarget calls with switches
	notify chan struct{}
}

// New returns a launcher in the starting phase; Start runs it.
func New(cfg Config) *Launcher {
	if cfg.Run == nil {
		cfg.Run = ExecRunner
	}
	if cfg.Get == nil {
		cfg.Get = TargetGetter
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.TempDirs == nil {
		cfg.TempDirs = DefaultTempDirs()
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	t := cfg.Timing
	if t == (Timing{}) {
		t = DefaultTiming()
	}
	life, stop := context.WithCancel(context.Background())
	l := &Launcher{cfg: cfg, t: t, life: life, stop: stop, notify: make(chan struct{}, 1)}
	l.target = target.Local("")
	if cfg.Servers != nil {
		l.target = cfg.Servers.Active()
	}
	if l.devMode() {
		l.local = cfg.DevSocket
	}
	l.st = State{
		Phase: PhaseStarting, Step: stepPtr(firstStep(l.target)), Title: startTitle(l.target),
		Problems: []Problem{}, LogTail: []string{},
		App:    AppInfo{Name: cfg.AppName, Version: cfg.AppVersion},
		Target: l.target,
		Dev:    l.devMode(),
	}
	go l.deliver()
	return l
}

func (l *Launcher) devMode() bool { return l.cfg.Dev && l.cfg.DevSocket != "" }

// firstStep is where starting begins: the service for this machine, the
// server itself for a remote.
func firstStep(t target.Target) Step {
	if t.Remote() {
		return StepServer
	}
	return StepService
}

func startTitle(t target.Target) string {
	if t.Remote() {
		return "Connecting to " + t.Name
	}
	return "Starting Stage server"
}

// remote reports whether the active server is a remote one.
func (l *Launcher) remote() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.target.Remote()
}

// Active is the active server.
func (l *Launcher) Active() target.Target {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.target
}

// LocalTarget is this machine's server, with its socket once known (a
// ready, a status probe or the dev socket), else "".
func (l *Launcher) LocalTarget() target.Target {
	l.mu.Lock()
	defer l.mu.Unlock()
	return target.Local(l.local)
}

// Close stops the monitor and state delivery (tests; the app just quits).
func (l *Launcher) Close() { l.stop() }

// Start ensures the active server in the background. With a remote
// active, it also looks up this machine's socket (`service status`, read
// only) so its notifications keep coming.
func (l *Launcher) Start() {
	go func() {
		l.op.Lock()
		defer l.op.Unlock()
		l.ensure(false)
	}()
	if l.remote() {
		go l.probeLocal()
	}
}

// probeLocal learns this machine's socket without starting anything and
// hands it to OnServer.
func (l *Launcher) probeLocal() {
	sock := l.cfg.DevSocket
	if !l.devMode() {
		st, _, _ := l.status(l.life)
		if st == nil || st.Socket.Path == "" {
			return
		}
		sock = st.Socket.Path
	}
	l.mu.Lock()
	if l.local == "" {
		l.local = sock
	}
	sock = l.local
	l.mu.Unlock()
	if l.cfg.OnServer != nil {
		l.cfg.OnServer(target.Local(sock))
	}
}

// Switch makes the saved server id active: the open connection (bridge,
// streams) ends at once, the state starts over for the new server
// (ServerEpoch 0) and the start sequence runs in the background (the
// service for this machine; a remote is only checked). Switching to the
// active server only refreshes its name.
func (l *Launcher) Switch(id string) error {
	if l.cfg.Servers == nil {
		if id == target.LocalID {
			return nil
		}
		return target.ErrNotFound
	}
	t, ok := l.cfg.Servers.Get(id)
	if !ok {
		return target.ErrNotFound
	}
	if err := l.cfg.Servers.SetActive(id); err != nil {
		return err
	}
	l.cbMu.Lock()
	l.mu.Lock()
	cur := l.target
	if t.Kind == target.KindLocal {
		t.Socket = ""
	}
	if cur.ID == t.ID && cur.URL == t.URL {
		l.target.Name = t.Name
		l.st.Target.Name = t.Name
		if !l.connected.IsZero() {
			l.connected.Name = t.Name
		}
		l.mu.Unlock()
		l.cbMu.Unlock()
		l.poke()
		return nil
	}
	l.gen++
	gen := l.gen
	l.target, l.connected = t, target.Target{}
	if l.opCancel != nil {
		l.opCancel()
	}
	if l.monStop != nil {
		l.monStop()
		l.monStop = nil
	}
	l.st.Phase, l.st.Step, l.st.Title = PhaseStarting, stepPtr(firstStep(t)), startTitle(t)
	l.st.Message, l.st.Fix, l.st.Command = "", "", ""
	l.st.Problems, l.st.LogTail = []Problem{}, []string{}
	l.st.Server, l.st.Foreground, l.st.VersionSkew = nil, false, false
	l.st.Target, l.st.ServerEpoch = t, 0
	l.mu.Unlock()
	if l.cfg.OnTarget != nil {
		l.cfg.OnTarget(target.Target{})
	}
	l.cbMu.Unlock()
	l.poke()
	l.cfg.Logf("launch: switched to %s (%s)", t.ID, orNone(t.URL))
	go func() {
		l.op.Lock()
		defer l.op.Unlock()
		l.mu.Lock()
		stale := l.gen != gen
		l.mu.Unlock()
		if !stale {
			l.ensure(false)
		}
	}()
	if t.Remote() {
		go l.probeLocal()
	}
	return nil
}

// State is the current state.
func (l *Launcher) State() State {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.st.clone()
}

// Socket is the socket of the last ready server, "" before one (and for
// a remote).
func (l *Launcher) Socket() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.connected.Socket
}

// Retry runs the start sequence again (for a remote: checks it again);
// it returns once it settled.
func (l *Launcher) Retry() error { return l.action(false) }

// StartServer does what Retry does, which starts this machine's service
// when it is stopped; ErrRemote while a remote is active.
func (l *Launcher) StartServer() error {
	if l.remote() {
		return ErrRemote
	}
	return l.action(false)
}

// RestartServer restarts the service onto the bundled CLI (`service
// restart --exe <cli>`), e.g. after a version skew or API mismatch;
// ErrRemote while a remote is active.
func (l *Launcher) RestartServer() error {
	if l.remote() {
		return ErrRemote
	}
	if l.devMode() {
		return errors.New("restart is not available in a development run: restart the dev server")
	}
	return l.action(true)
}

func (l *Launcher) action(restart bool) error {
	if !l.op.TryLock() {
		return ErrBusy
	}
	defer l.op.Unlock()
	l.update(func(s *State) { s.Busy = true })
	defer l.update(func(s *State) { s.Busy = false })
	l.ensure(restart)
	return nil
}

func (l *Launcher) update(fn func(*State)) {
	l.mu.Lock()
	fn(&l.st)
	l.mu.Unlock()
	l.poke()
}

// apply runs fn on the state only while gen is the current switch
// generation; false when a switch made it stale.
func (l *Launcher) apply(gen uint64, fn func(*State)) bool {
	l.mu.Lock()
	if gen != l.gen {
		l.mu.Unlock()
		return false
	}
	fn(&l.st)
	l.mu.Unlock()
	l.poke()
	return true
}

// opGeneration is the switch generation of the running ensure.
func (l *Launcher) opGeneration() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.opGen
}

func (l *Launcher) poke() {
	select {
	case l.notify <- struct{}{}:
	default:
	}
}

func (l *Launcher) deliver() {
	for {
		select {
		case <-l.life.Done():
			return
		case <-l.notify:
			if l.cfg.OnChange != nil {
				l.cfg.OnChange(l.State())
			}
		}
	}
}

// outcome is a settled, non-ready result of the start sequence.
type outcome struct {
	phase                        Phase
	title, message, fix, command string
	problems                     []Problem
	logTail                      []string
}

// settle settles the running ensure's outcome (dropped after a switch).
func (l *Launcher) settle(o outcome) { l.settleGen(l.opGeneration(), o) }

func (l *Launcher) settleGen(gen uint64, o outcome) {
	if o.problems == nil {
		o.problems = []Problem{}
	}
	if o.logTail == nil {
		o.logTail = []string{}
	}
	if l.apply(gen, func(s *State) {
		s.Phase, s.Step = o.phase, nil
		s.Title, s.Message, s.Fix, s.Command = o.title, o.message, o.fix, o.command
		s.Problems, s.LogTail = o.problems, o.logTail
	}) {
		l.cfg.Logf("launch: %s: %s %s", o.phase, o.title, o.message)
	}
}

func (l *Launcher) progress(step Step) {
	l.apply(l.opGeneration(), func(s *State) {
		if s.Phase != PhaseStarting {
			s.Phase, s.Title = PhaseStarting, startTitle(s.Target)
			s.Message, s.Fix, s.Command = "", "", ""
			s.Problems, s.LogTail = []Problem{}, []string{}
		}
		s.Step = stepPtr(step)
	})
}

func (l *Launcher) stopMonitor() {
	l.mu.Lock()
	stop := l.monStop
	l.monStop = nil
	l.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// ensure runs the start (or restart) sequence to a settled state, then
// monitors the server if it became ready. The caller holds l.op.
func (l *Launcher) ensure(restart bool) {
	l.stopMonitor()
	ctx, cancel := context.WithCancel(l.life)
	defer cancel()
	l.mu.Lock()
	l.opGen, l.opCancel = l.gen, cancel
	t := l.target
	l.mu.Unlock()
	if t.Remote() {
		l.ensureRemote(ctx, t)
		return
	}
	l.progress(StepService)
	if l.devMode() {
		l.ensureDev(ctx)
		return
	}
	cli := l.cfg.CLI
	if err := ValidateLocation(cli, l.cfg.TempDirs); err != nil {
		fix := "Install Stage in a permanent folder (not Downloads' temporary copies, /tmp or the Trash), then open it again."
		if runtime.GOOS == "darwin" {
			fix = "Move Stage to the Applications folder, then open it again."
		}
		l.settle(outcome{phase: PhaseMisconfigured, title: "Stage can't run from this location",
			message: "The Stage server must keep running from where the app is installed: " + err.Error() + ".", fix: fix})
		return
	}
	if fi, err := os.Stat(cli); err != nil || !fi.Mode().IsRegular() {
		l.settle(outcome{phase: PhaseMisconfigured, title: "The bundled Stage server is missing",
			message: fmt.Sprintf("%s was not found.", cli), fix: "Reinstall Stage."})
		return
	}
	info, o := l.readVersion(ctx)
	if o != nil {
		l.settle(*o)
		return
	}
	verb, bound := "start", l.t.Start
	if restart {
		verb, bound = "restart", l.t.Restart
	}
	res := l.run(ctx, bound, "service", verb, "--exe", cli, "--wait", "30s", "--json")
	l.handleReport(ctx, info, verb, bound, res)
}

func (l *Launcher) run(ctx context.Context, bound time.Duration, args ...string) Result {
	ctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	return l.cfg.Run(ctx, l.cfg.CLI, args...)
}

// readVersion reads the bundled CLI's identity, version and API version.
func (l *Launcher) readVersion(ctx context.Context) (buildInfo, *outcome) {
	l.mu.Lock()
	cached := l.info
	l.mu.Unlock()
	if cached != nil {
		return *cached, nil
	}
	res := l.run(ctx, l.t.Version, "version", "--json")
	var bi buildInfo
	if res.Err != nil || res.Code != 0 || parseJSON(res.Stdout, &bi) != nil || bi.Identity == "" {
		msg := fmt.Sprintf("%s version --json failed (exit %d)", l.cfg.CLI, res.Code)
		if res.Err != nil {
			msg = fmt.Sprintf("%s version --json: %v", l.cfg.CLI, res.Err)
		}
		return bi, &outcome{phase: PhaseFailed, title: "The bundled Stage server does not run",
			message: msg, fix: "Reinstall Stage.", logTail: lines(res.Stderr, 50)}
	}
	l.mu.Lock()
	l.info = &bi
	l.mu.Unlock()
	l.update(func(s *State) { s.App.Identity, s.App.APIVersion = bi.Identity, bi.APIVersion })
	return bi, nil
}

// handleReport maps a `service start|restart --json` result to a state
// (exit codes of docs/service.md).
func (l *Launcher) handleReport(ctx context.Context, info buildInfo, verb string, bound time.Duration, res Result) {
	if res.Err != nil {
		l.settle(outcome{phase: PhaseFailed, title: "Stage server did not start",
			message: fmt.Sprintf("stage service %s did not finish within %s.", verb, bound),
			fix:     "Retry. If it keeps failing, copy the diagnostics.", logTail: lines(res.Stderr, 50)})
		return
	}
	var rep report
	if err := parseJSON(res.Stdout, &rep); err != nil {
		l.settle(outcome{phase: PhaseFailed, title: "Stage server failed to start",
			message: fmt.Sprintf("stage service %s exited %d without a report.", verb, res.Code),
			fix:     "Retry. If it keeps failing, copy the diagnostics.", logTail: lines(res.Stderr, 50)})
		return
	}
	switch res.Code {
	case codeOK:
		if rep.Status == nil || rep.Status.Socket.Path == "" {
			l.settle(outcome{phase: PhaseFailed, title: "Stage server failed to start",
				message: "stage service " + verb + " reported success without a socket.", logTail: lines(res.Stderr, 50)})
			return
		}
		l.connect(ctx, info, rep.Status.Socket.Path, holderPID(rep.Status), false)
	case codeTimeout:
		l.awaitStatus(ctx, info)
	case codeConflict:
		l.foreground(ctx, info, rep.Status)
	case codeUnavailable:
		l.settle(l.unsupported(rep.Message, rep.Fix, rep.Status.problems()))
	case codeInvalid:
		l.settle(outcome{phase: PhaseMisconfigured, title: "Stage server configuration problem",
			message: rep.Message, fix: rep.Fix, problems: rep.Status.problems()})
	default:
		tail := rep.LogTail
		if len(tail) == 0 {
			tail = l.logTail(ctx)
		}
		l.settle(outcome{phase: PhaseFailed, title: "Stage server failed to start",
			message: rep.Message, fix: rep.Fix, problems: rep.Status.problems(), logTail: tail})
	}
}

func holderPID(st *status) *int {
	if st == nil || st.Lock.Holder == nil || st.Lock.Holder.PID == 0 {
		return nil
	}
	pid := st.Lock.Holder.PID
	return &pid
}

func (l *Launcher) unsupported(msg, fix string, problems []Problem) outcome {
	if fix == "" {
		fix = "Log in through a desktop session, or run the server yourself in a terminal:"
	}
	return outcome{phase: PhaseUnsupported, title: "No per-user service manager",
		message: msg, fix: fix, command: shellQuote(l.cfg.CLI) + " serve --listen unix", problems: problems}
}

// status runs `service status --json`; ok is false without a status
// document.
func (l *Launcher) status(ctx context.Context) (st *status, code int, res Result) {
	res = l.run(ctx, l.t.Status, "service", "status", "--json")
	if res.Err != nil {
		return nil, res.Code, res
	}
	var s status
	if parseJSON(res.Stdout, &s) != nil {
		return nil, res.Code, res
	}
	return &s, res.Code, res
}

// awaitStatus polls status after a start whose --wait expired (exit 3).
func (l *Launcher) awaitStatus(ctx context.Context, info buildInfo) {
	l.progress(StepServer)
	deadline := l.cfg.Now().Add(l.t.StartPoll)
	for {
		st, code, res := l.status(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case st == nil:
			l.settle(outcome{phase: PhaseFailed, title: "Stage server failed to start",
				message: fmt.Sprintf("stage service status exited %d without a report.", res.Code), logTail: lines(res.Stderr, 50)})
			return
		case code == codeOK && st.Socket.Path != "":
			l.connect(ctx, info, st.Socket.Path, holderPID(st), false)
			return
		case code == codeTimeout:
			// still starting
		default:
			if o, fg := l.statusOutcome(ctx, st, code); fg {
				l.foreground(ctx, info, st)
			} else {
				l.settle(o)
			}
			return
		}
		if !l.cfg.Now().Before(deadline) {
			l.settle(outcome{phase: PhaseFailed, title: "Stage server is taking too long to start",
				message: fmt.Sprintf("It was still starting after %s.", l.t.StartPoll),
				fix:     "Retry, or check the server log.", problems: st.problems(), logTail: l.logTail(ctx)})
			return
		}
		if !sleep(ctx, l.t.StartPollEvery) {
			return
		}
	}
}

// statusOutcome maps a status that is not ready to a state; fg reports a
// foreground server holding the data dir instead.
func (l *Launcher) statusOutcome(ctx context.Context, st *status, code int) (o outcome, fg bool) {
	for _, p := range st.Problems {
		if p.Code == "foreground_holder" {
			return outcome{}, true
		}
	}
	switch {
	case code == codeUnavailable:
		return l.unsupported("The per-user service manager is not reachable.", "", st.problems()), false
	case code == codeNotRunning || st.State == "stopped" || st.State == "not-installed":
		return outcome{phase: PhaseStopped, title: "Stage server stopped",
			message: "The Stage service is not running.", fix: "Start the server to continue.", problems: st.problems()}, false
	}
	return outcome{phase: PhaseFailed, title: "Stage server failed",
		message: fmt.Sprintf("The Stage service is %s.", st.State), fix: "Start the server again, or copy the diagnostics.",
		problems: st.problems(), logTail: l.logTail(ctx)}, false
}

// foreground handles a data dir held by a server started from a terminal
// (exit 5): use it when it answers on the desktop socket with this app's
// identity and API, else explain the conflict.
func (l *Launcher) foreground(ctx context.Context, info buildInfo, st *status) {
	l.progress(StepServer)
	if st == nil {
		st, _, _ = l.status(ctx)
	}
	var sock string
	var pid *int
	if st != nil {
		sock, pid = st.Socket.Path, holderPID(st)
	}
	reason := " but does not answer on the desktop socket."
	if sock != "" {
		l.progress(StepAPI)
		h, err := l.waitHealth(ctx, target.Local(sock), l.t.Ready)
		switch {
		case err != nil || h.Service != "stage":
		case h.Identity != info.Identity:
			reason = fmt.Sprintf(" but belongs to another installation (%s).", h.Identity)
		case h.APIVersion != info.APIVersion:
			reason = fmt.Sprintf(" but speaks API v%d; this app needs v%d.", h.APIVersion, info.APIVersion)
		default:
			l.ready(info, target.Local(sock), h, false, pid, true)
			return
		}
	}
	who := "A Stage server started from a terminal"
	if pid != nil {
		who = fmt.Sprintf("A Stage server started from a terminal (pid %d)", *pid)
	}
	l.settle(outcome{phase: PhaseConflict, title: "Another Stage server holds your data",
		message: who + " holds the data directory" + reason,
		fix:     "Stop that server (Ctrl+C in its terminal), then Retry. To keep using it, restart it with the desktop socket:",
		command: "stage serve --listen unix,127.0.0.1:7380", problems: st.problems()})
}

// connect checks readiness on sock and settles ready (or incompatible).
func (l *Launcher) connect(ctx context.Context, info buildInfo, sock string, pid *int, foreground bool) {
	l.progress(StepAPI)
	h, err := l.waitHealth(ctx, target.Local(sock), l.t.Ready)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		l.settle(outcome{phase: PhaseFailed, title: "Stage server does not answer",
			message: fmt.Sprintf("No answer on %s within %s: %v", sock, l.t.Ready, err),
			fix:     "Retry. If it keeps failing, restart the server.", logTail: l.logTail(ctx)})
		return
	}
	l.ready(info, target.Local(sock), h, !foreground, pid, foreground)
}

// waitHealth polls GET /api/health on t for up to bound; a refusal by
// the server's origin policy (403) ends it at once.
func (l *Launcher) waitHealth(ctx context.Context, t target.Target, bound time.Duration) (Health, error) {
	ctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	for {
		h, err := l.health(ctx, t, bound)
		if err == nil {
			return h, nil
		}
		var he *HTTPError
		if errors.As(err, &he) && he.Status == 403 {
			return h, err
		}
		if !sleep(ctx, 250*time.Millisecond) {
			return h, err
		}
	}
}

func (l *Launcher) health(ctx context.Context, t target.Target, bound time.Duration) (Health, error) {
	ctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	var h Health
	if err := l.cfg.Get(ctx, t, "/api/health", &h); err != nil {
		return h, err
	}
	if h.Service != "stage" {
		return h, fmt.Errorf("%s is not a Stage server (service %q)", where(t), h.Service)
	}
	return h, nil
}

// where names a target in messages: its socket or URL.
func where(t target.Target) string {
	if t.Remote() {
		return t.URL
	}
	return t.Socket
}

// ready settles the ready phase (or incompatible on another API version)
// for t and starts the monitor; dropped when a switch made the running
// ensure stale.
func (l *Launcher) ready(info buildInfo, t target.Target, h Health, managed bool, pid *int, foreground bool) {
	gen := l.opGeneration()
	if info.Identity == "" && !t.Remote() { // a dev run: the dev server stands in for the bundled CLI
		info = buildInfo{Identity: h.Identity, Version: h.Version, APIVersion: h.APIVersion}
		l.mu.Lock()
		l.info = &info
		l.mu.Unlock()
		l.update(func(s *State) { s.App.Identity, s.App.APIVersion = h.Identity, h.APIVersion })
	}
	if t.Remote() {
		managed, pid, foreground = false, nil, false
	}
	srv := &Server{ID: t.ID, Version: h.Version, APIVersion: h.APIVersion, Identity: h.Identity,
		PublicURL: h.PublicURL, Socket: t.Socket, Managed: managed, PID: pid}
	if info.APIVersion != 0 && h.APIVersion != info.APIVersion {
		if !l.apply(gen, func(s *State) { s.Server = srv }) {
			return
		}
		o := outcome{phase: PhaseIncompatible, title: "Stage server is incompatible",
			message: fmt.Sprintf("The running server (%s) speaks API v%d; this app needs v%d.", h.Version, h.APIVersion, info.APIVersion),
			fix:     "Restart the Stage server onto the version bundled with this app.", command: "stage service restart"}
		if t.Remote() {
			o.message = fmt.Sprintf("The server on %s (%s) speaks API v%d; this app needs v%d.", t.Name, h.Version, h.APIVersion, info.APIVersion)
			o.fix, o.command = "Update Stage on that machine to match this app, or switch to another server.", ""
		}
		l.settleGen(gen, o)
		l.startMonitor(gen, t, info, h, managed, pid, foreground)
		return
	}
	l.cbMu.Lock()
	ok := l.apply(gen, func(s *State) {
		s.Phase, s.Step, s.Title = PhaseReady, nil, "Connected"
		s.Message, s.Fix, s.Command = "", "", ""
		s.Problems, s.LogTail = []Problem{}, []string{}
		s.Server, s.Foreground = srv, foreground
		s.VersionSkew = !t.Remote() && info.Version != "" && h.Version != info.Version
		s.Target = t
		s.Epoch++
		s.ServerEpoch++
		// Under the same lock as the generation check.
		l.connected = t
		if !t.Remote() {
			l.local = t.Socket
		}
	})
	if ok && l.cfg.OnTarget != nil {
		l.cfg.OnTarget(t)
	}
	l.cbMu.Unlock()
	if !ok {
		return
	}
	if l.cfg.OnServer != nil {
		l.cfg.OnServer(t)
	}
	l.cfg.Logf("launch: ready on %s (server %s, api v%d, foreground %v)", where(t), h.Version, h.APIVersion, foreground)
	l.startMonitor(gen, t, info, h, managed, pid, foreground)
}

func (l *Launcher) startMonitor(gen uint64, t target.Target, info buildInfo, h Health, managed bool, pid *int, foreground bool) {
	ctx, cancel := context.WithCancel(l.life)
	l.mu.Lock()
	if gen != l.gen {
		l.mu.Unlock()
		cancel()
		return
	}
	if l.monStop != nil {
		l.monStop()
	}
	l.monStop = cancel
	l.mu.Unlock()
	go l.monitor(ctx, monitorState{gen: gen, t: t, info: info, instance: h.InstanceID, managed: managed, pid: pid, foreground: foreground})
}

// logTail is the last 50 server log lines (`service logs -n 50`), or nil.
func (l *Launcher) logTail(ctx context.Context) []string {
	if l.devMode() || l.remote() {
		return nil
	}
	res := l.run(ctx, l.t.Command, "service", "logs", "-n", "50")
	if res.Err != nil || res.Code != 0 {
		return nil
	}
	return lines(res.Stdout, 50)
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// Identity is the bundled CLI's identity (the deep-link scheme), "" until
// read.
func (l *Launcher) Identity() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.info != nil {
		return l.info.Identity
	}
	return l.st.App.Identity
}

// T3PublicURL is T3's public URL as the active server knows it (GET
// /api/t3/status), for opening a T3 thread in the browser.
func (l *Launcher) T3PublicURL(ctx context.Context) (string, error) {
	l.mu.Lock()
	t := l.connected
	l.mu.Unlock()
	if t.IsZero() {
		return "", errors.New("the Stage server is not connected")
	}
	var st struct {
		PublicURL *string `json:"publicUrl"`
	}
	if err := l.cfg.Get(ctx, t, "/api/t3/status", &st); err != nil {
		return "", err
	}
	if st.PublicURL == nil || strings.TrimSpace(*st.PublicURL) == "" {
		return "", errors.New("T3 has no public URL configured")
	}
	return strings.TrimRight(*st.PublicURL, "/"), nil
}
