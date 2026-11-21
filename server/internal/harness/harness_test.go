package harness_test

import (
	"context"
	"testing"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

func TestRegistryValidatesDescriptors(t *testing.T) {
	ok := harness.Descriptor{Kind: "a", Name: "A", Hosting: harness.Connected}
	for name, ds := range map[string][]harness.Descriptor{
		"no kind":            {{Name: "A", Hosting: harness.Connected}},
		"no name":            {{Kind: "a", Hosting: harness.Connected}},
		"twice":              {ok, ok},
		"unknown hosting":    {{Kind: "a", Name: "A", Hosting: "cloud"}},
		"unknown signal":     {{Kind: "a", Name: "A", Hosting: harness.Connected, Closures: []harness.Closure{{Signal: "merged", Provenance: "harness_merged"}}}},
		"mismatched closure": {{Kind: "a", Name: "A", Hosting: harness.Connected, Closures: []harness.Closure{{Signal: execution.ClosedSettled, Provenance: "harness_archived"}}}},
		"duplicate field":    {{Kind: "a", Name: "A", Hosting: harness.Connected, Setup: harness.Setup{Fields: []harness.Field{{ID: "u", Label: "U", Kind: harness.FieldURL}, {ID: "u", Label: "U2", Kind: harness.FieldText}}}}},
		"unknown field kind": {{Kind: "a", Name: "A", Hosting: harness.Connected, Setup: harness.Setup{Fields: []harness.Field{{ID: "u", Label: "U", Kind: "date"}}}}},
		"unlabeled action":   {{Kind: "a", Name: "A", Hosting: harness.Connected, Setup: harness.Setup{Actions: []harness.Action{{ID: "go"}}}}},
		"duplicate input":    {{Kind: "a", Name: "A", Hosting: harness.Connected, Setup: harness.Setup{Actions: []harness.Action{{ID: "go", Label: "Go", Input: []harness.Field{{ID: "x", Label: "X", Kind: harness.FieldText}, {ID: "x", Label: "X", Kind: harness.FieldText}}}}}}},
	} {
		if _, err := harness.NewRegistry(ds...); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	b := harness.Descriptor{Kind: "b", Name: "B", Hosting: harness.Hosted, Closures: []harness.Closure{{Signal: execution.ClosedArchived, Provenance: "harness_archived", Via: "via B"}}}
	r, err := harness.NewRegistry(b, ok)
	if err != nil {
		t.Fatal(err)
	}
	if k := r.Kinds(); len(k) != 2 || k[0] != "b" || k[1] != "a" {
		t.Fatalf("kinds %v", k)
	}
	if r.Name("b") != "B" || r.Name("zzz") != "zzz" {
		t.Fatal("names")
	}
	if c, found := b.Closure(execution.ClosedArchived); !found || c.Via != "via B" {
		t.Fatalf("closure %+v", c)
	}
	if _, found := b.Closure(execution.ClosedSettled); found {
		t.Fatal("undeclared closure")
	}
}

func TestProfilePolicy(t *testing.T) {
	none := func(string) string { return "" }
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	container := env(map[string]string{harness.ProfileEnv: harness.ProfileContainer})
	cases := []struct {
		name  string
		got   harness.Profile
		want  harness.Profile
		local bool
	}{
		{"managed", harness.ResolveProfile(true, false, none), harness.Profile{Kind: harness.ProfileDesktop, LocalProcesses: true}, true},
		{"foreground", harness.ResolveProfile(false, false, none), harness.Profile{Kind: harness.ProfileServer}, false},
		{"foreground flag", harness.ResolveProfile(false, true, none), harness.Profile{Kind: harness.ProfileServer, LocalProcesses: true}, true},
		{"foreground env", harness.ResolveProfile(false, false, env(map[string]string{harness.LocalHarnessesEnv: "1"})), harness.Profile{Kind: harness.ProfileServer, LocalProcesses: true}, true},
		{"container wins", harness.ResolveProfile(true, true, container), harness.Profile{Kind: harness.ProfileContainer}, false},
	}
	hosted := harness.Descriptor{Kind: "h", Name: "H", Hosting: harness.Hosted}
	connected := harness.Descriptor{Kind: "c", Name: "C", Hosting: harness.Connected}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, c.got, c.want)
		}
		if a := c.got.Allows(hosted); a.Available != c.local || (a.Reason == "") != c.local {
			t.Errorf("%s: hosted %+v", c.name, a)
		}
		if a := c.got.Allows(connected); !a.Available {
			t.Errorf("%s: connected %+v", c.name, a)
		}
	}
}

