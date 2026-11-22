package api_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/opencode"
	"github.com/flexdinesh/stage/server/internal/remote"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/workerchan"
)

// remoteEnv hosts joined machines with remote connection setup (no worker
// connects: they stay offline).
func remoteEnv(t *testing.T) *env {
	t.Helper()
	return newEnv(t, withPublicURL, func(d *api.Deps) {
		hub := workerchan.NewHub(t.Context(), workerchan.Options{Version: buildinfo.Version})
		set := machine.MustSet(machine.NewLocal(db.LocalMachine, harness.Profile{Kind: harness.ProfileServer}, nil))
		hosts := remote.NewHosts(set, hub, func(int64) []int64 { return nil }, nil, nil)
		hosts.SetSetup(remote.SetupHooks{Kinds: []string{t3.Kind, opencode.Kind}, Active: d.Store.ActiveID})
		local := d.Connectors
		d.Channels, d.Machines = hub, hosts
		d.LiveConnectors = func() harness.Connectors {
			m := map[int64]map[string]harness.Connector{}
			local.Each(func(id int64, dd harness.Descriptor, c harness.Connector) {
				if m[id] == nil {
					m[id] = map[string]harness.Connector{}
				}
				m[id][dd.Kind] = c
			})
			for _, hm := range set.All() {
				if hm.ID() != db.LocalMachine {
					m[hm.ID()] = hm.Connectors()
				}
			}
			c, err := harness.NewConnectors(testHarnesses, m)
			if err != nil {
				t.Error(err)
			}
			return c
		}
	})
}

type machineHarnessesOut struct {
	Machine struct {
		ID      int64  `json:"id"`
		Kind    string `json:"kind"`
		Status  string `json:"status"`
		Profile struct {
			Kind string `json:"kind"`
		} `json:"profile"`
	} `json:"machine"`
	Harnesses []struct {
		Kind        string `json:"kind"`
		Connectable bool   `json:"connectable"`
	} `json:"harnesses"`
}

func TestMachineHarnessRoutes(t *testing.T) {
	e := remoteEnv(t)
	var local, alias machineHarnessesOut
	e.must("GET", "/api/harnesses", nil, 200, &alias)
	e.must("GET", "/api/machines/1/harnesses", nil, 200, &local)
	if alias.Machine.ID != 1 || alias.Machine.Kind != "local" || local.Machine.Kind != "local" || len(local.Harnesses) != len(alias.Harnesses) {
		t.Fatalf("local machine harnesses %+v / %+v", local, alias)
	}
	e.must("GET", "/api/machines/999/harnesses", nil, 404, nil)
	e.must("GET", "/api/machines/999/harnesses/t3/status", nil, 404, nil)

	j := e.join("laptop")
	var remoteOut machineHarnessesOut
	e.must("GET", fmt.Sprintf("/api/machines/%d/harnesses", j.MachineID), nil, 200, &remoteOut)
	if remoteOut.Machine.Kind != "remote" || remoteOut.Machine.Status != "offline" || remoteOut.Machine.Profile.Kind != harness.ProfileServer {
		t.Fatalf("remote machine %+v", remoteOut.Machine)
	}
	for _, h := range remoteOut.Harnesses {
		if want := h.Kind == t3.Kind || h.Kind == opencode.Kind; h.Connectable != want {
			t.Errorf("%s connectable %v", h.Kind, h.Connectable)
		}
	}
	// An offline machine's setup answers at once, coded.
	var out errOut
	base := fmt.Sprintf("/api/machines/%d/harnesses/", j.MachineID)
	for _, c := range []struct{ method, path string }{
		{"GET", base + "t3/status"},
		{"PUT", base + "t3/settings"},
		{"POST", base + "opencode/actions/connect-local"},
		{"DELETE", base + "opencode/connection"},
	} {
		out = errOut{}
		if rec := raw(t, e.h, c.method, c.path, nil, map[string]any{}, &out); rec.Code != 503 || out.Code != remote.CodeMachineOffline {
			t.Errorf("%s %s: %d %+v", c.method, c.path, rec.Code, out)
		}
	}
	e.must("POST", base+"t3/actions/nope", map[string]any{}, 404, nil)
	e.must("GET", base+"nope/status", nil, 404, nil)
	// Removed: gone.
	e.must("DELETE", fmt.Sprintf("/api/machines/%d", j.MachineID), nil, 204, nil)
	e.must("GET", base+"t3/status", nil, 404, nil)
	// The pairing status route still answers next to the new routes.
	e.must("GET", "/api/machines/pairings/123", nil, 404, nil)
	e.must("GET", "/api/machines/nope/123", nil, 404, nil)
	// Connections lists stay memory only for remote summaries.
	if _, err := e.store.Connections(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerGatewayRefusesHarnessSetupChanges(t *testing.T) {
	e := remoteEnv(t)
	j := e.join("laptop")
	base := fmt.Sprintf("/api/machines/%d/harnesses/", j.MachineID)
	for _, c := range []struct{ method, path string }{
		{"PUT", base + "t3/settings"},
		{"POST", base + "t3/actions/pair"},
		{"DELETE", base + "opencode/connection"},
		{"PUT", "/api/machines/1/harnesses/t3/settings"},
	} {
		var out errOut
		if rec := e.gw(j.Token, c.method, c.path, map[string]any{}, &out); rec.Code != 403 || out.Code != "gateway_forbidden" {
			t.Errorf("%s %s: %d %+v", c.method, c.path, rec.Code, out)
		}
	}
	// Reading setup stays allowed.
	if rec := e.gw(j.Token, "GET", fmt.Sprintf("/api/machines/%d/harnesses", j.MachineID), nil, nil); rec.Code != 200 {
		t.Fatalf("read harnesses through the gateway: %d", rec.Code)
	}
}
