package claudecode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Follow-ups (ADR 0005 decision 18, E12). `claude --bg --resume <uuid>`
// continues a session only when its process has exited: on a live one, even
// idle, it starts a copy. So Send stops a live idle session first, then
// wakes it with the message and no flags (flags would also start a copy),
// keeping its saved options and Stage's hooks.

// Busy reasons.
const (
	busyWorking     = "Claude Code is working. Send once it finishes."
	busyWaiting     = "Claude Code is waiting on a permission prompt or question. Answer it first."
	busyInteractive = "This Claude session runs in a terminal or another app; send it there."
	busyCopy        = "Claude Code was busy and started a copy; Stage removed the copy. Send again once it finishes."
)

// wakeWindow bounds how long a Send's stopped session reads running.
const wakeWindow = 15 * time.Second

var copied = regexp.MustCompile(`started a copy as ([0-9a-f]{8})`)

// isWaking reports whether a Send is delivering to session id.
func (a *Adapter) isWaking(id string) bool {
	a.wakeMu.Lock()
	defer a.wakeMu.Unlock()
	until, ok := a.waking[id]
	return ok && a.now().Before(until)
}

// wake marks session id as being delivered to until the returned func runs.
func (a *Adapter) wake(id string) func() {
	a.wakeMu.Lock()
	if a.waking == nil {
		a.waking = map[string]time.Time{}
	}
	a.waking[id] = a.now().Add(wakeWindow)
	a.wakeMu.Unlock()
	return func() {
		a.wakeMu.Lock()
		delete(a.waking, id)
		a.wakeMu.Unlock()
	}
}

// sendable is nil when s takes a message now: a listed background session
// that is done, working but idle after its turn (idleAfterTurn), or blocked
// on a finished turn (it ended asking its user),
// in a folder that still exists. Else *execution.Busy with what to do.
func (a *Adapter) sendable(s session) error {
	resume := a.links(s).Command
	switch {
	case !s.listed:
		return &execution.Busy{Reason: "Claude Code no longer lists this session. Resume it in a terminal: " + resume}
	case !s.entry.Background():
		return &execution.Busy{Reason: busyInteractive}
	}
	switch s.entry.State {
	case "done":
	case "blocked":
		// A permission prompt or question keeps the turn open; a reaped
		// session no longer says which, the transcript does.
		if s.entry.Status == "waiting" || s.entry.WaitingFor != "" || !s.hasLog || !s.transcript.TurnOver {
			return &execution.Busy{Reason: busyWaiting}
		}
	case "working":
		if !s.idleAfterTurn() {
			return &execution.Busy{Reason: busyWorking}
		}
	default:
		return &execution.Busy{Reason: "Claude Code doesn't run this session now. Resume it in a terminal: " + resume}
	}
	if fi, err := os.Stat(s.cwd()); err != nil || !fi.IsDir() {
		return &execution.Busy{Reason: fmt.Sprintf("The session's folder %s is gone.", s.cwd())}
	}
	return nil
}

// Send delivers a follow-up: on a fresh session list it checks the session
// takes one (sendable), stops a live process (`claude stop`), then runs
// `claude --bg --resume <uuid> -- <text>` in the session's folder and waits
// for the session to run. A copy (the session got busy meanwhile) is
// removed and reported Busy.
func (a *Adapter) Send(ctx context.Context, ref execution.SessionRef, m execution.Message) error {
	if ref.ConnectionID != a.conn {
		return execution.ErrNotFound
	}
	a.sendMu.Lock()
	defer a.sendMu.Unlock()
	s, err := a.read(ctx, ref.ExternalSessionID, true)
	if err != nil {
		return err
	}
	if err := a.sendable(s); err != nil {
		return err
	}
	defer a.roster.invalidate()
	defer a.wake(s.id)()
	short := s.entry.ID
	if s.entry.PID > 0 {
		if err := a.stopForWake(ctx, short, s.entry.PID); err != nil {
			return err
		}
	}
	call, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	res, err := a.cli.Launch(call, s.cwd(), "--bg", "--resume", s.id, "--", m.Text)
	if err != nil {
		return fmt.Errorf("claude --bg --resume: %w", err)
	}
	if res.Code != 0 {
		return exitError("--bg --resume", res)
	}
	got, ok := ParseBackgrounded(res.Stdout)
	if c := copied.FindStringSubmatch(res.Stderr + res.Stdout); c != nil || (ok && got != short) {
		if c != nil {
			got = c[1]
		}
		a.removeCopy(ctx, got)
		return &execution.Busy{Reason: busyCopy}
	}
	if !ok {
		return fmt.Errorf("claude --bg --resume printed no session id: %s", excerpt(res.Stdout))
	}
	a.awaitRunning(ctx, short)
	return nil
}

// stopForWake stops session short's live process pid and waits until the
// session list shows it exited and the process is gone: the list drops the
// pid at once, but a resume while the process still ends (about 0.3 s)
// starts a copy (E12).
func (a *Adapter) stopForWake(ctx context.Context, short string, pid int) error {
	call, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	res, err := a.cli.Run(call, "", "stop", short)
	if err != nil {
		return err
	}
	if res.Code != 0 {
		return exitError("stop", res)
	}
	deadline := a.now().Add(a.launchWait)
	for {
		es, err := a.roster.list(ctx, true)
		if err == nil {
			if e, ok := findShort(es, short); (!ok || e.PID == 0) && !a.alive(pid) {
				return nil
			}
		}
		if !a.now().Before(deadline) {
			return fmt.Errorf("claude stop %s: the session's process is still running", short)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// awaitRunning waits (best effort, LaunchWait) until the session list shows
// session short woken: a process again, or working.
func (a *Adapter) awaitRunning(ctx context.Context, short string) {
	deadline := a.now().Add(a.launchWait)
	for {
		es, err := a.roster.list(ctx, true)
		if err == nil {
			if e, ok := findShort(es, short); ok && (e.PID > 0 || e.State == "working") {
				return
			}
		}
		if !a.now().Before(deadline) {
			a.log.Warn("claude: woken session not running yet", "session", short)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// removeCopy stops and removes a copy Claude started instead of waking the
// session (best effort; it keeps its transcript).
func (a *Adapter) removeCopy(ctx context.Context, short string) {
	call, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for _, cmd := range []string{"stop", "rm"} {
		res, err := a.cli.Run(call, "", cmd, short)
		if err == nil && res.Code != 0 {
			err = exitError(cmd, res)
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			a.log.Warn("claude: remove copy", "session", short, "cmd", cmd, "err", err)
		}
	}
}
