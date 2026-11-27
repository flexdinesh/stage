package storetest

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
)

func init() {
	register("ImportIdentityLadder", caseImportIdentityLadder)
	register("ImportAndPreviewAgree", caseImportAndPreviewAgree)
	register("MoveRebinds", caseMoveRebinds)
	register("MoveAmbiguousReview", caseMoveAmbiguousReview)
	register("DeclineSurvivesNewExternalID", caseDeclineSurvivesNewExternalID)
}

func identityWorkspace(id, path string, activeAt *time.Time) execution.WorkspaceInfo {
	return execution.WorkspaceInfo{
		ID: id, Title: id, WorkspaceRoot: path, Importable: true, ActiveAt: activeAt,
		Repo: "example.test/Org/Repo", RepoPath: ".",
	}
}

func caseImportAndPreviewAgree(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	const connection = dbtest.T3Connection
	now := time.Now()
	first := identityWorkspace("First", "/w/repo", &now)
	second := identityWorkspace("Second", "/w/clone", nil)
	if _, err := s.RefreshWorkspaces(ctx, connection, []execution.WorkspaceInfo{second, first}); err != nil {
		t.Fatal(err)
	}
	preview, err := s.ResolveWorkspaces(ctx, connection, false)
	if err != nil || preview.Applied || preview.PreviewToken == "" || len(preview.Placements) != 2 || preview.Created != 1 || preview.Joined != 1 {
		t.Fatalf("preview %+v: %v", preview, err)
	}
	for i, outcome := range []string{"new", "join"} {
		if p := preview.Placements[i]; string(p.Outcome) != outcome || p.NewProjectTitle != first.Title || !p.Acts {
			t.Fatalf("placement %d %+v", i, p)
		}
	}
	// An identical refresh retains the same semantic inputs.
	if _, err := s.RefreshWorkspaces(ctx, connection, []execution.WorkspaceInfo{first, second}); err != nil {
		t.Fatal(err)
	}
	result, err := s.ResolveWorkspaces(ctx, connection, true, preview.PreviewToken)
	if err != nil || !result.Applied || result.Created != preview.Created || result.Joined != preview.Joined {
		t.Fatalf("apply %+v: %v", result, err)
	}
	project, err := s.BoundProject(ctx, db.ProjectBinding{ConnectionID: connection, ExternalProjectID: first.ID, Workspace: first.WorkspaceRoot})
	if err != nil || len(project.Bindings) != 2 || !project.Bindings[0].Primary || project.Bindings[1].Primary {
		t.Fatalf("project %+v: %v", project, err)
	}
	for _, p := range result.Placements {
		if p.ProjectID == nil || *p.ProjectID != project.ID {
			t.Fatalf("applied owner %+v", p)
		}
	}
}

func caseMoveRebinds(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	const connection = dbtest.T3Connection
	old := identityWorkspace("Old", "/old/repo", nil)
	importAt(t, s, ctx, connection, old)
	project, err := s.BoundProject(ctx, db.ProjectBinding{ConnectionID: connection, ExternalProjectID: old.ID, Workspace: old.WorkspaceRoot})
	if err != nil || len(project.Bindings) != 1 {
		t.Fatalf("project %+v: %v", project, err)
	}
	oldID := project.Bindings[0].ID
	task, err := s.CreateTask(ctx, db.NewTask{ProjectID: project.ID, ConnectionID: connection, BindingID: &oldID, Title: "Task", Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := s.CreateSchedule(ctx, db.NewSchedule{ProjectID: project.ID, ConnectionID: connection, BindingID: &oldID, Title: "Schedule", Prompt: "go", Cron: "*/5 * * * *", Tz: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	next := identityWorkspace("New", "/new/repo", nil)
	next.Importable = false
	if _, err := s.RefreshWorkspaces(ctx, connection, []execution.WorkspaceInfo{next}); err != nil {
		t.Fatal(err)
	}
	preview, err := s.ResolveWorkspaces(ctx, connection, false)
	if err != nil || preview.Moved != 1 || len(preview.Placements) != 1 || preview.Placements[0].Rule != "moved" {
		t.Fatalf("preview %+v: %v", preview, err)
	}
	if result, err := s.ImportWorkspaces(ctx, connection); err != nil || result.Moved != 1 {
		t.Fatalf("import %+v: %v", result, err)
	}
	project, err = s.GetProject(ctx, project.ID)
	if err != nil || len(project.Bindings) != 1 {
		t.Fatalf("project %+v: %v", project, err)
	}
	b := project.Bindings[0]
	if b.ID == oldID || !b.Primary || b.Rule != "moved" || b.Workspace != next.WorkspaceRoot {
		t.Fatalf("binding %+v", b)
	}
	task, err = s.GetTask(ctx, task.ID)
	if err != nil || task.BindingID == nil || *task.BindingID != oldID || task.WorkspaceID != db.WorkspaceID(connection, old.ID, old.WorkspaceRoot) {
		t.Fatalf("task %+v: %v", task, err)
	}
	schedule, err = s.GetSchedule(ctx, schedule.ID)
	if err != nil || schedule.BindingID == nil || *schedule.BindingID != oldID || schedule.WorkspaceID != task.WorkspaceID {
		t.Fatalf("schedule %+v: %v", schedule, err)
	}
	_, err = s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateLaunching, BeginAttempt: true})
	assertProjectConflict(t, err, "binding_unavailable")
	_, err = s.RunScheduleNow(ctx, schedule.ID)
	assertProjectConflict(t, err, "destination_unavailable")
	selector := db.Optional[string]{Set: true, V: db.WorkspaceID(connection, next.ID, next.WorkspaceRoot)}
	if task, err = s.UpdateTask(ctx, task.ID, db.TaskPatch{WorkspaceID: selector}); err != nil || task.WorkspaceID != selector.V || task.BindingID != nil {
		t.Fatalf("repaired task %+v: %v", task, err)
	}
	if schedule, err = s.UpdateSchedule(ctx, schedule.ID, db.SchedulePatch{WorkspaceID: selector}); err != nil || schedule.WorkspaceID != selector.V || schedule.BindingID != nil {
		t.Fatalf("repaired schedule %+v: %v", schedule, err)
	}
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateLaunching, BeginAttempt: true}); err != nil {
		t.Fatal(err)
	}
	if dest, err := s.LaunchDestination(ctx, task.ID, 1); err != nil || dest.BindingID != b.ID || dest.Workspace != next.WorkspaceRoot {
		t.Fatalf("repaired launch %+v: %v", dest, err)
	}
	if spawned, err := s.RunScheduleNow(ctx, schedule.ID); err != nil || spawned.WorkspaceID != selector.V {
		t.Fatalf("repaired schedule spawn %+v: %v", spawned, err)
	}
	events, err := s.ProjectEvents(ctx, project.ID)
	if err != nil || len(events) != 1 || events[0].Type != "workspace_moved" {
		t.Fatalf("events %+v: %v", events, err)
	}
}

