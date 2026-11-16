package launch

import (
	"context"
	"fmt"
	"time"
)

// monitorState is what the monitor knows of the server it watches.
type monitorState struct {
	sock       string
	info       buildInfo
	instance   string
	managed    bool
	pid        *int
	foreground bool
}

// monitor checks health every HealthEvery. When the server stops
// answering it reconnects with backoff; after ReconnectWindow it asks the
// service what happened (stopped, failed, ...) and keeps polling slowly,
// so a server that comes back by itself is picked up. A new instance on
// the same socket (a restart between checks) counts as a reconnect, so
// the app reopens its streams.
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
		h, err := l.health(ctx, m.sock, l.t.HealthTimeout)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			if !downSince.IsZero() || h.InstanceID != m.instance {
				m.instance = h.InstanceID
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
			l.cfg.Logf("launch: server unreachable on %s: %v", m.sock, err)
			l.update(func(s *State) {
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
		if l.devMode() {
			l.settle(outcome{phase: PhaseStopped, title: "Dev server not reachable",
				message: fmt.Sprintf("No Stage server answers on %s.", m.sock), fix: "Start it with mise run desktop:dev."})
			continue
		}
		st, code, _ := l.status(ctx)
		if ctx.Err() != nil {
			return
		}
		switch {
		case st == nil:
			l.settle(outcome{phase: PhaseFailed, title: "Stage server not reachable",
				message: fmt.Sprintf("No answer on %s for %s, and the service status is unavailable.", m.sock, l.t.ReconnectWindow),
				fix:     "Start the server again, or copy the diagnostics."})
		case code == codeOK || code == codeTimeout:
			// The service is (re)starting or ready: keep reconnecting,
			// following its socket.
			if st.Socket.Path != "" {
				m.sock = st.Socket.Path
			}
			downSince, settled = now, false
		default:
			o, fg := l.statusOutcome(ctx, st, code)
			if fg {
				o = outcome{phase: PhaseConflict, title: "Another Stage server holds your data",
					message: "A Stage server started from a terminal holds the data directory.",
					fix:     "Stop that server, then Retry.", problems: st.problems()}
			}
			l.settle(o)
		}
	}
}

// recovered settles ready (epoch++) or incompatible after the server
// answers again; false when the monitor should stop.
func (l *Launcher) recovered(m monitorState, h Health) bool {
	if !l.op.TryLock() {
		return true // an action runs; it settles the state itself
	}
	defer l.op.Unlock()
	l.cfg.Logf("launch: server back on %s (instance %s)", m.sock, h.InstanceID)
	l.ready(m.info, m.sock, h, m.managed, m.pid, m.foreground)
	return false
}

// ensureDev connects a development build to the foreground dev server on
// DevSocket, without any service command. The dev server is the truth for
// identity and API: the bundled CLI (if `mygo dev` shipped one from
// resources/) may be another installation's.
func (l *Launcher) ensureDev(ctx context.Context) {
	sock := l.cfg.DevSocket
	l.progress(StepAPI)
	h, err := l.waitHealth(ctx, sock, l.t.StartPoll)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		l.settle(outcome{phase: PhaseStopped, title: "Dev server not reachable",
			message: fmt.Sprintf("No Stage server answers on %s: %v", sock, err), fix: "Start it with mise run desktop:dev."})
		l.startMonitor(sock, buildInfo{}, Health{}, false, nil, true)
		return
	}
	l.ready(buildInfo{}, sock, h, false, nil, true)
}
