// Package machine is the machine-runtime seam: one host per machine for
// its harness adapters, connectors and machine-local fs/process work
// (docs/execution.md "Worker-ready placement", ADR 0002). The coordinator,
// the runners, the API and connection setup reach harness code only
// through it. Local is the in-process machine every install runs; a remote
// machine is a second implementation of the same interface. The package
// names no harness: harness packages build what a machine hosts.
package machine

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

// Machine hosts one machine's harness code.
type Machine interface {
	ID() int64
	// Online: the machine is reachable now (the local machine always is).
	Online() bool
	// Profile is the machine's hosting profile (harness.ResolveProfile).
	Profile() harness.Profile
	// Allows reports whether the machine may host d, with the reason when
	// not: the one hosting decision the API, the connectors index and the
	// coordinator read (the local machine: its profile's policy).
	Allows(d harness.Descriptor) execution.Availability
	// Adapter is the installed adapter of one of the machine's active
	// connections.
	Adapter(connection int64) (execution.Adapter, bool)
	// Adapters are the machine's installed adapters by connection.
	Adapters() map[int64]execution.Adapter
	// Retired drops a connection's adapter after its record is retired.
	Retired(connection int64)
	// AgentGateways are the live worker's agent gateway listener URLs.
	AgentGateways() []string
	// Connector is the machine's connection setup for a harness kind.
	Connector(kind string) (harness.Connector, bool)
	// Connectors are the machine's connectors by kind.
	Connectors() map[string]harness.Connector
	// Ops is the machine's fs and process work.
	Ops() Ops
}

// Local is the machine in this process. Setup code installs its adapters
// (Install, the install port) and the server registers its connectors.
// Safe for concurrent use.
type Local struct {
	id      int64
	profile harness.Profile
	ops     Ops

	mu         sync.RWMutex
	adapters   map[int64]execution.Adapter
	connectors map[string]harness.Connector
	onChange   func(old, next int64)
}

// NewLocal is an empty in-process machine with ops (nil: LocalOps).
func NewLocal(id int64, profile harness.Profile, ops Ops) *Local {
	if ops == nil {
		ops = LocalOps{}
	}
	return &Local{id: id, profile: profile, ops: ops, adapters: map[int64]execution.Adapter{}, connectors: map[string]harness.Connector{}}
}

var _ Machine = (*Local)(nil)

func (l *Local) ID() int64                { return l.id }
func (l *Local) Online() bool             { return true }
func (l *Local) Profile() harness.Profile { return l.profile }
func (l *Local) Ops() Ops                 { return l.ops }
func (l *Local) AgentGateways() []string  { return nil }

// Allows is the machine's profile policy (harness.Profile.Allows).
func (l *Local) Allows(d harness.Descriptor) execution.Availability { return l.profile.Allows(d) }

func (l *Local) Adapter(connection int64) (execution.Adapter, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	a, ok := l.adapters[connection]
	return a, ok
}

func (l *Local) Adapters() map[int64]execution.Adapter {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return maps.Clone(l.adapters)
}

func (l *Local) Connector(kind string) (harness.Connector, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	c, ok := l.connectors[kind]
	return c, ok
}

func (l *Local) Connectors() map[string]harness.Connector {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return maps.Clone(l.connectors)
}

// OnChange sets the hook that runs after every Install (the coordinator
// and runners follow the machine's connections). Set it before setup code
// installs anything.
func (l *Local) OnChange(fn func(old, next int64)) {
	l.mu.Lock()
	l.onChange = fn
	l.mu.Unlock()
}

// Install moves a harness's adapter from connection old to next (equal
// unless its instance was replaced): old is no longer reached; a nil
// adapter uninstalls next (disconnected).
func (l *Local) Install(old, next int64, a execution.Adapter) {
	l.mu.Lock()
	if old != next {
		delete(l.adapters, old)
	}
	if a == nil {
		delete(l.adapters, next)
	} else {
		l.adapters[next] = a
	}
	fn := l.onChange
	l.mu.Unlock()
	if fn != nil {
		fn(old, next)
	}
}

// Retired drops a retired connection's adapter: it is never polled or
// launched on again, even once the harness is set up for another connection.
// The machine's change hook runs as for an install.
func (l *Local) Retired(id int64) { l.Install(id, id, nil) }

// SetConnector registers the machine's connector for kind.
func (l *Local) SetConnector(kind string, c harness.Connector) {
	l.mu.Lock()
	l.connectors[kind] = c
	l.mu.Unlock()
}

// Set is the server's machines, in id order. Remote machines join and
// leave while the server runs (Add, Remove). Safe for concurrent use.
type Set struct {
	localID int64
	mu      sync.RWMutex
	list    []Machine
}

// NewSet hosts local as the server's default machine and validates all ids.
func NewSet(local Machine, others ...Machine) (*Set, error) {
	ms := append([]Machine{local}, others...)
	slices.SortFunc(ms, func(a, b Machine) int { return cmp.Compare(a.ID(), b.ID()) })
	for i, m := range ms {
		if m.ID() <= 0 {
			return nil, fmt.Errorf("machine with invalid id %d", m.ID())
		}
		if i > 0 && ms[i-1].ID() == m.ID() {
			return nil, fmt.Errorf("machine %d hosted twice", m.ID())
		}
	}
	return &Set{localID: local.ID(), list: ms}, nil
}

// MustSet is NewSet for fixed compositions and tests.
func MustSet(local Machine, others ...Machine) *Set {
	s, err := NewSet(local, others...)
	if err != nil {
		panic(err)
	}
	return s
}

