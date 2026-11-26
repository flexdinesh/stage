package machine_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
)

type adapter struct {
	execution.Adapter
	name string
}

type connector struct{ harness.Connector }

func (connector) Summary(context.Context) harness.Summary { return harness.Summary{} }
func (connector) Status(context.Context, bool) (harness.Status, error) {
	return harness.Status{}, nil
}
func (connector) Configure(context.Context, json.RawMessage) (harness.Result, error) {
	return harness.Result{}, nil
}
func (connector) Run(context.Context, string, json.RawMessage) (harness.Result, error) {
	return harness.Result{}, nil
}
func (connector) Disconnect(context.Context) error { return nil }

// Install moves a harness between connections and reports each move: the
// old connection is no longer reachable, and a nil adapter disconnects.
func TestLocalInstallMovesAdaptersAndReportsChanges(t *testing.T) {
	l := machine.NewLocal(1, harness.Profile{Kind: harness.ProfileDesktop, LocalProcesses: true}, nil)
	var changes [][2]int64
	l.OnChange(func(old, next int64) { changes = append(changes, [2]int64{old, next}) })
	a, b := &adapter{name: "a"}, &adapter{name: "b"}
	l.Install(2, 2, a)
	if got, ok := l.Adapter(2); !ok || got != a {
		t.Fatalf("installed %v %v", got, ok)
	}
	l.Install(2, 5, b) // the instance was replaced
	if _, ok := l.Adapter(2); ok {
		t.Fatal("the replaced connection is still reachable")
	}
	if got, ok := l.Adapter(5); !ok || got != b {
		t.Fatalf("moved %v %v", got, ok)
	}
	l.Install(5, 5, nil) // disconnected
	if len(l.Adapters()) != 0 {
		t.Fatalf("disconnect left %v", l.Adapters())
	}
	if !slices.Equal(changes, [][2]int64{{2, 2}, {2, 5}, {5, 5}}) {
		t.Fatalf("changes %v", changes)
	}
	if !l.Online() || l.Profile().Kind != harness.ProfileDesktop || l.ID() != 1 {
		t.Fatal("local machine identity")
	}
	if _, ok := l.Ops().(machine.LocalOps); !ok {
		t.Fatal("default ops are the local ones")
	}
}

// A set finds each connection's adapter on whichever machine hosts it and
// indexes connectors per machine against the registry.
func TestSetRoutesConnectionsToTheirMachine(t *testing.T) {
	local, laptop := machine.NewLocal(1, harness.Profile{}, nil), machine.NewLocal(2, harness.Profile{}, nil)
	a, b := &adapter{name: "a"}, &adapter{name: "b"}
	local.Install(3, 3, a)
	laptop.Install(4, 4, b)
	set := machine.MustSet(laptop, local)
	if set.DefaultID() != laptop.ID() || set.Remove(laptop.ID()) {
		t.Fatal("the designated default must remain hosted")
	}
	if ids := []int64{set.All()[0].ID(), set.All()[1].ID()}; !slices.Equal(ids, []int64{1, 2}) {
		t.Fatalf("order %v", ids)
	}
	if got, ok := set.Adapter(4); !ok || got != b {
		t.Fatalf("laptop adapter %v", got)
	}
	if _, ok := set.Adapter(9); ok {
		t.Fatal("unknown connection resolved")
	}
	if all := set.Adapters(); len(all) != 2 || all[3] != a || all[4] != b {
		t.Fatalf("adapters %v", all)
	}
	if m, ok := set.Get(2); !ok || m != laptop {
		t.Fatal("default machine lookup")
	}
	if _, err := machine.NewSet(local, machine.NewLocal(1, harness.Profile{}, nil)); err == nil {
		t.Fatal("a machine hosted twice")
	}
	if _, err := machine.NewSet(machine.NewLocal(0, harness.Profile{}, nil)); err == nil {
		t.Fatal("machine id 0")
	}

	reg := harness.MustRegistry(harness.Descriptor{Kind: "doc", Name: "Doc", Sessions: harness.SessionsMachine})
	laptop.SetConnector("doc", connector{})
	connectors, err := set.Connectors(reg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := connectors.Get(2, "doc"); !ok {
		t.Fatal("laptop connector")
	}
	if _, ok := connectors.Get(1, "doc"); ok {
		t.Fatal("local machine has no doc connector")
	}
	local.SetConnector("unknown", connector{})
	if _, err := set.Connectors(reg); err == nil {
		t.Fatal("indexed a connector of an unregistered kind")
	}
}
