package serve

import (
	"context"
	"log/slog"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// movedRetirer retires a connection whose instance moved to another machine:
// an active connection whose stable identity is held by another machine
// and whose setup confirms the move according to its module policy. It retires
// onto that one, so pickers stop offering it and its default, backlog and
// schedules follow (db.Store.RetireConnection). It runs at startup, every
// sync interval and when kicked (a pairing, a worker joining, a manual sync).
type movedRetirer struct {
	store *db.Store
	log   *slog.Logger
	every time.Duration
	kick  chan struct{}
	// connectors are the live connectors of every machine.
	connectors func() harness.Connectors
	// reachable: the machine's setup answers now (the local machine, or an
	// online worker). A disconnected-looking connection on an unreachable
	// machine is never retired: its state is unknown.
	reachable func(machine int64) bool
	// onRetired follows a retirement on machine (adapters, runners and sync
	// resync).
	onRetired func(machine, old, next int64)
}

func newMovedRetirer(store *db.Store, every time.Duration, log *slog.Logger) *movedRetirer {
	return &movedRetirer{store: store, log: log, every: every, kick: make(chan struct{}, 1)}
}

// Kick asks for a pass soon.
func (m *movedRetirer) Kick() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// Run passes at once, then every interval and on each kick, until ctx ends.
func (m *movedRetirer) Run(ctx context.Context) {
	t := time.NewTicker(m.every)
	defer t.Stop()
	for {
		if err := m.Once(ctx); err != nil && ctx.Err() == nil {
			m.log.Warn("retire moved connections", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-m.kick:
		}
	}
}

// Once retires every moved connection it can confirm now.
func (m *movedRetirer) Once(ctx context.Context) error {
	moves, err := m.store.MovedConnections(ctx)
	if err != nil || len(moves) == 0 {
		return err
	}
	cs := m.connectors()
	retired := map[int64]bool{}
	for _, mv := range moves {
		if retired[mv.Old] || retired[mv.New] || !m.reachable(m.store.ConnectionMachine(mv.Old)) {
			continue
		}
		d, old, ok := cs.Owner(ctx, mv.Old)
		if !ok || d.MovedRetire == nil {
			continue
		}
		_, next, ok := cs.Owner(ctx, mv.New)
		if !ok || !d.MovedRetire(old.Summary(ctx), next.Summary(ctx)) {
			continue
		}
		machine := m.store.ConnectionMachine(mv.Old)
		if err := m.store.RetireConnection(ctx, mv.Old, mv.New); err != nil {
			return err
		}
		retired[mv.Old] = true
		m.log.Info("retired moved connection", "connection", mv.Old, "replacement", mv.New)
		if m.onRetired != nil {
			m.onRetired(machine, mv.Old, mv.New)
		}
	}
	return nil
}
