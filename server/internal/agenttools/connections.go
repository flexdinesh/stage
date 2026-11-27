package agenttools

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/db"
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
