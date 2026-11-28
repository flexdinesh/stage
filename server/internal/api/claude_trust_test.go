package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/engine"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/remote/remotetest"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// Claude Code's trust, on a fake claude with a temporary home, in process
// and over a worker: an untrusted listed folder is a blocked destination
// whose project action trusts it (only when confirmed, by a person or an
// agent), after which the refreshed inventory unblocks it; the trust
// source adds an unlisted folder, and again reports it existing.
func TestClaudeTrustLocalAndWorker(t *testing.T) {
	for _, placement := range []string{"local", "worker"} {
		t.Run(placement, func(t *testing.T) {
			ctx := t.Context()
			f := claudetest.New(t)
			lab, fresh := filepath.Join(f.Home, "lab"), filepath.Join(f.Home, "fresh")
			for _, d := range []string{lab, fresh} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			state := filepath.Join(f.Home, ".claude.json")
			if err := os.WriteFile(state, []byte(fmt.Sprintf(`{"projects":{%q:{"hasTrustDialogAccepted":true},%q:{"hasTrustDialogAccepted":false}}}`, f.Workspace, lab)), 0o600); err != nil {
				t.Fatal(err)
			}
			var conn int64
			e := newEnv(t, func(d *api.Deps) {
				machineID := db.LocalMachine
				if placement == "worker" {
					machineID = 2
					if _, err := testDB.Pool.ExecContext(ctx, `INSERT INTO machines(id,label) VALUES(2,'Worker')`); err != nil {
						t.Fatal(err)
					}
				}
				sw, err := d.Store.SwitchInstance(ctx, machineID, claudecode.Descriptor, f.ConfigDir(), "Claude", 0)
				conn = mustValue(t, sw, err).New
				opts := f.Options(conn)
				opts.StateLockWait = 200 * time.Millisecond
				adapters := map[int64]execution.Adapter{conn: claudecode.New(opts)}
				connectors := map[string]harness.Connector{claudecode.Kind: streamStatusConnector{summary: harness.Summary{ConnectionID: conn}}}
				var set *machine.Set
				if placement == "worker" {
					set = remotetest.NewWithConnectors(t, machineID, adapters, connectors).Set()
				} else {
					local := machine.NewLocal(machineID, harness.Profile{Kind: harness.ProfileServer, LocalProcesses: true}, machine.LocalOps{})
					local.Install(conn, conn, adapters[conn])
					set = machine.MustSet(local)
				}
				d.Harnesses = harness.MustRegistry(t3.Descriptor, claudecode.Descriptor)
				d.Engine = engine.NewForMachines(d.Store, d.Harnesses, set, d.Logger)
			})

			destination := func(root string) db.Destination {
				t.Helper()
				var inventory workspaceInventory
				e.must("GET", fmt.Sprintf("/api/connections/%d/workspaces?refresh=true", conn), nil, 200, &inventory)
				var catalog client.ProjectSummaries
				e.must("GET", "/api/projects?view=destinations", nil, 200, &catalog)
				i := slices.IndexFunc(inventory.Workspaces, func(w db.Workspace) bool { return w.Workspace == root })
				if i < 0 {
					t.Fatalf("%s not listed: %+v", root, inventory.Workspaces)
				}
				j := slices.IndexFunc(catalog.Destinations, func(d db.Destination) bool { return d.WorkspaceID == inventory.Workspaces[i].WorkspaceID })
				if j < 0 || catalog.Destinations[j].GroupProjectID == nil {
					t.Fatalf("%s not a destination: %+v", root, catalog.Destinations)
				}
				return catalog.Destinations[j]
			}
			actionsPath := func(d db.Destination) string {
				return fmt.Sprintf("/api/projects/%d/actions?connectionId=%d&workspaceId=%s", *d.GroupProjectID, conn, d.WorkspaceID)
			}
			trustAction := func(d db.Destination) execution.Action {
				t.Helper()
				var actions []execution.Action
				e.must("GET", actionsPath(d), nil, 200, &actions)
				i := slices.IndexFunc(actions, func(a execution.Action) bool { return a.ID == claudecode.ActionTrust })
				if i < 0 {
					t.Fatalf("actions %+v", actions)
				}
				return actions[i]
			}

			blocked := destination(lab)
			if blocked.Available || !strings.Contains(blocked.Reason, "Use Trust in Claude Code") {
				t.Fatalf("untrusted destination %+v", blocked)
			}
			if a := trustAction(blocked); !a.Availability.Available || a.Confirm == "" {
				t.Fatalf("trust on the blocked row %+v", a)
			}
			if a := trustAction(destination(f.Workspace)); a.Availability.Available || !strings.Contains(a.Availability.Reason, "already trusts") {
				t.Fatalf("trust on a trusted row %+v", a)
			}

			run := strings.Replace(actionsPath(blocked), "/actions?", "/actions/trust?", 1)
			agent := map[string]string{"Stage-Action-Agent": "1"}
			var refused errOut
			if rec := raw(t, e.h, "POST", run, agent, json.RawMessage(`{}`), &refused); rec.Code != 400 || !strings.Contains(refused.Error, "Confirm before running Trust in Claude Code") {
				t.Fatalf("unconfirmed agent trust %d %+v", rec.Code, refused)
			}
			agent["Stage-Action-Confirmed"] = "1"
			var result execution.ActionResult
			if rec := raw(t, e.h, "POST", run, agent, json.RawMessage(`{}`), &result); rec.Code != 200 || !strings.Contains(result.Message, "now trusts") {
				t.Fatalf("confirmed agent trust %d %s", rec.Code, rec.Body)
			}
			// The action refreshed the inventory: the row can launch now.
			var catalog client.ProjectSummaries
			e.must("GET", "/api/projects?view=destinations", nil, 200, &catalog)
			if i := slices.IndexFunc(catalog.Destinations, func(d db.Destination) bool { return d.WorkspaceID == blocked.WorkspaceID }); i < 0 || !catalog.Destinations[i].Available {
				t.Fatalf("trusted destination %+v", catalog.Destinations)
			}

			body := map[string]any{"sourceId": claudecode.SourceTrust, "input": map[string]string{"path": fresh}, "requestId": "fresh-1"}
			var out addError
			if got := e.do("POST", fmt.Sprintf("/api/connections/%d/workspaces", conn), body, &out); got != 400 || !strings.Contains(out.Error, "Send confirmed: true") {
				t.Fatalf("unconfirmed add %d %+v", got, out)
			}
			body["confirmed"] = true
			var added addedWorkspace
			e.must("POST", fmt.Sprintf("/api/connections/%d/workspaces", conn), body, 201, &added)
			if added.Existing || added.Workspace.Path != fresh || !added.Workspace.Available || !strings.Contains(added.Message, "now trusts") {
				t.Fatalf("added %+v", added)
			}
			e.must("POST", fmt.Sprintf("/api/connections/%d/workspaces", conn), body, 201, &added)
			if !added.Existing || added.Workspace.Path != fresh {
				t.Fatalf("again %+v", added)
			}

			// A live holder of Claude's lock: nothing changes, retry is safe.
			other := filepath.Join(f.Home, "other")
			if err := os.Mkdir(other, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(state+".lock", 0o755); err != nil {
				t.Fatal(err)
			}
			body = map[string]any{"sourceId": claudecode.SourceTrust, "input": map[string]string{"path": other}, "requestId": "other-1", "confirmed": true}
			if got := e.do("POST", fmt.Sprintf("/api/connections/%d/workspaces", conn), body, &out); got != http.StatusConflict || out.Code != "harness_busy" {
				t.Fatalf("locked add %d %+v", got, out)
			}
			b, err := os.ReadFile(state)
			if err != nil || strings.Contains(string(b), other) || !strings.Contains(string(b), lab) || !strings.Contains(string(b), fresh) {
				t.Fatalf("state %s, %v", b, err)
			}
		})
	}
}
