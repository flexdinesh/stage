package t3

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/flexdinesh/stage/server/internal/execution"
)

func projectRequest(id string, p Project, input string) execution.ActionRequest {
	return execution.ActionRequest{ID: id, Target: execution.TargetProject,
		Project: &execution.ActionWorkspace{ExternalProjectID: p.ID, Directory: p.WorkspaceRoot}, Input: []byte(input)}
}

// projectAvailability is each project action's unavailable reason ("" when
// available) for the workspace.
func projectAvailability(t *testing.T, a *Adapter, p Project) map[string]string {
	t.Helper()
	c, err := a.FeaturesForWorkspace(t.Context(), p.ID, p.WorkspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, action := range c.Actions {
		if action.Target == execution.TargetProject {
			out[action.ID] = action.Availability.Reason
		}
	}
	return out
}

func TestProjectActionsDeclared(t *testing.T) {
	c, err := NewAdapter(NewFake()).Features(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := execution.ValidateActions(c.Actions, ""); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		ProjectRename: "Rename in T3", ProjectArchive: "Archive in T3",
		ProjectRestore: "Restore in T3", ProjectRemove: "Remove from T3",
	}
	got := map[string]execution.Action{}
	for _, a := range c.Actions {
		if a.Target == execution.TargetProject {
			got[a.ID] = a
		}
	}
	if len(got) != len(want) {
		t.Fatalf("project actions %+v", got)
	}
	for id, label := range want {
		a := got[id]
		if a.Label != label || !a.Availability.Available {
			t.Fatalf("%s declared %+v", id, a)
		}
		agents := id != ProjectRemove
		if a.Allows(execution.CallerAgent) != agents || !a.Allows(execution.CallerUser) || a.Destructive == agents {
			t.Fatalf("%s callers %v destructive %v", id, a.Callers, a.Destructive)
		}
	}
	if f := got[ProjectRename].Input; len(f) != 1 || f[0].ID != "title" || !f[0].Required {
		t.Fatalf("rename input %+v", f)
	}
	if got[ProjectRemove].Confirm != "Remove this project from T3? T3 keeps the files on disk. A project with threads can't be removed; archive it instead." {
		t.Fatalf("remove confirm %q", got[ProjectRemove].Confirm)
	}
	if got[ProjectArchive].Confirm == "" || got[ProjectRestore].Confirm != "" || got[ProjectRename].Confirm != "" {
		t.Fatal("confirm texts")
	}
}

func TestProjectActionsFollowArchiveState(t *testing.T) {
	f := NewFake()
	a := NewAdapter(f)
	p := f.AddProject("App", "/w/app")

	if got := projectAvailability(t, a, p); got[ProjectArchive] != "" || got[ProjectRestore] != notArchived || got[ProjectRename] != "" || got[ProjectRemove] != "" {
		t.Fatalf("listed %v", got)
	}
	if res, err := a.Act(t.Context(), projectRequest(ProjectArchive, p, `{}`)); err != nil || res.Message != "Archived in T3." {
		t.Fatal(res, err)
	}
	if got := projectAvailability(t, a, p); got[ProjectArchive] != alreadyArchived || got[ProjectRestore] != "" || got[ProjectRename] != "" || got[ProjectRemove] != "" {
		t.Fatalf("archived %v", got)
	}
	if res, err := a.Act(t.Context(), projectRequest(ProjectRestore, p, `{}`)); err != nil || res.Message != "Restored in T3." {
		t.Fatal(res, err)
	}
	if projects, _ := f.ListProjects(t.Context()); projects[0].ArchivedAt != nil {
		t.Fatal("restore left the project archived")
	}
	if len(f.Updated) != 2 || *f.Updated[0].Archived != true || *f.Updated[1].Archived != false || f.Updated[0].Title != "" {
		t.Fatalf("updates %+v", f.Updated)
	}

	// T3 no longer lists it, or cannot list: every project action is
	// unavailable, and the capabilities still answer (launches use them).
	gone := Project{ID: "missing", WorkspaceRoot: "/w/gone"}
	for _, reason := range projectAvailability(t, a, gone) {
		if reason != "T3 no longer lists this project. Refresh workspaces." {
			t.Fatalf("gone reason %q", reason)
		}
	}
	broken := NewAdapter(failingList{f})
	c, err := broken.FeaturesForWorkspace(t.Context(), p.ID, p.WorkspaceRoot)
	if err != nil || !c.Operations[execution.OpLaunch].Available {
		t.Fatalf("list failure broke capabilities: %v", err)
	}
	for _, action := range c.Actions {
		if action.Target == execution.TargetProject && action.Availability.Reason != "Could not read T3's projects. Try again." {
			t.Fatalf("list failure %+v", action)
		}
	}
}

type failingList struct{ *Fake }

func (failingList) ListProjects(context.Context) ([]Project, error) {
	return nil, errors.New("t3 t3_project_list: connection refused")
}

func TestProjectRename(t *testing.T) {
	f := NewFake()
	a := NewAdapter(f)
	p := f.AddProject("App", "/w/app")
	res, err := a.Act(t.Context(), projectRequest(ProjectRename, p, `{"title":"  Billing  "}`))
	if err != nil || res.Message != "Renamed in T3 to Billing." {
		t.Fatal(res, err)
	}
	if projects, _ := f.ListProjects(t.Context()); projects[0].Title != "Billing" || projects[0].ArchivedAt != nil {
		t.Fatalf("project %+v", projects[0])
	}
	if len(f.Updated) != 1 || f.Updated[0].Title != "Billing" || f.Updated[0].Archived != nil {
		t.Fatalf("update %+v", f.Updated)
	}
	_, err = a.Act(t.Context(), projectRequest(ProjectRename, p, `{"title":" "}`))
	wantInvalidAction(t, err, "Enter the project's new title.")
}

func wantInvalidAction(t *testing.T, err error, reason string) {
	t.Helper()
	var inv *execution.Invalid
	if !errors.As(err, &inv) || inv.Reason != reason {
		t.Fatalf("want Invalid %q, got %T %v", reason, err, err)
	}
}

func TestProjectRemove(t *testing.T) {
	f := NewFake()
	a := NewAdapter(f)
	busy := f.AddProject("Busy", "/w/busy")
	if _, err := f.LaunchThread(t.Context(), LaunchInput{ProjectID: busy.ID, Title: "work"}); err != nil {
		t.Fatal(err)
	}
	_, err := a.Act(t.Context(), projectRequest(ProjectRemove, busy, `{}`))
	wantInvalidAction(t, err, notEmptyReason)

	idle := f.AddProject("Idle", "/w/idle")
	res, err := a.Act(t.Context(), projectRequest(ProjectRemove, idle, `{}`))
	if err != nil || res.Message != "Removed from T3. The files stay on disk." {
		t.Fatal(res, err)
	}
	if _, err := a.project(t.Context(), idle.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removed project still listed: %v", err)
	}
	// Already gone: T3 answers not found.
	if _, err := a.Act(t.Context(), projectRequest(ProjectRemove, idle, `{}`)); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("second remove = %v", err)
	}
	// Any other refusal is relayed in T3's words.
	f.DeleteErr = &ToolError{Tool: "t3_project_delete", Code: "invalid_request", Message: `"No project" is the built-in scratch project and cannot be deleted through MCP.`}
	_, err = a.Act(t.Context(), projectRequest(ProjectRemove, busy, `{}`))
	wantInvalidAction(t, err, `"No project" is the built-in scratch project and cannot be deleted through MCP.`)
	for _, id := range f.Deleted {
		if id == "" {
			t.Fatal("empty project id sent")
		}
	}
}