// DefaultID is the machine this server runs on.
func (s *Set) DefaultID() int64 { return s.localID }

// AgentGateways gives a remote machine's live gateway URLs. A machine id
// of zero (unknown connection placement) uses the server's local API.
func (s *Set) AgentGateways(id int64) (remote bool, urls []string) {
	if id == 0 || id == s.localID {
		return false, nil
	}
	if m, ok := s.Get(id); ok {
		return true, m.AgentGateways()
	}
	return true, nil
}

// Add hosts m (a machine that joined); an error when its id is invalid or
// already hosted.
func (s *Set) Add(m Machine) error {
	if m.ID() <= 0 {
		return fmt.Errorf("machine with invalid id %d", m.ID())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i, found := slices.BinarySearchFunc(s.list, m.ID(), func(x Machine, id int64) int { return cmp.Compare(x.ID(), id) })
	if found {
		return fmt.Errorf("machine %d hosted twice", m.ID())
	}
	s.list = slices.Insert(s.list, i, m)
	return nil
}

// Remove stops hosting machine id (it was removed); the in-process machine
// stays. False when no remote machine id was hosted.
func (s *Set) Remove(id int64) bool {
	if id == s.localID {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, m := range s.list {
		if m.ID() != id {
			continue
		}
		s.list = slices.Delete(s.list, i, i+1)
		return true
	}
	return false
}

// Get is the host of machine id.
func (s *Set) Get(id int64) (Machine, bool) {
	for _, m := range s.All() {
		if m.ID() == id {
			return m, true
		}
	}
	return nil, false
}

// Local looks up an in-process host for adapter setup.
func (s *Set) Local(id int64) (*Local, bool) {
	m, _ := s.Get(id)
	l, ok := m.(*Local)
	return l, ok
}

// All machines in id order.
func (s *Set) All() []Machine {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.list)
}

// Adapter is connection's installed adapter, on whichever machine hosts it.
func (s *Set) Adapter(connection int64) (execution.Adapter, bool) {
	for _, m := range s.All() {
		if a, ok := m.Adapter(connection); ok {
			return a, true
		}
	}
	return nil, false
}

// Adapters are every machine's installed adapters by connection.
func (s *Set) Adapters() map[int64]execution.Adapter {
	out := map[int64]execution.Adapter{}
	for _, m := range s.All() {
		maps.Copy(out, m.Adapters())
	}
	return out
}

// Connectors indexes every machine's connectors, checked against reg:
// each names a registered kind. A connector for a kind its machine does
// not allow (Machine.Allows) is left out, so nothing probes it.
func (s *Set) Connectors(reg *harness.Registry) (harness.Connectors, error) {
	m := map[int64]map[string]harness.Connector{}
	for _, mc := range s.All() {
		cs := mc.Connectors()
		for kind := range cs {
			if d, ok := reg.Lookup(kind); ok && !mc.Allows(d).Available {
				delete(cs, kind)
			}
		}
		if len(cs) > 0 {
			m[mc.ID()] = cs
		}
	}
	return harness.NewConnectors(reg, m)
}

// Ops is the fs and process work that must run where the harness runs.
// Every argument and result is JSON-serializable, so a remote machine can
// carry the calls over its worker channel.
type Ops interface {
	// Inventory reads each workspace's Git evidence (Repo, RepoPath, and
	// Importable under ImportIf) from the checkout the machine sees through
	// roots (one connection's shared paths; RootsFor adds a local path).
	Inventory(ctx context.Context, ws []execution.WorkspaceInfo, roots []SharedRoot) ([]execution.WorkspaceInfo, error)
	// Checkout is the Git evidence for a harness-side directory under
	// roots (zero when it is under none, or unreadable).
	Checkout(ctx context.Context, roots []SharedRoot, dir string) (CheckoutEvidence, error)
}

// SharedRoot is one declared shared path: a harness-side root and the same
// directory as Stage sees it on the machine.
type SharedRoot struct {
	HarnessRoot string `json:"harnessRoot"`
	StageRoot   string `json:"stageRoot"`
}

// CheckoutEvidence proves which Git checkout a directory belongs to.
type CheckoutEvidence struct {
	// Common identifies the checkout's Git common directory on its machine
	// (a device:inode key); "" when unproven. Comparable only within one
	// machine.
	Common string `json:"common,omitempty"`
	// Subproject is the directory relative to the checkout's top level.
	Subproject string `json:"subproject,omitempty"`
	// Remote is the normalized origin remote (gitmeta.RemoteIdentity).
	Remote string `json:"remote,omitempty"`
	// RepoPath is the slash path from the checkout top ("." at the top), ""
	// when unknown. A remote suggestion needs equal RepoPaths when both
	// sides know theirs.
	RepoPath string `json:"repoPath,omitempty"`
}

// SameRemote: equal remotes and, when both are known, equal subprojects.
func (e CheckoutEvidence) SameRemote(o CheckoutEvidence) bool {
	return e.Remote != "" && e.Remote == o.Remote && (e.RepoPath == "" || o.RepoPath == "" || e.RepoPath == o.RepoPath)
}

// SameCheckout: both are proven, the same Git common directory and the
// same subproject.
func (e CheckoutEvidence) SameCheckout(o CheckoutEvidence) bool {
	return e.Common != "" && o.Common != "" && e.Common == o.Common && e.Subproject == o.Subproject
}
