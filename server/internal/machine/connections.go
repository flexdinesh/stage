package machine

import (
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// Connections resolves active connections through their machine and harness policy.
type Connections struct {
	store     *db.Store
	set       *Set
	harnesses *harness.Registry
}

func NewConnections(store *db.Store, set *Set, reg *harness.Registry) *Connections {
	return &Connections{store: store, set: set, harnesses: reg}
}

func (c *Connections) Machines() *Set { return c.set }

func (c *Connections) Harnesses() *harness.Registry { return c.harnesses }

// Descriptor is kind's registered descriptor.
func (c *Connections) Descriptor(kind string) (harness.Descriptor, bool) {
	if c.harnesses == nil {
		return harness.Descriptor{}, false
	}
	return c.harnesses.Lookup(kind)
}

// Ops is the fs and process work of machine id (false when no host serves
// it).
func (c *Connections) Ops(id int64) (Ops, bool) {
	m, ok := c.set.Get(id)
	if !ok {
		return nil, false
	}
	return m.Ops(), true
}

func (c *Connections) All() map[int64]execution.Adapter {
	return c.set.Adapters()
}

func (c *Connections) InProcess(machineID int64) bool {
	if machineID == c.set.DefaultID() {
		return true
	}
	m, _ := c.set.Get(machineID)
	_, local := m.(*Local)
	return local
}

func (c *Connections) Adapter(id int64) (execution.Adapter, error) {
	a, ok := c.set.Adapter(id)
	kind := c.store.Harness(id)
	// A replaced instance keeps its history but is never reached again.
	if !ok && kind != "" && !c.store.ConnectionActive(id) {
		return nil, db.ConnectionRetired()
	}
	// Its machine does not allow the kind (a hosted harness on a profile
	// without local processes): say why. Checked before an installed
	// adapter too, so a worker restarted without local harnesses names the
	// fix instead of answering that it hosts no adapter.
	if d, found := c.Descriptor(kind); found {
		if m, found := c.set.Get(c.store.ConnectionMachine(id)); found {
			if av := m.Allows(d); !av.Available {
				return nil, &db.ConflictError{Code: "connection_unavailable", Msg: av.Reason}
			}
		}
	}
	if !ok {
		return nil, &db.ConflictError{Code: "connection_unavailable", Msg: "This harness is unavailable. Select a supported connection."}
	}
	return a, nil
}

func (c *Connections) Catalog(id int64) (execution.Catalog, error) {
	return c.Adapter(id)
}

func (c *Connections) Inventory(id int64) (execution.Inventory, error) {
	return c.Adapter(id)
}

func (c *Connections) Sessions(id int64) (execution.Sessions, error) {
	return c.Adapter(id)
}

func (c *Connections) Launcher(id int64) (execution.Launcher, error) {
	return c.Adapter(id)
}

// Online reports whether the machine currently serves its connections.
func (c *Connections) Online(machineID int64) bool {
	m, ok := c.set.Get(machineID)
	return ok && m.Online()
}