func TestProjectActionLostReplies(t *testing.T) {
	f := NewFake()
	a := NewAdapter(f)
	p := f.AddProject("App", "/w/app")

	// T3 applied the update; its listing shows it.
	f.LoseUpdate = 1
	if res, err := a.Act(t.Context(), projectRequest(ProjectArchive, p, `{}`)); err != nil || res.Message != "Archived in T3." {
		t.Fatal(res, err)
	}
	f.LoseDelete = 1
	if res, err := a.Act(t.Context(), projectRequest(ProjectRemove, p, `{}`)); err != nil || res.Message == "" {
		t.Fatal(res, err)
	}

	// Nothing applied: the listing can't confirm it.
	q := f.AddProject("Other", "/w/other")
	f.UpdateErr = errFakeLostReply
	var uncertain *execution.ActionUncertain
	if _, err := a.Act(t.Context(), projectRequest(ProjectRename, q, `{"title":"New"}`)); !errors.As(err, &uncertain) {
		t.Fatalf("lost rename = %v", err)
	}
	f.DeleteErr = errFakeLostReply
	if _, err := a.Act(t.Context(), projectRequest(ProjectRemove, q, `{}`)); !errors.As(err, &uncertain) {
		t.Fatalf("lost remove = %v", err)
	}
}

