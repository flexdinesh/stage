package api_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
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

// TestT3ProjectActionsLocalAndWorker renames, archives, restores and
// removes T3 projects through declared project actions, with the adapter
// on the server and on a worker: availability follows T3's archive state,
// remove is user-only and confirmed, T3's refusals are relayed, and every
// action refreshes the inventory while the Stage group title stays.
func TestT3ProjectActionsLocalAndWorker(t *testing.T) {
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
				})
			}
			fake.AddProject("App", "/work/app")
			busy := fake.AddProject("Busy", "/work/busy")
			if _, err := fake.LaunchThread(ctx, t3.LaunchInput{ProjectID: busy.ID, Title: "work"}); err != nil {
				t.Fatal(err)
			}
			e.must("GET", fmt.Sprintf("/api/connections/%d/workspaces?refresh=true", conn), nil, 200, nil)

			member := func(root string) (db.ProjectGroup, db.GroupWorkspace) {
				t.Helper()
				var out struct {
					Groups []db.ProjectGroup `json:"groups"`
				}
				e.must("GET", "/api/project-groups?archived=all", nil, 200, &out)
				for _, g := range out.Groups {
					for _, m := range g.Members {
						if m.ConnectionID == conn && m.Path == root {
							return g, m
						}
					}
				}
				t.Fatalf("%s is in no group: %+v", root, out.Groups)
				return db.ProjectGroup{}, db.GroupWorkspace{}
			}
			group, app := member("/work/app")
			_, busyRow := member("/work/busy")
			path := func(m db.GroupWorkspace, action string) string {
				g, _ := member(m.Path)
				base := fmt.Sprintf("/api/projects/%d/actions", g.ID)
				if action != "" {
					base += "/" + action
				}
				return fmt.Sprintf("%s?connectionId=%d&workspaceId=%s", base, conn, m.WorkspaceID)
			}
			agent := map[string]string{"Stage-Action-Agent": "1"}
			available := func(m db.GroupWorkspace, headers map[string]string) map[string]string {
				t.Helper()
				var actions []execution.Action
				if rec := raw(t, e.h, "GET", path(m, ""), headers, nil, &actions); rec.Code != 200 {
					t.Fatalf("list actions %d %s", rec.Code, rec.Body)
				}
				out := map[string]string{}
				for _, a := range actions {
					if a.Target == execution.TargetProject {
						out[a.ID] = a.Availability.Reason
					}
				}
				return out
			}
			run := func(m db.GroupWorkspace, action string, headers map[string]string, body string, want int) execution.ActionResult {
				t.Helper()
				var res execution.ActionResult
				if rec := raw(t, e.h, "POST", path(m, action), headers, json.RawMessage(body), &res); rec.Code != want {
					t.Fatalf("%s: status %d, want %d: %s", action, rec.Code, want, rec.Body)
				}
				return res
			}
			refusal := func(m db.GroupWorkspace, action string, headers map[string]string, want int) string {
				t.Helper()
				var out errOut
				if rec := raw(t, e.h, "POST", path(m, action), headers, json.RawMessage(`{}`), &out); rec.Code != want {
					t.Fatalf("%s: status %d, want %d: %s", action, rec.Code, want, rec.Body)
				}
				return out.Error
			}

			if got := available(app, nil); len(got) != 4 || got[t3.ProjectRename] != "" || got[t3.ProjectArchive] != "" ||
				got[t3.ProjectRestore] != "Not archived in T3." || got[t3.ProjectRemove] != "" {
				t.Fatalf("listed actions %v", got)
			}
			if got := available(app, agent); len(got) != 3 || slices.Contains(mapKeys(got), t3.ProjectRemove) {
				t.Fatalf("agent sees %v", got)
			}

			// Rename: T3's title changes, the refreshed row shows it, the
			// Stage group keeps its title.
			if res := run(app, t3.ProjectRename, nil, `{"title":"Billing"}`, 200); res.Message != "Renamed in T3 to Billing." {
				t.Fatalf("rename %+v", res)
			}
			if g, m := member("/work/app"); m.Title != "Billing" || g.Title != group.Title || g.ID != group.ID {
				t.Fatalf("after rename group %q member %+v (group was %q)", g.Title, m, group.Title)
			}

			// An agent archives it; the archived row stays a target and
			// offers Restore.
			if res := run(app, t3.ProjectArchive, agent, `{}`, 200); res.Message != "Archived in T3." {
				t.Fatalf("archive %+v", res)
			}
			if _, m := member("/work/app"); m.CatalogState != "archived" {
				t.Fatalf("after archive %+v", m)
			}
			if got := available(app, nil); got[t3.ProjectArchive] != "Already archived in T3." || got[t3.ProjectRestore] != "" || got[t3.ProjectRename] != "" {
				t.Fatalf("archived actions %v", got)
			}
			if msg := refusal(app, t3.ProjectArchive, nil, http.StatusConflict); msg != "Already archived in T3." {
				t.Fatalf("archive twice %q", msg)
			}
			run(app, t3.ProjectRestore, agent, `{}`, 200)
			if _, m := member("/work/app"); m.CatalogState != "listed" {
				t.Fatalf("after restore %+v", m)
			}

			// A lost reply T3's listing can't confirm is uncertain.
			fake.UpdateErr = errors.New("t3 t3_project_update: connection reset")
			if msg := refusal(app, t3.ProjectArchive, nil, http.StatusBadGateway); !strings.Contains(msg, "Check the harness") {
				t.Fatalf("uncertain archive %q", msg)
			}
			fake.UpdateErr = nil

			// Remove: user-only, confirmed, T3's refusal relayed.
			if msg := refusal(app, t3.ProjectRemove, agent, http.StatusForbidden); msg != "This action is available to users only." {
				t.Fatalf("agent remove %q", msg)
			}
			if msg := refusal(app, t3.ProjectRemove, nil, http.StatusBadRequest); msg != "Confirm this action before running it." {
				t.Fatalf("unconfirmed remove %q", msg)
			}
			confirmed := map[string]string{"Stage-Action-Confirmed": "1"}
			if msg := refusal(busyRow, t3.ProjectRemove, confirmed, http.StatusBadRequest); msg != "T3 can't remove a project with threads. Archive it instead." {
				t.Fatalf("remove busy %q", msg)
			}
			if len(fake.Deleted) != 1 {
				t.Fatalf("deletes %v", fake.Deleted)
			}
			if res := run(app, t3.ProjectRemove, confirmed, `{}`, 200); !strings.Contains(res.Message, "Removed from T3") {
				t.Fatalf("remove %+v", res)
			}
			if _, m := member("/work/app"); m.CatalogState != "missing" {
				t.Fatalf("after remove %+v", m)
			}
			if fake.Updated[0].Title != "Billing" || len(fake.Deleted) != 2 {
				t.Fatalf("T3 calls updated %+v deleted %v", fake.Updated, fake.Deleted)
			}
		})
	}
}

func mapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
