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
	desc     *Description
	adapters map[int64]*Adapter
	// sums are the worker's last connector summaries by kind (describe, or
	// a setup call's status).
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
		return "Its machine needs an update: the Stage worker there speaks another API version.", true
	}
	return "Its machine is offline: the Stage worker there is not connected.", true
}

// Profile is the worker's hosting policy as it last described it (a
// foreground server's until then).
func (m *Machine) Profile() harness.Profile {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.desc == nil {
		return harness.Profile{Kind: harness.ProfileServer}
	}
	return m.desc.Profile
}

// Allows: connected harnesses always; a hosted one as the worker's
// profile allows it (harness.Profile.Allows), with the fix in the worker's
// terms, and once its setup is ready only if the worker set it up (an
// older worker sets up no Claude Code or Codex). Until the worker
// described the machine its profile is unknown: allowed, so the calls
// themselves answer offline or still starting.
func (m *Machine) Allows(d harness.Descriptor) execution.Availability {
	m.mu.Lock()
	desc := m.desc
	m.mu.Unlock()
	if desc == nil {
		return execution.Availability{Available: true}
	}
	if desc.Profile.Allows(d).Available {
		if d.Hosting == harness.Hosted && desc.SetupReady && !slices.ContainsFunc(desc.Connectors, func(c ConnectorState) bool { return c.Kind == d.Kind }) {
			return execution.Availability{Reason: d.Name + " can't run on this machine: its Stage worker doesn't set it up. Update Stage on that machine."}
		}
		return execution.Availability{Available: true}
	}
	if desc.Profile.Kind == harness.ProfileContainer {
		return execution.Availability{Reason: d.Name + " can't run in a container: run this machine's Stage worker outside Docker."}
	}
	return execution.Availability{Reason: d.Name + " runs on this machine only when its Stage worker allows local harnesses: use the worker service, or run stage worker run --local-harnesses."}
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

// traits are the optional interfaces the worker's adapter for connection
// implements; known is false until the worker described that connection
// (a connection installed after the last describe is assumed capable, as
// before the first describe: its calls answer for themselves).
func (m *Machine) traits(connection int64) (traits []string, known bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.desc == nil {
		return nil, false
	}
	for _, h := range m.desc.Connections {
		if h.ConnectionID == connection {
			return h.Traits, true
		}
	}
	return nil, false
}

// Described reports whether the worker described the machine since this
// server started.
func (m *Machine) Described() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.desc != nil
}

// Describe asks the worker what it hosts (machine.describe) and keeps the
// answer: traits and profile.
func (m *Machine) Describe(ctx context.Context) error {
	raw, err := m.hub.Call(ctx, m.id, KindDescribe, 0, nil, CallTimeout)
	if err = decodeError(err); err != nil {
		return err
	}
	var d Description
	if err := jsonUnmarshal(raw, &d); err != nil {
		return err
	}
	m.mu.Lock()
	m.desc = &d
	if d.SetupReady {
		clear(m.sums)
		for _, c := range d.Connectors {
			m.sums[c.Kind] = c.Summary
		}
	}
	m.mu.Unlock()
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
// and each worker that connects (or reports a change) is described.
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
func (h *Hosts) SetSetup(hooks SetupHooks) { h.setup = hooks }

// NewHosts hosts remote machines in set over hub and registers the hub's
// attach hook and machine.changed handler. changed may be nil.
func NewHosts(set *machine.Set, hub *workerchan.Hub, conns Connections, changed func(), log *slog.Logger) *Hosts {
	if log == nil {
		log = slog.Default()
	}
	if changed == nil {
		changed = func() {}
	}
	h := &Hosts{set: set, hub: hub, conns: conns, log: log, changed: changed}
	hub.OnAttach(h.describe)
	hub.Handle(KindChanged, func(_ context.Context, id int64, _ workerchan.Envelope) (any, error) {
		go h.describe(id)
		return Empty{}, nil
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
	if h.hub.Presence(id).Online {
		go h.describe(id)
	}
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

func (h *Hosts) describe(id int64) {
	m, ok := h.Machine(id)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), CallTimeout)
	defer cancel()
	if err := m.Describe(ctx); err != nil {
		h.log.Warn("describe remote machine", "machine", id, "err", err)
	}
	// Reconnected or changed: poll its connections now.
	h.changed()
}

// jsonUnmarshal decodes a worker reply.
func jsonUnmarshal(raw []byte, out any) error {
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("malformed worker reply: %w", err)
	}
	return nil
}