func caseMoveAmbiguousReview(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	const connection = dbtest.T3Connection
	importAt(t, s, ctx, connection, identityWorkspace("old", "/w/old", nil))
	importAt(t, s, ctx, connection, identityWorkspace("nextA", "/w/a", nil), identityWorkspace("nextB", "/w/b", nil))
	review, err := s.ReviewWorkspaces(ctx)
	if err != nil || review.Counts.Moved != 2 || review.Attention != 2 || len(review.Workspaces) != 2 {
		t.Fatalf("review %+v: %v", review, err)
	}
	projects, err := s.ListProjects(ctx, "all")
	if err != nil || len(projects) != 1 || len(projects[0].Bindings) != 1 || projects[0].Bindings[0].Status != "missing" {
		t.Fatalf("projects %+v: %v", projects, err)
	}
}

func caseDeclineSurvivesNewExternalID(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	const connection = dbtest.T3Connection
	first := identityWorkspace("first", "/w/repo", nil)
	importAt(t, s, ctx, connection, first)
	project, err := s.BoundProject(ctx, db.ProjectBinding{ConnectionID: connection, ExternalProjectID: first.ID, Workspace: first.WorkspaceRoot})
	if err != nil || len(project.Bindings) != 1 {
		t.Fatalf("project %+v: %v", project, err)
	}
	if _, err := s.UnbindProject(ctx, project.ID, project.Bindings[0].ID); err != nil {
		t.Fatal(err)
	}
	rediscovered := identityWorkspace("changed-id", "/w/clone", nil)
	importAt(t, s, ctx, connection, rediscovered)
	ws, err := s.Workspaces(ctx, connection)
	if err != nil || len(ws) != 1 || !ws[0].Declined || ws[0].ProjectID != nil || ws[0].Resolution.Reason != "declined" {
		t.Fatalf("workspaces %+v: %v", ws, err)
	}
}

// importAt refreshes connection's discovery and imports it.
func importAt(t *testing.T, s *db.Store, ctx context.Context, connection int64, ws ...execution.WorkspaceInfo) {
	t.Helper()
	if _, err := s.RefreshWorkspaces(ctx, connection, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportWorkspaces(ctx, connection); err != nil {
		t.Fatal(err)
	}
}

// The same remote and subpath join across connections and machines.
func caseImportIdentityLadder(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	claude := harness.Descriptor{Kind: "claude", Sessions: harness.SessionsMachine, LocalProcesses: true}
	oc, err := s.SwitchInstance(ctx, db.LocalMachine, harness.Descriptor{Kind: "opencode"}, "instance", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	local, err := s.SwitchInstance(ctx, db.LocalMachine, claude, "home-a", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	laptop := pairMachine(t, s, ctx, "laptop")
	away, err := s.SwitchInstance(ctx, laptop.MachineID, claude, "home-b", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	ws := func(id, dir, repo string) execution.WorkspaceInfo {
		return execution.WorkspaceInfo{ID: id, Title: id, WorkspaceRoot: dir, Importable: true, Repo: repo, RepoPath: "."}
	}
	importAt(t, s, ctx, oc.New, ws("app", "/w/app", "github.com/o/app"))
	importAt(t, s, ctx, local.New, ws("/w/app", "/w/app", "github.com/o/app"))
	importAt(t, s, ctx, away.New, ws("/l/app", "/l/app", "github.com/o/app"), ws("/l/other", "/l/other", "github.com/o/other"))
	ps, err := s.ListProjects(ctx, "all")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(ps, func(p db.Project) bool { return p.Title == "app" })
	if len(ps) != 2 || i < 0 {
		t.Fatalf("projects %+v", ps)
	}
	rules := map[int64]string{}
	for _, b := range ps[i].Bindings {
		rules[b.ConnectionID] = b.Rule
	}
	if rules[oc.New] != "new" || rules[local.New] != "remote" || rules[away.New] != "remote" {
		t.Fatalf("bindings %+v", ps[i].Bindings)
	}
}
