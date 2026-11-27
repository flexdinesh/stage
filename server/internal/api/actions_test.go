package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/flexdinesh/stage/server/internal/api"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/engine"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/remote/remotetest"
	"github.com/flexdinesh/stage/server/internal/t3"
)

var displayFact = execution.Fact{
	ID: "label", Label: "Label", Value: "<script>text</script>", Tone: execution.ToneOK, Copyable: true,
}

type actionHarness struct {
	hostedAdapter
	calls   chan execution.ActionRequest
	gone    atomic.Bool
	onClear func(context.Context, execution.SessionRef) error
}

func (*actionHarness) Workspaces(context.Context) ([]execution.WorkspaceInfo, error) {
	return []execution.WorkspaceInfo{
		{ID: "repo", Title: "repo", WorkspaceRoot: hostedWorkspace},
		{ID: "alternate", Title: "alternate", WorkspaceRoot: "/work/alternate"},
		{ID: "other", Title: "other", WorkspaceRoot: "/work/other"},
	}, nil
}

func (*actionHarness) Features(ctx context.Context) (execution.Capabilities, error) {
	c, err := (hostedAdapter{}).Features(ctx)
	available := execution.Availability{Available: true}
	users := []execution.Caller{execution.CallerUser}
	both := []execution.Caller{execution.CallerUser, execution.CallerAgent}
	c.Actions = []execution.Action{
		{ID: "label", Label: "Label", Target: execution.TargetSession, Callers: both, Availability: available, Input: []execution.Field{
			{ID: "name", Label: "Name", Kind: execution.FieldSecret, Required: true},
			{ID: "scope", Label: "Scope", Kind: execution.FieldChoice, Required: true, Choices: []execution.ActionChoice{{ID: "brief", Label: "Brief"}}},
			{ID: "pinned", Label: "Pinned", Kind: execution.FieldBool, Required: true},
		}},
		{ID: "clear", Label: "Clear", Target: execution.TargetSession, Callers: users, Availability: available, Destructive: true, Confirm: "Clear the label?"},
		{ID: "busy", Label: "Busy", Target: execution.TargetSession, Callers: both, Availability: available},
		{ID: "later", Label: "Later", Target: execution.TargetSession, Callers: both, Availability: execution.Availability{Reason: "Connect first."}},
		{ID: "open-workspace", Label: "Open workspace", Target: execution.TargetProject, Callers: both, Availability: available},
	}
	return c, err
}

func (a *actionHarness) FeaturesForWorkspace(ctx context.Context, project, _ string) (execution.Capabilities, error) {
	c, err := a.Features(ctx)
	for i := range c.Actions {
		if c.Actions[i].ID == "label" {
			c.Actions[i].Label = "Label in " + project
		}
	}
	return c, err
}

func (a *actionHarness) Launch(ctx context.Context, in execution.Intent, cp execution.Checkpoint, save execution.SaveCheckpoint) (execution.Observation, error) {
	o, err := a.hostedAdapter.Launch(ctx, in, cp, save)
	withUnsafeLink := displayFact
	withUnsafeLink.Link = "javascript:bad"
	o.Facts = []execution.Fact{withUnsafeLink, displayFact}
	return o, err
}

func (a *actionHarness) Observe(ctx context.Context, ref execution.SessionRef) (execution.Observation, error) {
	if a.gone.Load() {
		return execution.Observation{}, execution.ErrNotFound
	}
	return a.hostedAdapter.Observe(ctx, ref)
}

func (a *actionHarness) Act(ctx context.Context, req execution.ActionRequest) (execution.ActionResult, error) {
	if req.ID == "busy" {
		return execution.ActionResult{}, &execution.Busy{Reason: "The harness is processing another request."}
	}
	a.calls <- req
	if req.ID == "clear" && req.Session != nil && a.onClear != nil {
		if err := a.onClear(ctx, *req.Session); err != nil {
			return execution.ActionResult{}, err
		}
	}
	return execution.ActionResult{Message: "Action completed.", Facts: []execution.Fact{displayFact},
		Link: "javascript:bad", JournalError: "forged journal failure"}, nil
}

type actionDeclarationsOnly struct {
	execution.Adapter
}

