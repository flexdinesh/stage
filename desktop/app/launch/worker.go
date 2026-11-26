package launch

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/flexdinesh/stage/desktop/app/target"
)

// WorkerStatus is where this machine's worker stands (the TS
// HostWorkerStatus of app/src/platform/index.ts).
type WorkerStatus string

// Worker statuses.
const (
	// WorkerStarting: the worker service is starting.
	WorkerStarting WorkerStatus = "starting"
	// WorkerOnline: the worker runs and its channel to the main server is up.
	WorkerOnline WorkerStatus = "online"
	// WorkerOffline: joined, but not connected (the service is stopped,
	// or the main server is unreachable).
	WorkerOffline WorkerStatus = "offline"
	// WorkerNeedsUpdate: the worker and main server have no shared channel protocol.
	WorkerNeedsUpdate WorkerStatus = "needs_update"
	// WorkerRemoved: the main server removed this machine (token revoked).
	WorkerRemoved WorkerStatus = "removed"
	// WorkerFailed: the worker service failed.
	WorkerFailed WorkerStatus = "failed"
)

// Worker is this machine as a worker of a main server (`stage worker
// service`), the TS HostWorker field for field.
type Worker struct {
	// URL is the main server's URL from the worker config.
	URL       string `json:"url"`
	MachineID int64  `json:"machineId"`
	// ServerID is the saved server that is this main server, null when
	// none matches its URL.
	ServerID *string `json:"serverId"`
	// ServerName is that server's name, else the URL's host.
	ServerName string       `json:"serverName"`
	Status     WorkerStatus `json:"status"`
	// Message is a detail sentence, or "".
	Message string `json:"message"`
	// Fix says what to do about it, or "".
	Fix string `json:"fix"`
}

func (w *Worker) clone() *Worker {
	if w == nil {
		return nil
	}
	out := *w
	if w.ServerID != nil {
		id := *w.ServerID
		out.ServerID = &id
	}
	return &out
}

// ErrWorkerDev is returned by worker actions in a development run (no
// service is managed there).
var ErrWorkerDev = errors.New("the worker is not available in a development run")

// workerService is the part of `stage worker service status --json` (and
// of the reports embedding it) the app reads.
type workerService struct {
	State      string `json:"state"`
	Ready      bool   `json:"ready"`
	Definition struct {
		Present      bool `json:"present"`
		LoginStartup bool `json:"loginStartup"`
	} `json:"definition"`
	Worker *struct {
		Joined         bool   `json:"joined"`
		URL            string `json:"url"`
		MachineID      int64  `json:"machineId"`
		OwnedByService bool   `json:"ownedByService"`
		State          string `json:"state"`
		LastError      string `json:"lastError"`
		Error          string `json:"error"`
	} `json:"worker"`
	Problems []Problem `json:"problems"`
}

func (s *workerService) problem(code string) (Problem, bool) {
	for _, p := range s.Problems {
		if p.Code == code {
			return p, true
		}
	}
	return Problem{}, false
}

// workerState keeps the worker's last known state and serializes the
// worker CLI calls.
type workerState struct {
	cli  sync.Mutex // one worker status or action command at a time
	op   sync.Mutex // one join, leave or restart at a time
	kick chan struct{}
}

// Worker is this machine's worker, nil when it is not joined.
func (l *Launcher) Worker() *Worker {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.st.Worker.clone()
}

// joined reports whether this machine is a worker (as last read).
func (l *Launcher) joined() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.st.Worker != nil
}

// workerStatus runs `worker service status --json`; nil without a status
// document.
func (l *Launcher) workerStatus(ctx context.Context) *workerService {
	res := l.run(ctx, l.t.Status, "worker", "service", "status", "--json")
	if res.Err != nil {
		return nil
	}
	var s workerService
	if parseJSON(res.Stdout, &s) != nil {
		return nil
	}
	return &s
}

// refreshWorker reads the worker service's status and publishes it. A
// development run has no worker. It returns the new worker (nil: not
// joined, or unknown).
func (l *Launcher) refreshWorker(ctx context.Context) *Worker {
	if l.devMode() {
		return nil
	}
	l.w.cli.Lock()
	s := l.workerStatus(ctx)
	l.w.cli.Unlock()
	if ctx.Err() != nil {
		return l.Worker()
	}
	w := l.workerFrom(s)
	l.setWorker(w)
	return w
}

