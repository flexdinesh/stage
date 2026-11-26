package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"sync"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// Connections lists a machine's active harness connections as the main
// server records them (db.Store.ActiveConnections).
type Connections func(machineID int64) []int64

// Machine is a remote machine as the main server hosts it: a proxy adapter
// for each of its active connections (the main server's record, so an
// offline machine's connections still answer, as offline) and its ops,
// over the machine's worker channel, and a proxy of each connection setup
// its worker runs (Connector).
type Machine struct {
	id    int64
	hub   *workerchan.Hub
	conns Connections
	// hosts is the Hosts that hosts the machine (nil: no connection setup).
	hosts *Hosts

	mu       sync.Mutex
	snapshot *workerchan.Ready
	desc     description
	adapters map[int64]*Adapter
	// sums are connector summaries from the latest snapshot or status call.
	sums       map[string]harness.Summary
	connectors map[string]*Connector
}

var _ machine.Machine = (*Machine)(nil)

// NewMachine hosts machine id over hub.
func NewMachine(id int64, hub *workerchan.Hub, conns Connections) *Machine {
	return &Machine{id: id, hub: hub, conns: conns, adapters: map[int64]*Adapter{}, sums: map[string]harness.Summary{}, connectors: map[string]*Connector{}}
}

func (m *Machine) ID() int64 { return m.id }

// Online: its worker's channel is live.
func (m *Machine) Online() bool { return m.hub.Presence(m.id).Online }

// offline is why the machine cannot be reached now (false when online).
func (m *Machine) offline() (string, bool) {
	p := m.hub.Presence(m.id)
	switch {
	case p.Online:
		return "", false
	case p.NeedsUpdate != nil:
		return p.NeedsUpdate.Reason, true
	case p.Negotiating:
		return workerchan.ErrNegotiating.Error(), true
	}
	return "Its machine is offline: the Stage worker there is not connected.", true
}

// refreshSnapshot updates the cached connector summaries when a new ready
// snapshot arrives. It retains the last-known summaries while disconnected.
// Call with m.mu held.
func (m *Machine) refreshSnapshot() *workerchan.Ready {
	if m.hub == nil {
		return nil
	}
	snapshot, _ := m.hub.Advertisement(m.id)
	if snapshot != nil && snapshot != m.snapshot {
		m.snapshot = snapshot
		m.desc = description{}
		_ = json.Unmarshal(snapshot.Details, &m.desc)
		clear(m.sums)
		for _, c := range m.desc.Connectors {
			m.sums[c.Kind] = c.Summary
		}
	}
	return snapshot
}

// Profile is the last worker profile, or the foreground server default.
func (m *Machine) Profile() harness.Profile {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshSnapshot()
	if m.desc.Profile.Kind == "" {
		return harness.Profile{Kind: harness.ProfileServer}
	}
	return m.desc.Profile
}

// Allows combines placement policy with the current worker's declared kinds.
func (m *Machine) Allows(d harness.Descriptor) execution.Availability {
	m.mu.Lock()
	snapshot := m.refreshSnapshot()
	profile := m.desc.Profile
	m.mu.Unlock()
	if snapshot == nil {
		if m.hub != nil && m.hub.Presence(m.id).Negotiating {
			return execution.Availability{Reason: workerchan.ErrNegotiating.Error()}
		}
		return execution.Availability{Available: true}
	}
	if !profile.Allows(d).Available {
		if profile.Kind == harness.ProfileContainer {
			return execution.Availability{Reason: d.Name + " can't run in a container: run this machine's Stage worker outside Docker."}
		}
		return execution.Availability{Reason: d.Name + " runs on this machine only when its Stage worker allows local harnesses: use the worker service, or run stage worker run --local-harnesses."}
	}
	if !slices.Contains(snapshot.Kinds, d.Kind) {
		return execution.Availability{Reason: d.Name + " can't run on this machine: its Stage worker doesn't set it up. Update Stage on that machine."}
	}
	return execution.Availability{Available: true}
}

func (m *Machine) active(connection int64) bool {
	return slices.Contains(m.conns(m.id), connection)
}

func (m *Machine) adapter(connection int64) *Adapter {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := m.adapters[connection]
	if a == nil {
		a = &Adapter{m: m, conn: connection}
		m.adapters[connection] = a
	}
	return a
}

func (m *Machine) Adapter(connection int64) (execution.Adapter, bool) {
	if !m.active(connection) {
		return nil, false
	}
	return m.adapter(connection), true
}

func (m *Machine) Adapters() map[int64]execution.Adapter {
	out := map[int64]execution.Adapter{}
	for _, id := range m.conns(m.id) {
		out[id] = m.adapter(id)
	}
	return out
}

