package agenttools

import (
	"context"
	"errors"

	"github.com/flexdinesh/stage/server/internal/db"
)

// harnesses is the server's connection list: one connection per harness
// instance, at most one active per harness.
type harnesses []db.HarnessConnection

func (o *Ops) harnesses(ctx context.Context) (harnesses, error) {
	cs, err := o.Client.Connections(ctx)
	return harnesses(cs), err
}

// t3 is the active T3 connection (0 when none).
func (h harnesses) t3() int64 {
	for _, c := range h {
		if c.Harness == db.HarnessT3 && c.Active {
			return c.ID
		}
	}
	return 0
}

// isT3 reports whether a connection, active or replaced, is a T3 instance.
func (h harnesses) isT3(id int64) bool {
	for _, c := range h {
		if c.ID == id {
			return c.Harness == db.HarnessT3
		}
	}
	return false
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

// t3Lookup resolves the active T3 connection for legacy thread ids, once.
func (o *Ops) t3Lookup(ctx context.Context) func() (int64, error) {
	var id int64
	return func() (int64, error) {
		if id != 0 {
			return id, nil
		}
		h, err := o.harnesses(ctx)
		if err != nil {
			return 0, err
		}
		if id = h.t3(); id == 0 {
			return 0, errors.New("no T3 connection on this Stage server; pass callerSession")
		}
		return id, nil
	}
}