// setWorker publishes w; a change of joined tells OnWorker, and leaving
// while this machine's target shows the worker screen starts its server.
func (l *Launcher) setWorker(w *Worker) {
	l.mu.Lock()
	was := l.st.Worker != nil
	l.st.Worker = w
	now := w != nil
	if w != nil && l.st.Phase == PhaseWorker {
		l.st.Title = workerTitle(w)
	}
	leftOnScreen := was && !now && !l.target.Remote() && l.st.Phase == PhaseWorker
	l.mu.Unlock()
	l.poke()
	if was != now {
		l.cfg.Logf("launch: worker joined %v", now)
		if l.cfg.OnWorker != nil {
			l.cfg.OnWorker(now)
		}
		select {
		case l.w.kick <- struct{}{}:
		default:
		}
	}
	if leftOnScreen {
		go func() {
			l.op.Lock()
			defer l.op.Unlock()
			l.ensure(false)
		}()
	}
}

// workerFrom maps a worker service status to the worker the page shows.
func (l *Launcher) workerFrom(s *workerService) *Worker {
	if s == nil || s.Worker == nil {
		return l.Worker() // unknown: keep the last one
	}
	sw := s.Worker
	if !sw.Joined {
		return nil
	}
	w := &Worker{URL: sw.URL, MachineID: sw.MachineID}
	w.ServerID, w.ServerName = l.matchServer(sw.URL)
	switch {
	case sw.State == "revoked":
		w.Status = WorkerRemoved
		w.Message = w.ServerName + " removed this machine."
		w.Fix = "Leave to clean up, then join again to serve it."
	case sw.State == "needs_update":
		w.Status = WorkerNeedsUpdate
		w.Message = sw.LastError
		if w.Message == "" {
			w.Message = "The worker and main machine need a shared channel protocol version."
		}
	case s.State == "failed" || s.State == "unknown":
		w.Status = WorkerFailed
		w.Message = "The worker service failed."
		if s.State == "unknown" {
			w.Message = "The per-user service manager is not reachable."
		}
		w.Fix = "Restart the worker. If it keeps failing, copy the diagnostics."
		if p, ok := s.problem("foreground_holder"); ok {
			w.Message, w.Fix = p.Message+".", "Stop that worker, then restart the worker."
		}
	case s.State == "starting":
		w.Status = WorkerStarting
	case s.Ready && sw.State == "online":
		w.Status = WorkerOnline
	case s.Ready:
		w.Status = WorkerOffline
		w.Message = "Not connected to " + w.ServerName + "."
		if sw.LastError != "" {
			w.Message = "Not connected to " + w.ServerName + ": " + sw.LastError
		}
		w.Fix = "It reconnects by itself once " + w.ServerName + " is reachable."
	default:
		w.Status = WorkerOffline
		w.Message = "The worker service is not running."
		w.Fix = "Restart the worker."
	}
	if sw.Error != "" {
		w.Status, w.Message, w.Fix = WorkerFailed, sw.Error, "Leave, then join again."
	}
	return w
}

// matchServer finds the saved server at the main server's URL (or whose
// public URL it is, for the active one): its id and name, else nil and the
// URL's host.
func (l *Launcher) matchServer(raw string) (*string, string) {
	norm, err := target.NormalizeURL(raw)
	if err != nil {
		norm = raw
	}
	l.mu.Lock()
	active := l.target
	var public string
	if s := l.st.Server; s != nil && s.ID == active.ID && s.PublicURL != nil {
		public, _ = target.NormalizeURL(*s.PublicURL)
	}
	l.mu.Unlock()
	if active.Remote() && public != "" && public == norm {
		id := active.ID
		return &id, active.Name
	}
	if l.cfg.Servers != nil {
		for _, t := range l.cfg.Servers.List() {
			if t.Remote() && t.URL == norm {
				id := t.ID
				return &id, t.Name
			}
		}
	}
	if u, err := url.Parse(norm); err == nil && u.Host != "" {
		return nil, u.Host
	}
	return nil, raw
}

