package api_test

import (
	"testing"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/codex"
	"github.com/flexdinesh/stage/server/internal/codex/codextest"
	"github.com/flexdinesh/stage/server/internal/codexsetup"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/opencode"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
)

var codexHarnesses = harness.MustRegistry(t3.Descriptor, opencode.Descriptor, codex.Descriptor)

// codexEnv composes the real Codex setup on a desktop local machine, over
// a fake daemon.
func codexEnv(t *testing.T) (*env, *codextest.Server) {
	t.Helper()
	srv := codextest.New(t)
	daemon := &codextest.Daemon{Srv: srv, T: t}
	e := newEnv(t, func(d *api.Deps) {
		local := machine.NewLocal(db.LocalMachine, harness.Profile{Kind: harness.ProfileDesktop, LocalProcesses: true}, nil)
		set := machine.MustSet(local)
		svc := t3sync.NewForMachines(d.Store, codexHarnesses, set, nil)
		local.OnChange(svc.ConnectionsChanged)
		ports := machine.NewStorePorts(d.Store.OnMachine(db.LocalMachine), codexHarnesses)
		setup, err := codexsetup.New(t.Context(), codexsetup.Options{
			Ports:   harness.Ports{Settings: ports.Settings(), Identity: ports, Install: local.Install},
			Version: "test", Daemon: func(string) codex.Daemon { return daemon },
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(setup.Close)
		local.SetConnector(codex.Kind, codexsetup.NewConnector(setup))
		d.Harnesses, d.Sync = codexHarnesses, svc
		d.LiveConnectors = func() harness.Connectors {
			c, err := set.Connectors(codexHarnesses)
			if err != nil {
				t.Error(err)
			}
			return c
		}
	})
	return e, srv
}

// The setup routes return the full status the Codex panel reads: state,
// fields, actions and details, before and after connect.
func TestCodexSetupEndpoints(t *testing.T) {
	e, srv := codexEnv(t)
	var list hostedListing
	e.must("GET", "/api/harnesses", nil, 200, &list)
	found := false
	for _, h := range list.Harnesses {
		if h.Kind == codex.Kind {
			found = h.Available && h.Connectable
		}
	}
	if !found {
		t.Fatalf("codex not listed connectable: %+v", list)
	}

	code, st := e.raw("GET", "/api/harnesses/codex/status?refresh=1", "")
	d, _ := st["details"].(map[string]any)
	if code != 200 || st["state"] != codexsetup.StateNotConnected || d["daemon"] != "running" || d["cliVersion"] != "0.171.0" {
		t.Fatalf("initial status %d %v", code, st)
	}
	if acts, _ := st["actions"].([]any); len(acts) != 2 {
		t.Fatalf("actions %v", st["actions"])
	}

	srv.SetSignedOut(true)
	code, body := e.raw("POST", "/api/harnesses/codex/actions/connect", "{}")
	if code != 409 || body["code"] != codexsetup.CodeSignedOut {
		t.Fatalf("signed-out connect %d %v", code, body)
	}
	code, st = e.raw("GET", "/api/harnesses/codex/status", "")
	if d, _ := st["details"].(map[string]any); code != 200 || st["state"] != codexsetup.StateSignedOut || d["fix"] != "codex login" {
		t.Fatalf("signed-out status %d %v", code, st)
	}

	srv.SetSignedOut(false)
	if code, body := e.raw("POST", "/api/harnesses/codex/actions/connect", "{}"); code != 200 {
		t.Fatalf("connect %d %v", code, body)
	}
	code, st = e.raw("GET", "/api/harnesses/codex/status", "")
	d, _ = st["details"].(map[string]any)
	if code != 200 || st["state"] != codexsetup.StateConnected || st["connected"] != true || d["codexHome"] == "" {
		t.Fatalf("connected status %d %v", code, st)
	}
	if code, body := e.raw("POST", "/api/harnesses/codex/actions/nope", "{}"); code != 404 {
		t.Fatalf("unknown action %d %v", code, body)
	}
	if code, body := e.raw("PUT", "/api/harnesses/codex/settings", `{"binary":1}`); code != 400 {
		t.Fatalf("bad binary %d %v", code, body)
	}
	if code, body := e.raw("DELETE", "/api/harnesses/codex/connection", ""); code != 200 {
		t.Fatalf("disconnect %d %v", code, body)
	}
	if code, st := e.raw("GET", "/api/harnesses/codex/status", ""); code != 200 || st["state"] != codexsetup.StateNotConnected {
		t.Fatalf("after disconnect %d %v", code, st)
	}
}
