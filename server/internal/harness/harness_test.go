package harness_test

import (
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