func workerTitle(w *Worker) string { return "This machine is a worker of " + w.ServerName }

// workerOutcome is the worker screen shown for this machine's target
// while it is joined.
func workerOutcome(w *Worker) outcome {
	return outcome{phase: PhaseWorker, title: workerTitle(w),
		message: "Its own Stage server is off while it runs " + w.ServerName + "'s tasks. Its data stays on this machine.",
		fix:     "Leave " + w.ServerName + " to use this machine's server again."}
}

// workerLoop refreshes the worker every WorkerEvery while it is joined,
// and on a kick (an action, a switch, a retry) otherwise.
func (l *Launcher) workerLoop() {
	for {
		every := l.t.WorkerEvery
		if !l.joined() {
			every = 0
		}
		var tick <-chan time.Time
		var timer *time.Timer
		if every > 0 {
			timer = time.NewTimer(every)
			tick = timer.C
		}
		select {
		case <-l.life.Done():
		case <-l.w.kick:
		case <-tick:
		}
		if timer != nil {
			timer.Stop()
		}
		if l.life.Err() != nil {
			return
		}
		l.refreshWorker(l.life)
	}
}

// kickWorker asks the loop to refresh now.
func (l *Launcher) kickWorker() {
	select {
	case l.w.kick <- struct{}{}:
	default:
	}
}

// workerAction runs one worker CLI command; err carries its message (the
// report's or stderr's) when it did not exit 0 (or 3, still starting, when
// okTimeout).
func (l *Launcher) workerAction(ctx context.Context, bound time.Duration, okTimeout bool, args ...string) (Result, error) {
	l.w.cli.Lock()
	res := l.run(ctx, bound, args...)
	l.w.cli.Unlock()
	cmd := "stage " + strings.Join(args[:min(len(args), 3)], " ")
	switch {
	case res.Err != nil:
		return res, fmt.Errorf("%s: %w", cmd, res.Err)
	case res.Code == codeOK, okTimeout && res.Code == codeTimeout:
		return res, nil
	}
	var rep report
	if parseJSON(res.Stdout, &rep) == nil && rep.Message != "" {
		msg := rep.Message
		if rep.Fix != "" {
			msg += " (" + rep.Fix + ")"
		}
		return res, &CommandError{Code: res.Code, Msg: msg}
	}
	// Prefer the CLI's "error: ..." line (a usage error prints the usage after it).
	errLines := lines(res.Stderr, 50)
	for _, l := range errLines {
		if msg, found := strings.CutPrefix(l, "error: "); found {
			return res, &CommandError{Code: res.Code, Msg: msg}
		}
	}
	if len(errLines) > 0 {
		return res, &CommandError{Code: res.Code, Msg: errLines[len(errLines)-1]}
	}
	return res, &CommandError{Code: res.Code, Msg: fmt.Sprintf("%s exited %d", cmd, res.Code)}
}

// CommandError is a CLI command that exited non-zero.
type CommandError struct {
	Code int
	Msg  string
}

func (e *CommandError) Error() string { return e.Msg }

func (l *Launcher) workerOp() (func(), error) {
	if l.devMode() {
		return nil, ErrWorkerDev
	}
	if !l.w.op.TryLock() {
		return nil, errors.New("another worker action is running")
	}
	return l.w.op.Unlock, nil
}

