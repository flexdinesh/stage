package api_test

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/engine"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/remote/remotetest"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// TestT3WorkspaceAddLocalAndWorker adds T3 workspaces through each source,
// with the adapter on the server and on a worker (adapter.workspaceAdd).
func TestT3WorkspaceAddLocalAndWorker(t *testing.T) {
	for _, placement := range []string{"local", "worker"} {
		t.Run(placement, func(t *testing.T) {
			ctx := t.Context()
			var (
				e    *env
				fake *t3.Fake
				conn int64
			)
			if placement == "local" {
				e, conn = newEnv(t), dbtest.T3Connection
				fake = e.fake
			} else {
				fake = t3.NewFake()
				reg := harness.MustRegistry(t3.Descriptor)
				e = newEnv(t, func(d *api.Deps) {
					if _, err := testDB.Pool.ExecContext(ctx, `INSERT INTO machines(id,label) VALUES(2,'Worker')`); err != nil {
						t.Fatal(err)
					}
					sw, err := d.Store.SwitchInstance(ctx, 2, t3.Descriptor, "new", t3.Descriptor.Name, 0)
					if err != nil {
						t.Fatal(err)
					}
					conn = sw.New
					set := remotetest.NewWithConnectors(t, 2,
						map[int64]execution.Adapter{conn: t3.NewConnectionAdapter(fake, conn)},
						map[string]harness.Connector{t3.Kind: streamStatusConnector{summary: harness.Summary{ConnectionID: conn}}},
					).Set()
					d.Engine, d.Harnesses = engine.NewForMachines(d.Store, reg, set, slog.Default()), reg
					d.LiveConnectors = func() harness.Connectors {
						out, err := set.Connectors(reg)
						if err != nil {
							t.Fatal(err)
						}
						return out
					}
				})
			}
			route := fmt.Sprintf("/api/connections/%d/workspaces", conn)

			var caps execution.Capabilities
			e.must("GET", fmt.Sprintf("/api/connections/%d/capabilities", conn), nil, 200, &caps)
			if !caps.Operations[execution.OpWorkspaceAdd].Available || len(caps.WorkspaceSources) != 3 ||
				caps.WorkspaceSources[0].ID != t3.SourceFolder || caps.WorkspaceSources[1].ID != t3.SourceClone || caps.WorkspaceSources[2].ID != t3.SourceNew {
				t.Fatalf("T3 capabilities %+v %+v", caps.Operations[execution.OpWorkspaceAdd], caps.WorkspaceSources)
			}

			// Existing folder.
			var added addedWorkspace
			e.must("POST", route, addBody(t3.SourceFolder, "/work/app", "req-folder"), 201, &added)
			if added.Existing || added.Workspace.Path != "/work/app" || added.Workspace.Title != "app" || added.Workspace.ConnectionID != conn ||
				added.Group.ID == 0 || !strings.Contains(added.Message, "Registered /work/app") {
				t.Fatalf("folder add %+v", added)
			}
			var again addedWorkspace
			e.must("POST", route, addBody(t3.SourceFolder, "/work/app/", "req-folder"), 201, &again)
			if !again.Existing || again.Workspace.WorkspaceID != added.Workspace.WorkspaceID || len(fake.Created) != 1 {
				t.Fatalf("folder repeat %+v (creates %d)", again, len(fake.Created))
			}

			// Clone repository.
			clone := map[string]any{"sourceId": t3.SourceClone, "requestId": "req-clone",
				"input": map[string]any{"remoteUrl": "git@example.com:o/repo.git", "destination": "/src/repo"}}
			e.must("POST", route, clone, 201, &added)
			if added.Existing || added.Workspace.Path != "/src/repo" || added.Workspace.Title != "repo" || len(fake.Cloned) != 1 {
				t.Fatalf("clone add %+v", added)
			}

			// New project: a commit failure is a warning fact.
			fake.CommitError = "git user.email is not set"
			newProject := map[string]any{"sourceId": t3.SourceNew, "requestId": "req-new", "input": map[string]any{"title": "Billing"}}
			e.must("POST", route, newProject, 201, &added)
			if added.Workspace.Title != "Billing" || !strings.HasSuffix(added.Workspace.Path, "/projects/billing") ||
				len(added.Facts) != 1 || added.Facts[0].Tone != execution.ToneWarn {
				t.Fatalf("new add %+v", added)
			}

			// T3's refusals are 400s with its words; a relative path never
			// reaches T3.
			var out addError
			if got := e.do("POST", route, addBody(t3.SourceFolder, "work/rel", "req-rel"), &out); got != 400 || !strings.Contains(out.Error, "absolute path") {
				t.Fatalf("relative %d %+v", got, out)
			}
			fake.MissFolder("/work/gone")
			if got := e.do("POST", route, addBody(t3.SourceFolder, "/work/gone", "req-gone"), &out); got != 400 ||
				!strings.Contains(out.Error, "could not be completed") || !strings.Contains(out.Error, "Create folder if missing") {
				t.Fatalf("missing folder %d %+v", got, out)
			}

			// A lost reply T3 cannot confirm is 504; a lost reply it did act
			// on is reconciled.
			fake.LoseCreate = 1
			e.must("POST", route, addBody(t3.SourceFolder, "/work/lost", "req-lost"), 201, &added)
			if added.Existing || added.Workspace.Path != "/work/lost" {
				t.Fatalf("reconciled %+v", added)
			}
			fake.CreateErr = errors.New("t3 t3_project_create: connection reset")
			if got := e.do("POST", route, addBody(t3.SourceFolder, "/work/unsent", "req-unsent"), &out); got != 504 || out.Code != "outcome_uncertain" {
				t.Fatalf("uncertain %d %+v", got, out)
			}
			fake.CreateErr = nil

			var events struct {
				Events []db.ProjectEvent `json:"events"`
			}
			e.must("GET", fmt.Sprintf("/api/projects/%d/events", again.Group.ID), nil, 200, &events)
			if len(events.Events) == 0 || events.Events[0].Type != "workspace_added" || !strings.Contains(events.Events[0].Text, "(Existing folder)") {
				t.Fatalf("events %+v", events.Events)
			}
		})
	}
}
