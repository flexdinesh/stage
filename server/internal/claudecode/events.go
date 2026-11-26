package claudecode

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// settleDelays are the waits before each read after a hook. Claude may
// post a hook a moment before its session list shows the change; reading
// again until it does, then pushing what was read, keeps the list and
// transcript the only source of state.
var settleDelays = []time.Duration{0, 250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second}

// maxSettling bounds the sessions read at once after hooks.
const maxSettling = 4

// Events pushes an observation of each session a hook names, read from the
// session list and transcript (ADR 0005 decision 16). Without hooks it
// returns at once. It returns when ctx ends or the setup replaced this
// adapter (the subscription ended); the coordinator calls it again.
// Sessions Stage does not own are the coordinator's to ignore.
func (a *Adapter) Events(ctx context.Context, sink execution.Sink) error {
	if a.hooks == nil {
		return nil
	}
	sub := a.hooks.Subscribe()
	defer a.hooks.Unsubscribe(sub)
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()
	var mu sync.Mutex
	latest := map[string]Nudge{} // per session, the newest nudge not yet read
	running := map[string]bool{}
	slots := make(chan struct{}, maxSettling)
	// next takes id's newest nudge, or ends its run when none is left.
	next := func(id string) (Nudge, bool) {
		mu.Lock()
		defer mu.Unlock()
		n, ok := latest[id]
		delete(latest, id)
		if !ok {
			delete(running, id)
		}
		return n, ok
	}
	run := func(id string) {
		defer wg.Done()
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		defer func() { <-slots }()
		for n, ok := next(id); ok && ctx.Err() == nil; n, ok = next(id) {
			o, ok := a.settle(ctx, n)
			if !ok {
				continue
			}
			if err := sink(ctx, o); err != nil && ctx.Err() == nil {
				a.log.Warn("claude hook: ingest", "session", id, "err", err)
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-sub.Done():
			return nil
		case n := <-sub.Nudges():
			mu.Lock()
			latest[n.Session] = n
			start := !running[n.Session]
			running[n.Session] = true
			mu.Unlock()
			if start {
				wg.Add(1)
				go run(n.Session)
			}
		}
	}
}

// settle reads n's session until it shows the change n's event announces,
// at most len(settleDelays) times, and returns the last observation read.
// A session that is gone or unreadable throughout yields none.
func (a *Adapter) settle(ctx context.Context, n Nudge) (execution.Observation, bool) {
	var last execution.Observation
	have := false
	since := n.At
	for i, d := range a.settleDelays {
		if i > 0 {
			select {
			case <-ctx.Done():
				return last, have
			case <-time.After(d):
			}
			since = a.now()
		}
		es, err := a.roster.listSince(ctx, since)
		if err != nil {
			continue
		}
		s, err := a.readListed(ctx, n.Session, es)
		if errors.Is(err, execution.ErrNotFound) {
			return execution.Observation{}, false
		}
		if err != nil {
			continue
		}
		o, err := a.observation(s)
		if err != nil {
			continue
		}
		last, have = o, true
		if settled(n, o.State) {
			break
		}
	}
	return last, have
}

// settled reports whether state shows the change n's event announces. An
// event that announces nothing in particular is settled by one read.
func settled(n Nudge, state string) bool {
	switch n.Event {
	case "Stop", "StopFailure", "SessionEnd":
		return state != "running"
	case "PermissionRequest":
		return state == "needs_input"
	case "Notification":
		switch n.NotificationType {
		case "permission_prompt", "agent_needs_input", "elicitation_dialog":
			return state == "needs_input"
		case "idle_prompt":
			return state != "running"
		}
	case "UserPromptSubmit":
		return state == "running"
	}
	return true
}
