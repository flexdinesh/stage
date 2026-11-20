package api_test

import (
	"net/http"
	"testing"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/opencode"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// testHarnesses is the server composition the API tests run against.
var testHarnesses = harness.MustRegistry(t3.Descriptor, opencode.Descriptor)

// hostedFake is a harness that runs as local processes.
var hostedFake = harness.Descriptor{Kind: "hosted-fake", Name: "Hosted fake", Hosting: harness.Hosted}

type harnessesReply struct {
	Machine struct {
		ID      int64           `json:"id"`
		Label   string          `json:"label"`
		Profile harness.Profile `json:"profile"`
	} `json:"machine"`
	Harnesses []struct {
		Kind      string            `json:"kind"`
		Name      string            `json:"name"`
		Hosting   string            `json:"hosting"`
		Available bool              `json:"available"`
		Reason    string            `json:"reason"`
		Closures  []harness.Closure `json:"closures"`
	} `json:"harnesses"`
}

func TestHarnessesListDescriptorsAndMachineAvailability(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile harness.Profile
		hosted  bool
	}{
		{"desktop", harness.ResolveProfile(true, false, func(string) string { return "" }), true},
		{"foreground", harness.ResolveProfile(false, false, func(string) string { return "" }), false},
		{"foreground opt-in", harness.ResolveProfile(false, true, func(string) string { return "" }), true},
		{"container", harness.ResolveProfile(true, true, func(k string) string {
			if k == harness.ProfileEnv {
				return harness.ProfileContainer
			}
			return ""
		}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, func(d *api.Deps) {
				d.Harnesses = harness.MustRegistry(t3.Descriptor, opencode.Descriptor, hostedFake)
				d.Profile = tc.profile
			})
			var out harnessesReply
			if code := e.do(http.MethodGet, "/api/harnesses", nil, &out); code != http.StatusOK {
				t.Fatalf("status %d", code)
			}
			if out.Machine.ID != 1 || out.Machine.Profile != tc.profile || len(out.Harnesses) != 3 {
				t.Fatalf("reply %+v", out)
			}
			t3h, oc, hosted := out.Harnesses[0], out.Harnesses[1], out.Harnesses[2]
			if t3h.Kind != "t3" || t3h.Name != "T3" || !t3h.Available || len(t3h.Closures) != 1 || t3h.Closures[0].Via != "via T3 settle" {
				t.Fatalf("t3 %+v", t3h)
			}
			if oc.Kind != "opencode" || !oc.Available || oc.Closures[0].Signal != "archived" {
				t.Fatalf("opencode %+v", oc)
			}
			// Connected harnesses run anywhere; hosted ones only where the
			// profile allows local processes, with a reason otherwise.
			if hosted.Hosting != "hosted" || hosted.Available != tc.hosted || (hosted.Reason == "") == !tc.hosted || hosted.Closures == nil {
				t.Fatalf("hosted %+v", hosted)
			}
		})
	}
}