func TestDeclaredActionsLocalAndWorker(t *testing.T) {
	for _, placement := range []string{"local", "worker"} {
		t.Run(placement, func(t *testing.T) {
			ctx := t.Context()
			actor := &actionHarness{calls: make(chan execution.ActionRequest, 8)}
			actor.onClear = func(ctx context.Context, ref execution.SessionRef) error {
				_, err := testDB.Pool.ExecContext(ctx, `UPDATE tasks SET session_id=NULL WHERE session_id=
					(SELECT id FROM session_identities WHERE connection_id=$1 AND external_id=$2)`, ref.ConnectionID, ref.ExternalSessionID)
				return err
			}
			withoutActor := hostedKit
			withoutActor.Kind, withoutActor.Name = "declarations-only", "Declarations only"
			reg := harness.MustRegistry(t3.Descriptor, hostedKit, withoutActor)
			var conn, noActor int64
			var svc *engine.Engine
			e := newEnv(t, func(d *api.Deps) {
				const remoteID = 2
				machineID := db.LocalMachine
				if placement == "worker" {
					machineID = remoteID
					if _, err := testDB.Pool.ExecContext(ctx, `INSERT INTO machines(id,label) VALUES(2,'Worker')`); err != nil {
						t.Fatal(err)
					}
				}
				add := func(desc harness.Descriptor) int64 {
					t.Helper()
					sw, err := d.Store.SwitchInstance(ctx, machineID, desc, "new", desc.Name, 0)
					if err != nil {
						t.Fatal(err)
					}
					return sw.New
				}
				conn, noActor = add(hostedKit), add(withoutActor)
				adapters := map[int64]execution.Adapter{conn: actor, noActor: actionDeclarationsOnly{actor}}
				connectors := map[string]harness.Connector{
					hostedKit.Kind:    streamStatusConnector{summary: harness.Summary{ConnectionID: conn}},
					withoutActor.Kind: streamStatusConnector{summary: harness.Summary{ConnectionID: noActor}},
				}
				var set *machine.Set
				if placement == "worker" {
					set = remotetest.NewWithConnectors(t, machineID, adapters, connectors).Set()
				} else {
					local := machine.NewLocal(machineID, harness.Profile{LocalProcesses: true}, nil)
					for id, adapter := range adapters {
						local.Install(id, id, adapter)
					}
					for kind, connector := range connectors {
						local.SetConnector(kind, connector)
					}
					set = machine.MustSet(local)
				}
				svc = engine.NewForMachines(d.Store, reg, set, slog.Default())
				d.Engine, d.Harnesses = svc, reg
				d.LiveConnectors = func() harness.Connectors {
					out, err := set.Connectors(reg)
					if err != nil {
						t.Fatal(err)
					}
					return out
				}
			})
			project := func(connection int64, id, directory string) db.Project {
				t.Helper()
				p, err := e.store.InsertProject(ctx, db.Project{Title: id, Bindings: []db.ProjectBinding{{ConnectionID: connection, ExternalProjectID: id, Workspace: directory}}})
				if err != nil {
					t.Fatal(err)
				}
				return p
			}
			first := project(conn, "repo", hostedWorkspace)
			second := project(conn, "other", "/work/other")
			third := project(noActor, "third", "/work/third")
			const alternateRoot = "/work/alternate"
			bound, err := e.store.BindProject(ctx, db.ProjectBinding{
				ProjectID: first.ID, ConnectionID: conn, ExternalProjectID: "alternate", Workspace: alternateRoot,
			})
			bound = mustValue(t, bound, err)
			index := slices.IndexFunc(bound.Bindings, func(b db.ProjectBinding) bool {
				return b.ExternalProjectID == "alternate"
			})
			if index < 0 {
				t.Fatalf("alternate binding fixture missing: %+v", bound)
			}
			alternate := bound.Bindings[index]
			actorWorkspaces, err := actor.Workspaces(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for id, workspaces := range map[int64][]execution.WorkspaceInfo{
				conn: actorWorkspaces, noActor: {{ID: "third", Title: "third", WorkspaceRoot: "/work/third"}},
			} {
				if _, err := e.store.RefreshWorkspaces(ctx, id, workspaces); err != nil {
					t.Fatal(err)
				}
			}
			firstWorkspace := db.WorkspaceID(conn, "repo", hostedWorkspace)
			task, err := e.store.CreateTask(ctx, db.NewTask{
				ProjectID: first.ID, ConnectionID: conn, WorkspaceID: firstWorkspace, Title: "Task", Prompt: "Start",
			})
			if err != nil {
				t.Fatal(err)
			}
			path := fmt.Sprintf("/api/tasks/%d/actions", task.ID)
			e.must("GET", path, nil, http.StatusNotFound, nil)
			task, err = svc.Coordinator.StartTask(ctx, task.ID)
			if err != nil || task.Session == nil {
				t.Fatalf("launch: %+v %v", task, err)
			}
			_, err = e.store.SetPrimaryBinding(ctx, first.ID, alternate.ID)
			mustValue(t, struct{}{}, err)
			agent := map[string]string{"Stage-Action-Agent": "1", "Stage-Caller-Session": fmt.Sprintf("%d:agent-session", conn)}
			var actions []execution.Action
			e.must("GET", path, nil, http.StatusOK, &actions)
			if len(actions) != 4 || !slices.ContainsFunc(actions, func(a execution.Action) bool {
				return a.ID == "later" && !a.Availability.Available && a.Availability.Reason == "Connect first."
			}) {
				t.Fatalf("user actions %+v", actions)
			}
			if !slices.ContainsFunc(actions, func(a execution.Action) bool { return a.ID == "label" && a.Label == "Label in repo" }) {
				t.Fatalf("session actions left the workspace pinned at launch: %+v", actions)
			}
			if rec := raw(t, e.h, "GET", path, agent, nil, &actions); rec.Code != 200 || len(actions) != 3 ||
				slices.ContainsFunc(actions, func(a execution.Action) bool { return a.ID == "clear" }) {
				t.Fatalf("agent actions %s", rec.Body)
			}
			if rec := raw(t, e.h, "POST", path+"/clear", agent, map[string]any{"caller": "user", "permission": "all"}, nil); rec.Code != 403 {
				t.Fatalf("bypass attempt %d %s", rec.Code, rec.Body)
			}
			e.must("POST", path+"/clear", json.RawMessage(`{}`), http.StatusBadRequest, nil)
			e.must("POST", path+"/later", json.RawMessage(`{}`), http.StatusConflict, nil)
			e.must("POST", path+"/open-workspace", json.RawMessage(`{}`), http.StatusNotFound, nil)
			e.must("POST", path+"/busy", json.RawMessage(`{}`), http.StatusConflict, nil)
			for _, bad := range []string{
				`{"name":"fake-secret","scope":"missing","pinned":false}`,
				`{"name":"fake-secret","scope":"brief"}`,
				`{"name":"fake-secret","scope":"brief","pinned":null}`,
				`{"name":"fake-secret","scope":"brief","pinned":"false"}`,
				`{"name":"fake-secret","scope":"brief","pinned":false,"caller":"user"}`,
				`{"name":"fake-secret","scope":"brief","pinned":true,"pinned":false}`,
			} {
				rec := raw(t, e.h, "POST", path+"/label", agent, json.RawMessage(bad), nil)
				if rec.Code != 400 || strings.Contains(rec.Body.String(), "fake-secret") {
					t.Fatalf("invalid input %d %s", rec.Code, rec.Body)
				}
			}
			actor.gone.Store(true)
			input := json.RawMessage(`{"name":"fake-secret","scope":"brief","pinned":false}`)
			e.must("POST", path+"/label", input, http.StatusNotFound, nil)
			actor.gone.Store(false)
			if len(actor.calls) != 0 {
				t.Fatal("a refused action reached the actor")
			}
			var result execution.ActionResult
			if rec := raw(t, e.h, "POST", path+"/label", agent, input, &result); rec.Code != 200 {
				t.Fatalf("run %d %s", rec.Code, rec.Body)
			}
			req := <-actor.calls
			if req.Session == nil || *req.Session != *task.Session || string(req.Input) != string(input) || result.Link != "" ||
				result.JournalError != "" || !slices.Equal(result.Facts, []execution.Fact{displayFact}) {
				t.Fatalf("dispatch or result wrong: %+v %+v", req, result)
			}
			var detail struct{ Facts []execution.Fact }
			e.must("GET", fmt.Sprintf("/api/tasks/%d", task.ID), nil, 200, &detail)
			if !slices.Equal(detail.Facts, []execution.Fact{displayFact}) {
				t.Fatalf("stored facts %+v", detail.Facts)
			}
			journal := e.journal(fmt.Sprintf("/api/tasks/%d/journal?kind=events", task.ID))
			var eventCount int
			for _, entry := range journal.Entries {
				if entry.EventType == "session_action" {
					eventCount++
					if strings.Contains(entry.Text, "fake-secret") || strings.Contains(entry.Text, displayFact.Value) ||
						!strings.Contains(entry.Text, "label") || !strings.Contains(entry.Text, task.Session.ExternalSessionID) ||
						!strings.Contains(entry.Text, "called by agent") || entry.SourceSession == nil ||
						entry.SourceSession.ExternalSessionID != "agent-session" {
						t.Fatalf("journal event %+v", entry)
					}
				}
			}
			if eventCount != 1 {
				t.Fatalf("session actions in journal = %d", eventCount)
			}
			actionPath := func(projectID, connectionID int64, selector string) string {
				return fmt.Sprintf("/api/projects/%d/actions/open-workspace?connectionId=%d&workspaceId=%s", projectID, connectionID, selector)
			}
			for _, p := range []db.Project{first, second} {
				b := p.Bindings[0]
				selector := db.WorkspaceID(b.ConnectionID, b.ExternalProjectID, b.Workspace)
				e.must("POST", actionPath(p.ID, conn, selector), json.RawMessage(`{}`), http.StatusOK, &result)
				req := <-actor.calls
				if req.Session != nil || req.Project == nil || req.Project.ExternalProjectID != b.ExternalProjectID || req.Project.Directory != b.Workspace {
					t.Fatalf("project action went to the wrong workspace: %+v", req)
				}
			}
			e.must("POST", fmt.Sprintf("/api/projects/%d/actions/open-workspace?workspaceId=%s", first.ID, firstWorkspace),
				json.RawMessage(`{}`), http.StatusBadRequest, nil)
			e.must("POST", actionPath(first.ID, conn, ""), json.RawMessage(`{}`), http.StatusConflict, nil)
			thirdWorkspace := db.WorkspaceID(noActor, "third", "/work/third")
			e.must("POST", actionPath(first.ID, noActor, thirdWorkspace), json.RawMessage(`{}`), http.StatusNotFound, nil)
			e.must("POST", actionPath(third.ID, noActor, thirdWorkspace), json.RawMessage(`{}`), http.StatusConflict, nil)

			alternateWorkspace := db.WorkspaceID(conn, "alternate", alternateRoot)
			explicit := actionPath(first.ID, conn, alternateWorkspace)
			e.must("POST", explicit, json.RawMessage(`{}`), http.StatusOK, &result)
			if req := <-actor.calls; req.Project == nil || req.Project.ExternalProjectID != "alternate" || req.Project.Directory != alternateRoot {
				t.Fatalf("explicit selector routed to the wrong workspace: %+v", req)
			}
			msg := e.refused("POST", explicit+fmt.Sprintf("&bindingId=%d", alternate.ID), json.RawMessage(`{}`), http.StatusBadRequest)
			if !strings.Contains(msg, "workspaceId") {
				t.Fatalf("retired action selector: %q", msg)
			}
			e.must("POST", actionPath(second.ID, conn, alternateWorkspace), json.RawMessage(`{}`), http.StatusNotFound, nil)
			e.must("POST", actionPath(first.ID, noActor, alternateWorkspace), json.RawMessage(`{}`), http.StatusConflict, nil)

			alias, err := e.store.InsertProject(ctx, db.Project{Title: "Legacy link"})
			alias = mustValue(t, alias, err)
			_, err = e.store.MergeProjects(ctx, first.ID, alias.ID, true)
			mustValue(t, struct{}{}, err)
			e.must("POST", actionPath(alias.ID, conn, alternateWorkspace), json.RawMessage(`{}`), http.StatusOK, &result)
			if req := <-actor.calls; req.Project == nil || req.Project.ExternalProjectID != "alternate" {
				t.Fatalf("alias action routed to the wrong workspace: %+v", req)
			}

			edit := db.GroupEditInput{ConnectionID: conn, WorkspaceID: alternateWorkspace, TargetProjectID: &second.ID}
			var preview db.GroupEdit
			e.must("POST", "/api/project-groups/membership", edit, http.StatusOK, &preview)
			edit.PreviewToken = preview.PreviewToken
			e.must("POST", "/api/project-groups/membership", edit, http.StatusOK, nil)
			e.must("POST", explicit, json.RawMessage(`{}`), http.StatusNotFound, nil)
			e.must("POST", actionPath(second.ID, conn, alternateWorkspace), json.RawMessage(`{}`), http.StatusOK, &result)
			if req := <-actor.calls; req.Project == nil || req.Project.ExternalProjectID != "alternate" {
				t.Fatalf("regrouped action routed to the wrong workspace: %+v", req)
			}
			if rec := raw(t, e.h, "POST", path+"/clear", map[string]string{"Stage-Action-Confirmed": "1"}, json.RawMessage(`{}`), &result); rec.Code != 200 ||
				!strings.Contains(result.JournalError, "action ran") || len(actor.calls) != 1 {
				t.Fatalf("journal failure %d %s, calls %d", rec.Code, rec.Body, len(actor.calls))
			}
			<-actor.calls
			if _, err := testDB.Pool.ExecContext(ctx, `UPDATE tasks SET session_id=$1 WHERE id=$2`, task.SessionIdentityID, task.ID); err != nil {
				t.Fatal(err)
			}
			if err := e.store.RetireConnection(ctx, conn, 0); err != nil {
				t.Fatal(err)
			}
			e.must("POST", path+"/label", input, http.StatusConflict, nil)
			e.must("POST", actionPath(second.ID, conn, alternateWorkspace), json.RawMessage(`{}`), http.StatusConflict, nil)
			if len(actor.calls) != 0 {
				t.Fatal("an unrelated or retired target reached the actor")
			}
		})
	}
}
