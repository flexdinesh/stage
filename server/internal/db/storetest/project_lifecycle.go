package storetest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func init() {
	register("ProjectMergeMovesRowsAndAliases", caseProjectMerge)
	register("ProjectMergePreviewChanges", caseProjectMergePreviewChanged)
	register("ProjectSplitMovesOnlyWorkspace", caseProjectSplit)
}

func caseProjectMerge(t *testing.T, open Factory) {
	engine := open(t)
	s, ctx := db.NewStore(engine), context.Background()
	into := mustProject(t, s, ctx, "target")
	from := mustProject(t, s, ctx, "source")
	final := mustProject(t, s, ctx, "final")
	separate := mustProject(t, s, ctx, "separate")
	archived, err := s.UpdateProject(ctx, into.ID, into.Title, true)
	if err != nil {
		t.Fatal(err)
	}
	task, schedule, snippet := createProjectContents(t, s, ctx, from.ID, from.Bindings[0].ID)

	var inactive int64
	if err := engine.Write(ctx, func(tx store.Tx) error {
		pr := tx.Projects()
		var err error
		inactive, err = pr.InsertBinding(ctx, store.ProjectBinding{
			ProjectID: from.ID, ConnectionID: dbtest.T3Connection, ExternalProjectID: "old",
			Workspace: "/w/old", Rule: "manual",
		})
		if err != nil {
			return err
		}
		if err := pr.DeactivateBinding(ctx, inactive); err != nil {
			return err
		}
		return pr.AddProjectEvent(ctx, from.ID, "workspace_moved", "Previously moved.", db.DefaultUser)
	}); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]int64{{final.ID, from.ID}, {from.ID, separate.ID}, {into.ID, separate.ID}, {from.ID, into.ID}} {
		if err := s.DismissProjectMerge(ctx, pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
	}

	preview, err := s.MergeProjects(ctx, into.ID, from.ID, false)
	wantMoves := db.MergeMoves{Tasks: 1, Schedules: 1, Snippets: 1, Workspaces: 1}
	if err != nil || !preview.Preview || preview.Project.ID != into.ID || preview.From.ID != from.ID ||
		preview.Moves != wantMoves || preview.Blocked != nil || preview.PreviewToken == "" {
		t.Fatalf("preview %+v, %v", preview, err)
	}
	merged, err := s.MergeProjects(ctx, into.ID, from.ID, true, preview.PreviewToken)
	if err != nil || merged.Preview || merged.Moves != wantMoves {
		t.Fatalf("merge %+v, %v", merged, err)
	}
	p := merged.Project
	if p.ID != into.ID || p.Title != archived.Title || p.ArchivedAt == nil || !p.ArchivedAt.Equal(*archived.ArchivedAt) {
		t.Fatalf("survivor metadata %+v", p)
	}
	if len(p.Aliases) != 1 || p.Aliases[0].ID != from.ID || p.Aliases[0].Title != from.Title {
		t.Fatalf("aliases %+v", p.Aliases)
	}
	if len(p.Bindings) != 2 || p.Bindings[0].ID != into.Bindings[0].ID || !p.Bindings[0].Primary ||
		p.Bindings[1].ID != from.Bindings[0].ID || p.Bindings[1].Primary {
		t.Fatalf("bindings %+v", p.Bindings)
	}
	assertProjectContents(t, s, ctx, task, schedule, snippet, into.ID, from.Bindings[0].ID)
	assertMergeDismissals(t, engine, ctx, [][2]int64{{into.ID, final.ID}, {into.ID, separate.ID}})
	if stale := assertFreshMergePreview(t, s, ctx, into.ID, from.ID, preview); stale.Blocked == nil || stale.Blocked.Code != "same_project" {
		t.Fatalf("repeated confirmation %+v", stale)
	}

	if _, err := s.MergeProjects(ctx, final.ID, into.ID, true); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetProject(ctx, from.ID)
	if err != nil || got.ID != final.ID || len(got.Aliases) != 2 {
		t.Fatalf("original alias resolves to %+v, %v", got, err)
	}
	for _, alias := range []int64{from.ID, into.ID} {
		if id, err := s.ProjectID(ctx, alias); err != nil || id != final.ID {
			t.Fatalf("canonical #%d = #%d, %v", alias, id, err)
		}
	}
	if err := engine.Read(ctx, func(tx store.Tx) error {
		b, err := tx.Projects().Binding(ctx, inactive)
		if err != nil {
			return err
		}
		if b.ProjectID != final.ID || b.Active {
			t.Fatalf("inactive revision %+v", b)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	assertProjectContents(t, s, ctx, task, schedule, snippet, final.ID, from.Bindings[0].ID)
	assertMergeDismissals(t, engine, ctx, [][2]int64{{final.ID, separate.ID}})
	events, err := s.ProjectEvents(ctx, from.ID)
	if err != nil || len(events) != 3 || events[0].ProjectID != final.ID || events[0].Type != "project_merged" ||
		events[1].Text != fmt.Sprintf("Merged #%d source: 1 tasks, 1 schedules, 1 snippets, 1 workspaces.", from.ID) ||
		events[2].Type != "workspace_moved" || events[2].Text != "Previously moved." {
		t.Fatalf("events %+v, %v", events, err)
	}
}

func caseProjectMergePreviewChanged(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	into := mustProject(t, s, ctx, "target")
	from := mustProject(t, s, ctx, "source")
	task := mustTask(t, s, ctx, from.ID, "first", nil)
	preview, err := s.MergeProjects(ctx, into.ID, from.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.MergeProjects(ctx, into.ID, from.ID, false)
	if err != nil || again.PreviewToken != preview.PreviewToken {
		t.Fatalf("stable preview %+v, %v", again, err)
	}
	if err := s.DeleteTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	task = mustTask(t, s, ctx, from.ID, "second", nil)
	fresh := assertFreshMergePreview(t, s, ctx, into.ID, from.ID, preview)
	if fresh.Moves != preview.Moves || fresh.Blocked != nil {
		t.Fatalf("same count, changed task: %+v", fresh)
	}

	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateLaunching, BeginAttempt: true}); err != nil {
		t.Fatal(err)
	}
	blocked := assertFreshMergePreview(t, s, ctx, into.ID, from.ID, fresh)
	if blocked.Blocked == nil || blocked.Blocked.Code != "task_launching" {
		t.Fatalf("launching preview %+v", blocked)
	}
	_, err = s.MergeProjects(ctx, into.ID, from.ID, true, blocked.PreviewToken)
	assertProjectConflict(t, err, "task_launching")
	if id, err := s.ProjectID(ctx, from.ID); err != nil || id != from.ID {
		t.Fatalf("source after rejected merge = %d, %v", id, err)
	}
	got := getTask(t, s, ctx, task.ID)
	if got.ProjectID != from.ID || got.State != db.StateLaunching {
		t.Fatalf("source task after rejected merge %+v", got)
	}
}

func caseProjectSplit(t *testing.T, open Factory) {
	engine := open(t)
	s, ctx := db.NewStore(engine), context.Background()
	const connection = dbtest.T3Connection
	if _, err := s.RefreshWorkspaces(ctx, dbtest.T3Connection, []execution.WorkspaceInfo{
		{ID: "source", WorkspaceRoot: "/w/source", Title: " Workspace title "},
		{ID: "next", WorkspaceRoot: "/w/next"},
		{ID: "last", WorkspaceRoot: "/w/last"},
	}); err != nil {
		t.Fatal(err)
	}
	src, err := s.InsertProject(ctx, db.Project{Title: "Source", Bindings: []db.ProjectBinding{
		{ConnectionID: connection, ExternalProjectID: "source", Workspace: "/w/source"},
		{ConnectionID: connection, ExternalProjectID: "next", Workspace: "/w/next"},
		{ConnectionID: connection, ExternalProjectID: "last", Workspace: "/w/last"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	binding, next := src.Bindings[0].ID, src.Bindings[1].ID
	task, schedule, snippet := createProjectContents(t, s, ctx, src.ID, binding)
	split, err := s.SplitProject(ctx, src.ID, binding, "  ")
	if err != nil {
		t.Fatal(err)
	}
	dst := split.Project
	if dst.ID == src.ID || dst.Title != "Workspace title" || len(dst.Bindings) != 1 ||
		dst.Bindings[0].ID != binding || !dst.Bindings[0].Primary || dst.Bindings[0].Rule != "manual" {
		t.Fatalf("split workspace %+v", dst)
	}
	kept := split.Source.Bindings
	if split.Source.ID != src.ID || len(kept) != 2 || kept[0].ID != next || !kept[0].Primary || kept[1].Primary {
		t.Fatalf("remaining workspaces %+v", kept)
	}
	assertProjectContents(t, s, ctx, task, schedule, snippet, src.ID, binding)
	_, err = s.TransitionTask(ctx, db.Transition{TaskID: task, To: db.StateLaunching, BeginAttempt: true})
	assertProjectConflict(t, err, "binding_unavailable")
	_, err = s.SplitProject(ctx, dst.ID, binding, "Another project")
	assertProjectConflict(t, err, "split_last_workspace")
	assertMergeDismissals(t, engine, ctx, [][2]int64{{src.ID, dst.ID}})
	for _, id := range []int64{src.ID, dst.ID} {
		events, err := s.ProjectEvents(ctx, id)
		if err != nil || len(events) != 1 || events[0].Type != "project_split" || events[0].ProjectID != id {
			t.Fatalf("split events for project %d: %+v, %v", id, events, err)
		}
	}
}

func createProjectContents(t *testing.T, s *db.Store, ctx context.Context, project, binding int64) (int64, int64, int64) {
	t.Helper()
	task, err := s.CreateTask(ctx, db.NewTask{
		ProjectID: project, ConnectionID: dbtest.T3Connection, BindingID: &binding,
		Title: "Task", Prompt: "Prompt",
	})
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := s.CreateSchedule(ctx, db.NewSchedule{
		ProjectID: project, ConnectionID: dbtest.T3Connection, BindingID: &binding,
		Title: "Schedule", Prompt: "Prompt", Cron: "0 9 * * *", Tz: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	snippet, err := s.CreateSnippet(ctx, db.SnippetPatch{
		Name: db.Optional[string]{Set: true, V: "Snippet"}, Body: db.Optional[string]{Set: true, V: "Prompt"},
		ProjectID: db.Optional[int64]{Set: true, V: project},
	})
	if err != nil {
		t.Fatal(err)
	}
	return task.ID, schedule.ID, snippet.ID
}

func assertProjectContents(t *testing.T, s *db.Store, ctx context.Context, task, schedule, snippet, project, binding int64) {
	t.Helper()
	gotTask := getTask(t, s, ctx, task)
	if gotTask.ProjectID != project || gotTask.BindingID == nil || *gotTask.BindingID != binding {
		t.Fatalf("task destination %+v", gotTask)
	}
	gotSchedule, err := s.GetSchedule(ctx, schedule)
	if err != nil || gotSchedule.ProjectID != project || gotSchedule.BindingID == nil || *gotSchedule.BindingID != binding {
		t.Fatalf("schedule destination %+v, %v", gotSchedule, err)
	}
	gotSnippet, err := s.GetSnippet(ctx, snippet)
	if err != nil || gotSnippet.ProjectID == nil || *gotSnippet.ProjectID != project {
		t.Fatalf("snippet project %+v, %v", gotSnippet, err)
	}
}

func assertMergeDismissals(t *testing.T, e store.Engine, ctx context.Context, want [][2]int64) {
	t.Helper()
	err := e.Read(ctx, func(tx store.Tx) error {
		got, err := tx.Projects().MergeDismissals(ctx)
		if err == nil && !slices.Equal(got, want) {
			t.Fatalf("dismissals %v, want %v", got, want)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertFreshMergePreview(t *testing.T, s *db.Store, ctx context.Context, into, from int64, old db.MergeResult) db.MergeResult {
	t.Helper()
	result, err := s.MergeProjects(ctx, into, from, true, old.PreviewToken)
	var changed *db.PreviewChangedError
	if !errors.As(err, &changed) {
		t.Fatalf("stale confirmation %+v, %v", result, err)
	}
	fresh, ok := changed.Fresh.(db.MergeResult)
	if !ok || !fresh.Preview || fresh.PreviewToken == "" || fresh.PreviewToken == old.PreviewToken {
		t.Fatalf("fresh preview %+v", changed.Fresh)
	}
	return fresh
}

func assertProjectConflict(t *testing.T, err error, code string) {
	t.Helper()
	var c *db.ConflictError
	if !errors.As(err, &c) || c.Code != code {
		t.Fatalf("got %v, want conflict %q", err, code)
	}
}
