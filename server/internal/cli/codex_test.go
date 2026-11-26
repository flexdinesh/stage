package cli_test

import (
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/codex"
	"github.com/flexdinesh/stage/server/internal/codex/codextest"
	"github.com/flexdinesh/stage/server/internal/codexsetup"
	"github.com/flexdinesh/stage/server/internal/db"
	stageharness "github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
)

func TestCodexCommands(t *testing.T) {
	srv := codextest.New(t)
	daemon := &codextest.Daemon{Srv: srv, T: t}
	h := newHarness(t, func(d *api.Deps) {
		reg := stageharness.MustRegistry(codex.Descriptor)
		local := machine.NewLocal(db.LocalMachine, stageharness.Profile{Kind: stageharness.ProfileDesktop, LocalProcesses: true}, nil)
		svc := t3sync.NewForMachines(d.Store, machine.MustSet(local), nil)
		svc.Harnesses = reg
		local.OnChange(svc.ConnectionsChanged)
		ports := machine.NewStorePorts(d.Store.OnMachine(db.LocalMachine), reg)
		setup, err := codexsetup.New(t.Context(), codexsetup.Options{
			Ports:   stageharness.Ports{Settings: ports.Settings(), Identity: ports, Install: local.Install},
			Version: "test", Daemon: func(string) codex.Daemon { return daemon },
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(setup.Close)
		c, err := stageharness.NewConnectors(reg, map[int64]map[string]stageharness.Connector{db.LocalMachine: {codex.Kind: codexsetup.NewConnector(setup)}})
		if err != nil {
			t.Fatal(err)
		}
		d.Harnesses, d.Connectors, d.Sync = reg, c, svc
	})
	code, out, errOut := h.run("", "harness", "codex")
	if code != 0 || !strings.Contains(out, "codex not connected") || !strings.Contains(out, "daemon      running") || !strings.Contains(out, "harness codex run connect") {
		t.Fatalf("codex: %d %q %q", code, out, errOut)
	}
	srv.SetSignedOut(true)
	if code, _, errOut := h.run("", "harness", "codex", "run", "connect"); code != 1 || !strings.Contains(errOut, "stage API 409: Codex isn't signed in") {
		t.Fatalf("signed out: %d %q", code, errOut)
	}
	srv.SetSignedOut(false)
	code, out, errOut = h.run("", "harness", "codex", "run", "connect")
	if code != 0 || !strings.Contains(out, "codex connected") || !strings.Contains(out, "account") {
		t.Fatalf("connect: %d %q %q", code, out, errOut)
	}
	if code, out, errOut := h.run("", "harness", "codex", "disconnect"); code != 0 || !strings.Contains(out, "codex not connected") {
		t.Fatalf("disconnect: %d %q %q", code, out, errOut)
	}
}
