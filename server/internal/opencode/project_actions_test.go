package opencode

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/opencode/opencodetest"
)

// projectFixture serves the fake with one project. Storing 1 in the
// returned counter loses the next project PATCH reply after applying it, 2
// before applying it.
func projectFixture(t *testing.T, configure func(*opencodetest.Server)) (*Adapter, *opencodetest.Server, *atomic.Int32) {
	t.Helper()
	fake := opencodetest.New(t, "")
	fake.AddProject("prj_app", "/w/app", false)
	if configure != nil {
		configure(fake)
	}
	var lose atomic.Int32
	base := fake.Config.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode := lose.Load(); r.Method == http.MethodPatch && mode != 0 {
			lose.Store(0)
			if mode == 1 {
				base.ServeHTTP(httptest.NewRecorder(), r)
			}
			conn, _, err := http.NewResponseController(w).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
			return
		}
		base.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	a, err := New(3, Settings{Endpoint: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return a, fake, &lose
}

func renameRequest(project, input string) execution.ActionRequest {
	return execution.ActionRequest{ID: ProjectRename, Target: execution.TargetProject,
		Project: &execution.ActionWorkspace{ExternalProjectID: project, Directory: "/w/app"}, Input: []byte(input)}
}

func projectRenameDeclaration(t *testing.T, a *Adapter) execution.Action {
	t.Helper()
	c, err := a.Features(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := execution.ValidateActions(c.Actions, ""); err != nil {
		t.Fatal(err)
	}
	for _, action := range c.Actions {
		if action.Target == execution.TargetProject {
			return action
		}
	}
	t.Fatalf("no project action in %+v", c.Actions)
	return execution.Action{}
}

func TestProjectRenameInOpenCode(t *testing.T) {
	a, fake, _ := projectFixture(t, nil)
	action := projectRenameDeclaration(t, a)
	if action.ID != ProjectRename || action.Label != "Rename in OpenCode" || !action.Availability.Available ||
		action.Destructive || action.Confirm != "" || !action.Allows(execution.CallerAgent) ||
		len(action.Input) != 1 || action.Input[0].ID != "name" || !action.Input[0].Required {
		t.Fatalf("declaration %+v", action)
	}
	res, err := a.Act(t.Context(), renameRequest("prj_app", `{"name":" Billing "}`))
	if err != nil || res.Message != "Renamed in OpenCode to Billing." {
		t.Fatal(res, err)
	}
	if got := fake.ProjectName("prj_app"); got != "Billing" {
		t.Fatalf("name %q", got)
	}
	ws, err := a.Workspaces(t.Context())
	if err != nil || len(ws) != 1 || ws[0].Title != "Billing" {
		t.Fatalf("inventory %+v %v", ws, err)
	}
}

func TestProjectRenameRefusals(t *testing.T) {
	a, fake, _ := projectFixture(t, nil)
	var inv *execution.Invalid
	if _, err := a.Act(t.Context(), renameRequest("prj_app", `{"name":"  "}`)); !errors.As(err, &inv) || inv.Reason != "Enter the project's new name." {
		t.Fatalf("blank name = %v", err)
	}
	if _, err := a.Act(t.Context(), renameRequest(globalProject, `{"name":"x"}`)); !errors.As(err, &inv) || inv.Reason != globalRenameReason {
		t.Fatalf("global = %v", err)
	}
	if _, err := a.Act(t.Context(), renameRequest("prj_gone", `{"name":"x"}`)); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("unknown project = %v", err)
	}
	fake.FailProjectUpdate(http.StatusBadRequest, "name too long")
	if _, err := a.Act(t.Context(), renameRequest("prj_app", `{"name":"x"}`)); !errors.As(err, &inv) || inv.Reason != "OpenCode refused the new name: name too long" {
		t.Fatalf("refused = %v", err)
	}
	if _, err := a.Act(t.Context(), execution.ActionRequest{ID: "explode", Target: execution.TargetProject,
		Project: &execution.ActionWorkspace{ExternalProjectID: "prj_app"}, Input: []byte(`{}`)}); err == nil {
		t.Fatal("unknown action accepted")
	}
	if fake.ProjectName("prj_app") != "" {
		t.Fatal("a refused request renamed the project")
	}
}

func TestProjectRenameFollowsTheContract(t *testing.T) {
	a, fake, _ := projectFixture(t, func(s *opencodetest.Server) { s.WithoutProjectUpdate() })
	action := projectRenameDeclaration(t, a)
	if action.Availability.Available || action.Availability.Reason != projectRenameUnsupported {
		t.Fatalf("declaration %+v", action)
	}
	if _, err := a.Act(t.Context(), renameRequest("prj_app", `{"name":"x"}`)); !errors.Is(err, execution.ErrUnavailable) {
		t.Fatalf("rename without contract = %v", err)
	}
	if fake.ProjectName("prj_app") != "" {
		t.Fatal("renamed without the contract")
	}
}

func TestProjectRenameUnavailableForGlobal(t *testing.T) {
	actions := projectActions(serverContract{projectRename: true})
	if got := projectActionStates(actions, globalProject)[0].Availability; got.Available || got.Reason != globalRenameReason {
		t.Fatalf("global %+v", got)
	}
	if got := projectActionStates(actions, "prj_app")[0].Availability; !got.Available {
		t.Fatalf("project %+v", got)
	}
	if !actions[0].Availability.Available {
		t.Fatal("projectActionStates changed its input")
	}
}

func TestProjectRenameLostReply(t *testing.T) {
	a, fake, lose := projectFixture(t, nil)
	lose.Store(1) // applied, reply lost: the listing confirms it
	if res, err := a.Act(t.Context(), renameRequest("prj_app", `{"name":"Billing"}`)); err != nil || res.Message == "" {
		t.Fatal(res, err)
	}
	lose.Store(2) // never applied: uncertain
	var uncertain *execution.ActionUncertain
	if _, err := a.Act(t.Context(), renameRequest("prj_app", `{"name":"Other"}`)); !errors.As(err, &uncertain) {
		t.Fatalf("lost before applying = %v", err)
	}
	if fake.ProjectName("prj_app") != "Billing" {
		t.Fatalf("name %q", fake.ProjectName("prj_app"))
	}
}

// An archived OpenCode project's actions are not listed: its refusal is
// typed, so the project action listing relays it as a 400 with the reason.
func TestArchivedProjectRefusalIsRelayed(t *testing.T) {
	a, _, _ := projectFixture(t, func(s *opencodetest.Server) { s.AddProject("prj_old", "/w/old", true) })
	_, err := a.FeaturesForWorkspace(t.Context(), "prj_old", "/w/old")
	var inv *execution.Invalid
	if !errors.As(err, &inv) || inv.Reason != "Archived in OpenCode. Unarchive it there, then refresh." {
		t.Fatalf("archived features = %T %v", err, err)
	}
}