func TestProjectActionRefusals(t *testing.T) {
	a := NewAdapter(NewFake())
	if _, err := a.Act(t.Context(), execution.ActionRequest{ID: ProjectRename, Target: execution.TargetProject, Input: []byte(`{}`)}); err == nil {
		t.Fatal("project action without a project accepted")
	}
	missing := Project{ID: "nope", WorkspaceRoot: "/w"}
	if _, err := a.Act(t.Context(), projectRequest(ProjectArchive, missing, `{}`)); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("unknown project = %v", err)
	}
	if _, err := a.Act(t.Context(), projectRequest("explode", missing, `{}`)); err == nil {
		t.Fatal("unknown action accepted")
	}
}

func TestProjectActionsOverMCP(t *testing.T) {
	f := newFakeT3(t)
	var mu sync.Mutex
	calls := map[string][]map[string]any{}
	record := func(name string) func(args map[string]any) (*mcp.CallToolResult, error) {
		return func(args map[string]any) (*mcp.CallToolResult, error) {
			mu.Lock()
			calls[name] = append(calls[name], args)
			mu.Unlock()
			if name == "t3_project_delete" && args["projectId"] == "p-busy" {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{
					Text: `{"_tag":"OrchestratorMcpFailure","code":"invalid_request","message":"The project is not empty; force=true is required to delete it."}`}}}, nil
			}
			return structured(`{"id":"` + args["projectId"].(string) + `","title":"T","workspaceRoot":"/w"}`), nil
		}
	}
	f.tool("t3_project_update", record("t3_project_update"))
	f.tool("t3_project_delete", record("t3_project_delete"))
	a := NewAdapter(newTestClient(t, f, f.adminToken, NewMemoryTokenStore()))
	ctx := ctxT(t)

	p := Project{ID: "p-1", WorkspaceRoot: "/w"}
	for _, r := range []execution.ActionRequest{
		projectRequest(ProjectRename, p, `{"title":"Billing"}`),
		projectRequest(ProjectArchive, p, `{}`),
		projectRequest(ProjectRestore, p, `{}`),
		projectRequest(ProjectRemove, p, `{}`),
	} {
		if _, err := a.Act(ctx, r); err != nil {
			t.Fatalf("%s over MCP: %v", r.ID, err)
		}
	}
	_, err := a.Act(ctx, projectRequest(ProjectRemove, Project{ID: "p-busy", WorkspaceRoot: "/b"}, `{}`))
	wantInvalidAction(t, err, notEmptyReason)

	updates := calls["t3_project_update"]
	if len(updates) != 3 || len(updates[0]) != 2 || updates[0]["projectId"] != "p-1" || updates[0]["title"] != "Billing" ||
		len(updates[1]) != 2 || updates[1]["archived"] != true || updates[2]["archived"] != false {
		t.Fatalf("update args %v", updates)
	}
	for _, d := range calls["t3_project_delete"] {
		if len(d) != 1 || d["force"] != nil {
			t.Fatalf("delete args %v", d)
		}
	}
	if !retrySafe("t3_project_update", map[string]any{"projectId": "p", "archived": true}) ||
		retrySafe("t3_project_update", map[string]any{"projectId": "p", "trackFolders": []string{"/x"}}) ||
		retrySafe("t3_project_delete", map[string]any{"projectId": "p"}) {
		t.Fatal("retry policy")
	}
}
