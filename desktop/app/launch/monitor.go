package launch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/flexdinesh/stage/desktop/app/target"
)

// monitorState is what the monitor knows of the server it watches.
type monitorState struct {
	gen        uint64 // the switch generation it belongs to
	t          target.Target
	info       buildInfo
	identity   string
	shared     bool
	managed    bool
	pid        *int
	foreground bool
}

// monitor checks health every HealthEvery. When the server stops
// answering it reconnects with backoff; after ReconnectWindow it asks the
// service what happened (stopped, failed, ...) and keeps polling slowly,
// so a server that comes back by itself is picked up. A new instance on
// the same socket (a restart between checks) counts as a reconnect, so
// the app reopens its streams. A remote is never asked: after the window
// it is unreachable, and polling continues.
func (l *Launcher) monitor(ctx context.Context, m monitorState) {
	var downSince time.Time
	settled := false
	backoff := l.t.BackoffMin
	for {
		wait := l.t.HealthEvery
		if !downSince.IsZero() && !settled {
			wait = backoff
			backoff = min(backoff*2, l.t.BackoffMax)
		}
		if !sleep(ctx, wait) {
			return
		}
		h, err := l.health(ctx, m.t, l.t.HealthTimeout)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			if h.SharedAccess && l.cfg.CheckSession != nil {
				_ = l.cfg.CheckSession(ctx, m.t)
			}
			identity := healthIdentity(h)
			if !downSince.IsZero() || (identity != "" && identity != m.identity) || h.SharedAccess != m.shared {
				m.identity = identity
				m.shared = h.SharedAccess
				downSince, settled, backoff = time.Time{}, false, l.t.BackoffMin
				if !l.recovered(m, h) {
					return // a new monitor took over
				}
			}
			continue
		}
		now := l.cfg.Now()
		if downSince.IsZero() {
			downSince = now
			l.cfg.Logf("launch: server unreachable on %s: %v", where(m.t), err)
			l.apply(m.gen, func(s *State) {
				if s.Phase == PhaseReady {
					s.Phase, s.Title = PhaseReconnecting, "Reconnecting to Stage server"
					s.Message = "The Stage server stopped answering."
				}
			})
			continue
		}
		if settled || now.Sub(downSince) < l.t.ReconnectWindow {
			continue
		}
		settled = true
		if m.t.Remote() {
			l.settleGen(m.gen, unreachable(m.t, err))
			continue
		}
		if l.devMode() {
			l.settleGen(m.gen, outcome{phase: PhaseStopped, title: "Dev server not reachable",
				message: fmt.Sprintf("No Stage server answers on %s.", m.t.Socket), fix: "Start it with mise run desktop:dev."})
			continue
		}
		st, code, _ := l.status(ctx)
		if ctx.Err() != nil {
			return
		}
		switch {
		case st == nil:
			l.settleGen(m.gen, outcome{phase: PhaseFailed, title: "Stage server not reachable",
				message: fmt.Sprintf("No answer on %s for %s, and the service status is unavailable.", m.t.Socket, l.t.ReconnectWindow),
				fix:     "Start the server again, or copy the diagnostics."})
		case code == codeOK || code == codeTimeout:
			// The service is (re)starting or ready: keep reconnecting,
			// following its socket.
			if st.Socket.Path != "" {
				m.t.Socket = st.Socket.Path
			}
			downSince, settled = now, false
		default:
			o, fg := l.statusOutcome(ctx, st, code)
			if fg {
				o = outcome{phase: PhaseConflict, title: "Another Stage server holds your data",
					message: "A Stage server started from a terminal holds the data directory.",
					fix:     "Stop that server, then Retry.", problems: st.problems()}
			}
			l.settleGen(m.gen, o)
		}
	}
}

func healthIdentity(h Health) string {
	if h.SharedAccess {
		return h.installation
	}
	return h.InstanceID
}

// recovered settles ready (epoch++) or incompatible after the server
// answers again; false when the monitor should stop.
func (l *Launcher) recovered(m monitorState, h Health) bool {
	if !l.op.TryLock() {
		return true // an action runs; it settles the state itself
	}
	defer l.op.Unlock()
	l.mu.Lock()
	if l.gen != m.gen {
		l.mu.Unlock()
		return false // switched away
	}
	l.opGen = m.gen
	l.mu.Unlock()
	l.cfg.Logf("launch: server back on %s (instance %s)", where(m.t), h.InstanceID)
	l.ready(m.info, m.t, h, m.managed, m.pid, m.foreground)
	return false
}

// ensureRemote connects to a remote server: no service and no CLI command
// but `version --json` (the API this app needs). Unreachable keeps
// polling, so a server that comes up is picked up.
func (l *Launcher) ensureRemote(ctx context.Context, t target.Target) {
	l.progress(StepServer)
	info, o := l.readVersion(ctx)
	if o != nil {
		if ctx.Err() != nil {
			return
		}
		l.cfg.Logf("launch: %s (%s); connecting to %s without an API check", o.title, o.message, t.URL)
		info = buildInfo{}
	}
	h, err := l.waitHealth(ctx, t, l.t.Ready)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		l.settle(unreachable(t, err))
		l.startMonitor(l.opGeneration(), t, info, Health{}, false, nil, false)
		return
	}
	l.progress(StepAPI)
	l.ready(info, t, h, false, nil, false)
}

// unreachable is the outcome of a remote that does not answer; a refusal
// by its origin policy (403, a hostname it does not know) names the
// setting to add there.
func unreachable(t target.Target, err error) outcome {
	o := outcome{phase: PhaseUnreachable, title: "Can't reach " + t.Name,
		message: fmt.Sprintf("No Stage server answers at %s: %v", t.URL, err),
		fix:     "Check that Stage runs on that machine and listens on an address this machine can reach (stage serve --listen 0.0.0.0:7380), then Retry."}
	var he *HTTPError
	if errors.As(err, &he) && he.Status == 403 {
		o.title = t.Name + " refused the connection"
		o.message = fmt.Sprintf("The Stage server at %s refused this app: %s", t.URL, he.Message)
		o.fix = "On that server, allow this address, restart it, then Retry:"
		o.command = "STAGE_ALLOWED_ORIGINS=" + t.URL
	}
	return o
}

// ensureDev connects a development build to the foreground dev server on
// DevSocket, without any service command. The dev server is the truth for
// identity and API: the bundled CLI (if `mygo dev` shipped one from
// resources/) may be another installation's.
func (l *Launcher) ensureDev(ctx context.Context) {
	sock := l.cfg.DevSocket
	l.progress(StepAPI)
	h, err := l.waitHealth(ctx, target.Local(sock), l.t.StartPoll)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		l.settle(outcome{phase: PhaseStopped, title: "Dev server not reachable",
			message: fmt.Sprintf("No Stage server answers on %s: %v", sock, err), fix: "Start it with mise run desktop:dev."})
		l.startMonitor(l.opGeneration(), target.Local(sock), buildInfo{}, Health{}, false, nil, true)
		return
	}
	l.ready(buildInfo{}, target.Local(sock), h, false, nil, true)
}
