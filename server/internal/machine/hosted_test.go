package machine_test

import (
	"context"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
)

var (
	hostedKind    = harness.Descriptor{Kind: "proc", Name: "Proc", Hosting: harness.Hosted}
	connectedKind = harness.Descriptor{Kind: "svc", Name: "Svc", Hosting: harness.Connected}
)

// A local machine allows what its profile allows; the connectors index
// leaves out a connector for a kind its machine does not allow.
func TestLocalAllowsHostedKindsOnlyWhereItsProfileDoes(t *testing.T) {
	reg := harness.MustRegistry(connectedKind, hostedKind)
	desktop := machine.NewLocal(1, harness.Profile{Kind: harness.ProfileDesktop, LocalProcesses: true}, nil)
	server := machine.NewLocal(2, harness.Profile{Kind: harness.ProfileServer}, nil)
	for _, m := range []*machine.Local{desktop, server} {
		m.SetConnector(hostedKind.Kind, connector{})
		m.SetConnector(connectedKind.Kind, connector{})
	}
	if a := desktop.Allows(hostedKind); !a.Available || a.Reason != "" {
		t.Fatalf("desktop hosted %+v", a)
	}
	if a := server.Allows(hostedKind); a.Available || a.Reason != server.Profile().Allows(hostedKind).Reason || a.Reason == "" {
		t.Fatalf("server hosted %+v", a)
	}
	if a := server.Allows(connectedKind); !a.Available {
		t.Fatalf("server connected %+v", a)
	}
	cs, err := machine.MustSet(desktop, server).Connectors(reg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cs.Get(1, hostedKind.Kind); !ok {
		t.Fatal("desktop hosted connector missing")
	}
	if _, ok := cs.Get(2, hostedKind.Kind); ok {
		t.Fatal("server indexed a hosted connector its profile refuses")
	}
	if _, ok := cs.Get(2, connectedKind.Kind); !ok {
		t.Fatal("server connected connector missing")
	}
	// The machine's own connectors are untouched.
	if _, ok := server.Connector(hostedKind.Kind); !ok {
		t.Fatal("index filtering removed the machine's connector")
	}
}

// A hosted connect action creates the machine's connection row through the
// identity port and installs the adapter on it; the same identity keeps
// the connection, another replaces it.
func TestConnectCreatesTheConnectionRowAndInstallsItsAdapter(t *testing.T) {
	var testDB dbtest.DB
	store := db.NewStore(testDB.Reset(t))
	reg := harness.MustRegistry(hostedKind)
	ports := machine.NewStorePorts(store.OnMachine(db.LocalMachine), reg)
	local := machine.NewLocal(db.LocalMachine, harness.Profile{Kind: harness.ProfileDesktop, LocalProcesses: true}, nil)
	var moves [][2]int64
	local.OnChange(func(old, next int64) { moves = append(moves, [2]int64{old, next}) })
	newAdapter := func(int64) execution.Adapter { return &adapter{name: "proc"} }
	ctx := t.Context()

	if id, err := ports.Active(ctx, hostedKind.Kind); err != nil || id != 0 {
		t.Fatalf("active before connect %d %v", id, err)
	}
	res, err := machine.Connect(ctx, ports, local.Install, hostedKind.Kind, "/home/u/.claude", "u@example", newAdapter)
	if err != nil {
		t.Fatal(err)
	}
	first, err := ports.Active(ctx, hostedKind.Kind)
	if err != nil || first == 0 || res.Replaced != 0 {
		t.Fatalf("first connect: active %d, %+v, %v", first, res, err)
	}
	c, err := ports.GetHarnessConnection(ctx, first)
	if err != nil || c.Harness != hostedKind.Kind || c.MachineID != db.LocalMachine || c.Label != "u@example" {
		t.Fatalf("connection row %+v %v", c, err)
	}
	if _, ok := local.Adapter(first); !ok || len(moves) != 1 || moves[0] != [2]int64{0, first} {
		t.Fatalf("install %v", moves)
	}

	// The same identity keeps the connection (and relabels it).
	if res, err = machine.Connect(ctx, ports, local.Install, hostedKind.Kind, "/home/u/.claude", "renamed", newAdapter); err != nil || res.Replaced != 0 {
		t.Fatalf("reconnect %+v %v", res, err)
	}
	if id, _ := ports.Active(ctx, hostedKind.Kind); id != first {
		t.Fatalf("same identity moved to %d", id)
	}

	// Another identity on a connection that holds nothing restamps it in
	// place (db.Store.SwitchInstance): nothing is replaced.
	if res, err = machine.Connect(ctx, ports, local.Install, hostedKind.Kind, "/home/v/.claude", "v", newAdapter); err != nil || res.Replaced != 0 {
		t.Fatalf("restamp %+v %v", res, err)
	}
	if id, _ := ports.Active(ctx, hostedKind.Kind); id != first {
		t.Fatalf("restamp moved to %d", id)
	}
}

// switchIdentity answers SwitchInstance with a fixed switch.
type switchIdentity struct {
	machine.Identity
	sw db.Switch
}

func (s switchIdentity) SwitchInstance(context.Context, string, string, string, int64) (db.Switch, error) {
	return s.sw, nil
}

// A replaced instance: the adapter moves to the new connection, the old
// one is no longer reached, and the result names it.
func TestConnectReportsTheReplacedConnection(t *testing.T) {
	local := machine.NewLocal(1, harness.Profile{}, nil)
	local.Install(5, 5, &adapter{name: "old"})
	fresh := &adapter{name: "new"}
	res, err := machine.Connect(t.Context(), switchIdentity{sw: db.Switch{Old: 5, New: 6, Changed: true}}, local.Install, hostedKind.Kind, "x", "x", func(int64) execution.Adapter { return fresh })
	if err != nil || res.Replaced != 5 {
		t.Fatalf("%+v %v", res, err)
	}
	if _, ok := local.Adapter(5); ok {
		t.Fatal("replaced connection still reached")
	}
	if a, ok := local.Adapter(6); !ok || a != fresh {
		t.Fatal("new connection not installed")
	}
}