// kinds are the harness kinds the machine's worker sets up (Allows
// decides which of them it may host).
func (m *Machine) kinds() []string {
	if m.hosts == nil {
		return nil
	}
	return m.hosts.setup.Kinds
}

// Connector is the proxy of kind's connection setup on the worker; it
// exists offline too (its calls then answer as unavailable).
func (m *Machine) Connector(kind string) (harness.Connector, bool) {
	if !slices.Contains(m.kinds(), kind) {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.connectors[kind]
	if c == nil {
		c = &Connector{m: m, kind: kind}
		m.connectors[kind] = c
	}
	return c, true
}

func (m *Machine) Connectors() map[string]harness.Connector {
	out := map[string]harness.Connector{}
	for _, kind := range m.kinds() {
		if c, ok := m.Connector(kind); ok {
			out[kind] = c
		}
	}
	return out
}

func (m *Machine) Ops() machine.Ops { return ops{m} }

// traits are proven only by the current ready snapshot.
func (m *Machine) traits(connection int64) []string {
	snapshot, _ := m.hub.Advertisement(m.id)
	if snapshot != nil {
		for _, c := range snapshot.Connections {
			if c.ConnectionID == connection {
				return c.Traits
			}
		}
	}
	return nil
}

// ops runs machine.Ops on the worker.
type ops struct{ m *Machine }

func (o ops) Inventory(ctx context.Context, ws []execution.WorkspaceInfo, roots []machine.SharedRoot) ([]execution.WorkspaceInfo, error) {
	var out []execution.WorkspaceInfo
	err := o.call(ctx, KindInventory, InventoryRequest{Workspaces: ws, Roots: roots}, &out)
	return out, err
}

func (o ops) Checkout(ctx context.Context, roots []machine.SharedRoot, dir string) (machine.CheckoutEvidence, error) {
	var e machine.CheckoutEvidence
	err := o.call(ctx, KindCheckout, CheckoutRequest{Roots: roots, Directory: dir}, &e)
	return e, err
}

func (o ops) call(ctx context.Context, kind string, body, out any) error {
	raw, err := o.m.hub.Call(ctx, o.m.id, kind, 0, body, CallTimeout)
	if err = decodeError(err); err != nil {
		return err
	}
	return jsonUnmarshal(raw, out)
}

// Hosts keeps a machine.Set's remote machines in step with the main
// server's record: a machine that joins is hosted, a removed one is not,
// and worker ready snapshots trigger a refresh.
type Hosts struct {
	set   *machine.Set
	hub   *workerchan.Hub
	conns Connections
	log   *slog.Logger
	// changed runs after the hosted machines, or what a worker hosts,
	// changed (the coordinator and runners follow the connections).
	changed func()
	// setup connects remote connectors to the main server (SetSetup).
	setup SetupHooks
}

// SetSetup gives hosted machines connection setup for hooks.Kinds. Call it
// before the first Join.
func (h *Hosts) SetSetup(hooks SetupHooks) {
	h.setup = hooks
	h.hub.SetHarnessKinds(hooks.Kinds)
}

// NewHosts hosts remote machines in set and follows their ready snapshots.
func NewHosts(set *machine.Set, hub *workerchan.Hub, conns Connections, changed func(), log *slog.Logger) *Hosts {
	if log == nil {
		log = slog.Default()
	}
	if changed == nil {
		changed = func() {}
	}
	h := &Hosts{set: set, hub: hub, conns: conns, log: log, changed: changed}
	hub.SetCallbackKinds([]string{KindCheckpointSave, KindLaunchImage, KindObservation})
	hub.OnReady(func(id int64) {
		if _, ok := h.Machine(id); ok {
			h.changed()
		}
	})
	return h
}

// Join hosts remote machine id (no-op when hosted).
func (h *Hosts) Join(id int64) {
	if _, ok := h.set.Get(id); ok {
		return
	}
	m := NewMachine(id, h.hub, h.conns)
	m.hosts = h
	if err := h.set.Add(m); err != nil {
		h.log.Warn("host remote machine", "machine", id, "err", err)
		return
	}
	h.changed()
}

// Leave stops hosting remote machine id (it was removed).
func (h *Hosts) Leave(id int64) {
	if h.set.Remove(id) {
		h.changed()
	}
}

// Machine is hosted remote machine id.
func (h *Hosts) Machine(id int64) (*Machine, bool) {
	m, ok := h.set.Get(id)
	if !ok {
		return nil, false
	}
	r, ok := m.(*Machine)
	return r, ok
}

// jsonUnmarshal decodes a worker reply.
func jsonUnmarshal(raw []byte, out any) error {
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("malformed worker reply: %w", err)
	}
	return nil
}