type nopConnector struct{ harness.Connector }

// idConnector serves one active connection.
type idConnector struct {
	harness.Connector
	id int64
}

func (c idConnector) Summary(context.Context) harness.Summary {
	return harness.Summary{ConnectionID: c.id}
}

func TestConnectorsAreCheckedAgainstTheRegistry(t *testing.T) {
	conn := harness.Descriptor{Kind: "svc", Name: "Svc", Hosting: harness.Connected}
	hosted := harness.Descriptor{Kind: "proc", Name: "Proc", Hosting: harness.Hosted}
	reg := harness.MustRegistry(conn, hosted)
	one := func(kind string) map[int64]map[string]harness.Connector {
		return map[int64]map[string]harness.Connector{1: {kind: nopConnector{}}}
	}
	if _, err := harness.NewConnectors(reg, one("nope")); err == nil {
		t.Fatal("connector for an unregistered kind accepted")
	}
	if _, err := harness.NewConnectors(reg, one("proc")); err == nil {
		t.Fatal("connector for a hosted kind accepted")
	}
	if _, err := harness.NewConnectors(reg, map[int64]map[string]harness.Connector{0: {"svc": nopConnector{}}}); err == nil {
		t.Fatal("connector for machine 0 accepted")
	}
	cs, err := harness.NewConnectors(reg, one("svc"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cs.Get(1, "svc"); !ok {
		t.Fatal("svc connector missing")
	}
	if _, ok := cs.Get(1, "proc"); ok {
		t.Fatal("proc has no connector")
	}
	if _, ok := cs.Get(2, "svc"); ok {
		t.Fatal("machine 2 has no connector")
	}
	var zero harness.Connectors
	zero.Each(func(int64, harness.Descriptor, harness.Connector) { t.Fatal("zero Connectors has none") })
}

func TestConnectorsServeEachMachinesActiveConnection(t *testing.T) {
	a := harness.Descriptor{Kind: "a", Name: "A", Hosting: harness.Connected}
	b := harness.Descriptor{Kind: "b", Name: "B", Hosting: harness.Connected}
	reg := harness.MustRegistry(a, b)
	cs, err := harness.NewConnectors(reg, map[int64]map[string]harness.Connector{
		2: {"a": idConnector{id: 21}, "b": idConnector{id: 22}},
		1: {"b": idConnector{id: 12}, "a": idConnector{id: 11}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Machines in id order, kinds in registry order.
	var seen []int64
	cs.Each(func(machine int64, d harness.Descriptor, k harness.Connector) {
		if want := machine*10 + map[string]int64{"a": 1, "b": 2}[d.Kind]; k.Summary(t.Context()).ConnectionID != want {
			t.Fatalf("machine %d %s serves %d", machine, d.Kind, k.Summary(t.Context()).ConnectionID)
		}
		seen = append(seen, k.Summary(t.Context()).ConnectionID)
	})
	if len(seen) != 4 || seen[0] != 11 || seen[1] != 12 || seen[2] != 21 || seen[3] != 22 {
		t.Fatalf("order %v", seen)
	}
	// The same kind on two machines: each machine's connector owns its own
	// active connection.
	if d, k, ok := cs.Owner(t.Context(), 21); !ok || d.Kind != "a" || k.Summary(t.Context()).ConnectionID != 21 {
		t.Fatalf("owner of 21: %v %+v", ok, d)
	}
	if d, k, ok := cs.Owner(t.Context(), 11); !ok || d.Kind != "a" || k.Summary(t.Context()).ConnectionID != 11 {
		t.Fatalf("owner of 11: %v %+v", ok, d)
	}
	if _, _, ok := cs.Owner(t.Context(), 99); ok {
		t.Fatal("unknown connection owned")
	}
}