// JoinWorker makes this machine a worker of the main server in link (a
// pairing link the page created on the active remote): `worker join`,
// then this machine's own server service is uninstalled (one role per
// identity; its data stays) and the worker service started. When the
// server service cannot be uninstalled, or no service manager can run
// the worker, the join is undone (`worker leave`). Login startup moves
// from the server service to the worker service.
func (l *Launcher) JoinWorker(ctx context.Context, link string) error {
	release, err := l.workerOp()
	if err != nil {
		return err
	}
	defer release()
	if !l.remote() {
		return errors.New("switch to the server this machine should serve first")
	}
	if w := l.refreshWorker(ctx); w != nil {
		return fmt.Errorf("this machine is already a worker of %s; leave it first", w.ServerName)
	}
	cli := l.cfg.CLI
	if _, err := l.workerAction(ctx, l.t.Command, false, "worker", "join", "--json", link); err != nil {
		return fmt.Errorf("join: %w", err)
	}
	login := false
	if st, _, _ := l.status(ctx); st != nil {
		login = st.Definition.LoginStartup
	}
	undo := func(cause error) error {
		if _, err := l.workerAction(ctx, l.t.Command, false, "worker", "leave", "--json"); err != nil {
			l.cfg.Logf("launch: undo join: %v", err)
			_, _ = l.workerAction(ctx, l.t.Command, false, "worker", "leave", "--local", "--json")
		}
		l.refreshWorker(ctx)
		return cause
	}
	if _, err := l.workerAction(ctx, l.t.Restart, false, "service", "uninstall", "--wait", "30s", "--json"); err != nil {
		return undo(fmt.Errorf("this machine's Stage server could not be stopped: %w", err))
	}
	l.setWorker(l.workerFrom(l.workerStatus(ctx))) // joined: the local stream and probe stop
	_, err = l.workerAction(ctx, l.t.Start, true, "worker", "service", "start", "--exe", cli, "--wait", "30s", "--json")
	var ce *CommandError
	if errors.As(err, &ce) && ce.Code == codeUnavailable {
		return undo(fmt.Errorf("the worker needs a per-user service manager: %w", err))
	}
	if err != nil {
		l.cfg.Logf("launch: worker service start: %v", err)
	}
	if login {
		if _, err := l.workerAction(ctx, l.t.Command, false, "worker", "service", "enable", "--exe", cli, "--json"); err != nil {
			l.cfg.Logf("launch: worker login startup: %v", err)
		}
	}
	l.refreshWorker(ctx)
	return nil
}

// LeaveWorker stops this machine's worker (`worker service uninstall`)
// and leaves its main server (`worker leave`, which removes the machine
// there; local: only deletes the local config, for a main server that is
// unreachable or already removed it). Login startup moves back to the
// server service; with this machine's target active its server starts.
func (l *Launcher) LeaveWorker(ctx context.Context, local bool) error {
	release, err := l.workerOp()
	if err != nil {
		return err
	}
	defer release()
	s := l.workerStatus(ctx)
	if s == nil || s.Worker == nil || !s.Worker.Joined {
		l.setWorker(nil)
		return nil
	}
	login := s.Definition.LoginStartup
	if _, err := l.workerAction(ctx, l.t.Restart, false, "worker", "service", "uninstall", "--wait", "30s", "--json"); err != nil {
		var ce *CommandError
		if !errors.As(err, &ce) || ce.Code != codeUnavailable {
			l.refreshWorker(ctx)
			return fmt.Errorf("the worker could not be stopped: %w", err)
		}
	}
	args := []string{"worker", "leave", "--json"}
	if local {
		args = []string{"worker", "leave", "--local", "--json"}
	}
	if _, err := l.workerAction(ctx, l.t.Command, false, args...); err != nil {
		l.refreshWorker(ctx)
		return fmt.Errorf("leave: %w", err)
	}
	if login {
		if _, err := l.workerAction(ctx, l.t.Command, false, "service", "enable", "--exe", l.cfg.CLI, "--json"); err != nil {
			l.cfg.Logf("launch: server login startup: %v", err)
		}
	}
	l.refreshWorker(ctx)
	return nil
}

// RestartWorker restarts the worker service onto the bundled CLI (`worker
// service restart --exe <cli>`), e.g. after an update. A failure shows in
// the worker's state.
func (l *Launcher) RestartWorker(ctx context.Context) error {
	release, err := l.workerOp()
	if err != nil {
		return err
	}
	defer release()
	if l.refreshWorker(ctx) == nil {
		return errors.New("this machine is not a worker")
	}
	if _, err := l.workerAction(ctx, l.t.Restart, true, "worker", "service", "restart", "--exe", l.cfg.CLI, "--wait", "30s", "--json"); err != nil {
		l.cfg.Logf("launch: worker service restart: %v", err)
	}
	l.refreshWorker(ctx)
	return nil
}
