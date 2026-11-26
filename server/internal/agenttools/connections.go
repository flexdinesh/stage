package agenttools

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// harnesses is the server's connection list: one connection per harness
// instance, at most one active per harness.
type harnesses []db.HarnessConnection

func (o *Ops) harnesses(ctx context.Context) (harnesses, error) {
	cs, err := o.Client.Connections(ctx)
	return harnesses(cs), err
}

// get is the connection with id (nil when unknown).
func (h harnesses) get(id int64) *db.HarnessConnection {
	for i := range h {
		if h[i].ID == id {
			return &h[i]
		}
	}
	return nil
}

// machine of a connection (0 when unknown).
func (h harnesses) machine(id int64) int64 {
	if c := h.get(id); c != nil {
		return c.MachineID
	}
	return 0
}

// newTaskConfig is the model and options pinned for new tasks on a
// connection (nil: the harness default).
func (h harnesses) newTaskConfig(id int64) *execution.Config {
	if c := h.get(id); c != nil {
		return c.NewTaskConfig
	}
	return nil
}

func (h harnesses) active() []db.HarnessConnection {
	var out []db.HarnessConnection
	for _, c := range h {
		if c.Active {
			out = append(out, c)
		}
	}
	return out
}
