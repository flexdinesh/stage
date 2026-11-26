package machine

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// Identity is the main server's record of a machine's harness connections
// as connection setup reaches it. It is harness-neutral: a setup names its
// harness by kind and the main server applies its own registry's policy.
// StorePorts serves it in process; a worker serves it over its channel
// (workersetup.Ports).
type Identity interface {
	// Active is the machine's active connection of kind (0: none yet).
	Active(ctx context.Context, kind string) (int64, error)
	// SwitchInstance makes the instance with identity the machine's active
	// connection of kind; the first one creates the connection row
	// (db.Store.SwitchInstance).
	SwitchInstance(ctx context.Context, kind, identity, label string, revive int64) (db.Switch, error)
}

// Install moves a harness's adapter from connection old to next (equal
// unless its instance was replaced); a nil adapter disconnects next
// (Local.Install, the install port).
type Install func(old, next int64, a execution.Adapter)

var _ Identity = StorePorts{}

// Connect records instance identity as the machine's connection of kind
// and installs the adapter newAdapter builds for it: the first connect
// creates the connection row, the same identity keeps it, and another
// identity replaces it under the replaced-instance rules. Result.Replaced
// is the connection the switch retired. A hosted harness's connect action
// runs it.
func Connect(ctx context.Context, id Identity, install Install, kind, identity, label string, newAdapter func(connection int64) execution.Adapter) (harness.Result, error) {
	sw, err := id.SwitchInstance(ctx, kind, identity, label, 0)
	if err != nil {
		return harness.Result{}, err
	}
	install(sw.Old, sw.New, newAdapter(sw.New))
	var res harness.Result
	if sw.Changed {
		res.Replaced = sw.Old
	}
	return res, nil
}
