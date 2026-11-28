package codex

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Events pushes an observation of each thread whose status changes. The
// daemon broadcasts thread/status/changed and thread/archived to every
// client, subscribed or not, so Stage hears about its threads without
// holding their event streams. Unowned threads are the coordinator's to
// ignore. Returns when ctx ends or the connection drops.
func (a *Adapter) Events(ctx context.Context, sink execution.Sink) error {
	queue := make(chan string, 256)
	stop := a.client.listen(func(n notification) { queueObservation(queue, n) })
	defer stop()
	// Connect (and start the daemon) so notifications flow.
	if err := a.client.call(ctx, "thread/loaded/list", map[string]any{"limit": 1}, nil); err != nil {
		return err
	}
	done := a.client.done()
	if done == nil {
		return errors.New("codex connection dropped")
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			return errors.New("codex connection dropped")
		case id := <-queue:
			o, err := a.Observe(ctx, a.ref(id))
			if err != nil {
				continue
			}
			if err := sink(ctx, o); err != nil && ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}
}

func queueObservation(queue chan<- string, n notification) {
	switch n.Method {
	case "thread/status/changed", "thread/archived", "thread/unarchived", "turn/completed":
	default:
		return
	}
	var p struct {
		ThreadID string `json:"threadId"`
	}
	if json.Unmarshal(n.Params, &p) != nil || !threadID.MatchString(p.ThreadID) {
		return
	}
	select {
	case queue <- p.ThreadID:
	default: // polling reconciles a dropped event
	}
}
