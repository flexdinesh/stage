package db

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
)

const oc int64 = 2

func importStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s, ctx, pool := connectionStore(t)
	mustExec(t, pool, `INSERT INTO harness_connections(id,harness) VALUES(2,'opencode')`)
	return s, ctx
}

func ws(id, dir string, opts ...func(*execution.WorkspaceInfo)) execution.WorkspaceInfo {
	w := execution.WorkspaceInfo{ID: id, Title: id, WorkspaceRoot: dir, Importable: true}
	for _, o := range opts {
		o(&w)
	}
	return w
}

func refreshImport(t *testing.T, s *Store, ctx context.Context, conn int64, inv ...execution.WorkspaceInfo) ImportResult {
	t.Helper()
	if _, err := s.RefreshWorkspaces(ctx, conn, inv); err != nil {
		t.Fatal(err)
	}
	r, err := s.ImportWorkspaces(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func byTitle(t *testing.T, s *Store, ctx context.Context, title string) (Project, bool) {
	t.Helper()
	ps, err := s.ListProjects(ctx, "all")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(ps, func(p Project) bool { return p.Title == title })
	if i < 0 {
		return Project{}, false
	}
	return ps[i], true
}

func TestImportCreatesProjectsForImportableWorkspacesOnly(t *testing.T) {
	s, ctx := importStore(t)
	archived := time.Now()
	r := refreshImport(t, s, ctx, oc,
		ws("stage", "/w/stage", func(w *execution.WorkspaceInfo) { w.Repo = "github.com/o/stage" }),
		ws("home", "/home/u", func(w *execution.WorkspaceInfo) { w.Importable = false }),
		ws("old", "/w/old", func(w *execution.WorkspaceInfo) { w.ArchivedAt = &archived }),
	)
	if r.Created != 1 || r.Bound != 0 {
		t.Fatalf("result %+v", r)
	}
	p, ok := byTitle(t, s, ctx, "stage")
	if !ok || p.Origin == nil || *p.Origin != "discovered" || p.ImportedAt == nil || len(p.Bindings) != 1 ||
		p.Bindings[0].Origin != BindingImported || p.Bindings[0].Workspace != "/w/stage" || !slices.Equal(p.Repos, []string{"github.com/o/stage"}) {
		t.Fatalf("project %+v", p)
	}
	for _, title := range []string{"home", "old"} {
		if _, ok := byTitle(t, s, ctx, title); ok {
			t.Errorf("%s imported", title)
		}
	}
	// A repeat pass is a no-op.
	if r = refreshImport(t, s, ctx, oc, ws("stage", "/w/stage")); r != (ImportResult{}) {
		t.Fatalf("repeat %+v", r)
	}
	sums, err := s.ImportSummaries(ctx)
	if err != nil || len(sums) != 1 || sums[0].ConnectionID != oc || sums[0].Harness != "opencode" || sums[0].Count != 1 {
		t.Fatalf("summaries %+v %v", sums, err)
	}
}

func TestImportBindsAcrossHarnessesBySamePath(t *testing.T) {
	s, ctx := importStore(t)
	t3 := s.ActiveID(LocalMachine, "t3")
	existing := projectWithBinding(t, s, t3, "/w/stage")
	r := refreshImport(t, s, ctx, oc, ws("oc-stage", "/w/stage"))
	if r.Created != 0 || r.Bound != 1 {
		t.Fatalf("result %+v", r)
	}
	p, err := s.GetProject(ctx, existing)
	if err != nil || len(p.Bindings) != 2 {
		t.Fatalf("project %+v %v", p, err)
	}
	i := slices.IndexFunc(p.Bindings, func(b ProjectBinding) bool { return b.ConnectionID == oc })
	if i < 0 || p.Bindings[i].Origin != BindingPath || p.Origin != nil {
		t.Fatalf("binding %+v", p.Bindings)
	}
}

func TestImportLeavesAmbiguousAndOccupiedPathsAlone(t *testing.T) {
	s, ctx, pool := connectionStore(t)
	mustExec(t, pool, `INSERT INTO harness_connections(id,harness,active,retired_at) VALUES(3,'opencode',0,stage_now())`)
	mustExec(t, pool, `INSERT INTO harness_connections(id,harness) VALUES(2,'opencode')`)
	// Two projects claim /w/dup: one through T3, one through a replaced
	// OpenCode connection.
	projectWithBinding(t, s, s.ActiveID(LocalMachine, "t3"), "/w/dup")
	other, err := s.InsertProject(ctx, Project{Title: "other"})
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, pool, `INSERT INTO project_bindings(project_id,connection_id,external_project_id,workspace) VALUES(?,3,'x','/w/dup')`, other.ID)
	// The project at /w/busy already has a different OpenCode workspace.
	busy := projectWithBinding(t, s, s.ActiveID(LocalMachine, "t3"), "/w/busy")
	if _, err = s.BindProject(ctx, ProjectBinding{ProjectID: busy, ConnectionID: oc, ExternalProjectID: "elsewhere", Workspace: "/w/elsewhere"}); err != nil {
		t.Fatal(err)
	}
	r := refreshImport(t, s, ctx, oc, ws("dup", "/w/dup"), ws("busy", "/w/busy"), ws("elsewhere", "/w/elsewhere"))
	if r != (ImportResult{}) {
		t.Fatalf("result %+v", r)
	}
	var n int
	if err = pool.QueryRow(`SELECT count(*) FROM projects`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("projects %d %v", n, err)
	}
}

func TestImportPicksMostRecentlyActiveIDPerWorkspace(t *testing.T) {
	s, ctx := importStore(t)
	older, newer := time.Now().Add(-time.Hour), time.Now()
	refreshImport(t, s, ctx, oc,
		ws("a-old", "/w/k8s", func(w *execution.WorkspaceInfo) { w.ActiveAt = &older }),
		ws("b-new", "/w/k8s", func(w *execution.WorkspaceInfo) { w.ActiveAt = &newer }),
	)
	p, ok := byTitle(t, s, ctx, "b-new")
	if !ok || p.Bindings[0].ExternalProjectID != "b-new" {
		t.Fatalf("project %+v", p)
	}
	if _, ok = byTitle(t, s, ctx, "a-old"); ok {
		t.Fatal("duplicate project for one workspace")
	}
}

func TestUnbindDeclinesAndManualBindWithdraws(t *testing.T) {
	s, ctx := importStore(t)
	refreshImport(t, s, ctx, oc, ws("stage", "/w/stage"))
	p, _ := byTitle(t, s, ctx, "stage")
	if _, err := s.UnbindProject(ctx, p.ID, oc); err != nil {
		t.Fatal(err)
	}
	if r := refreshImport(t, s, ctx, oc, ws("stage", "/w/stage")); r != (ImportResult{}) {
		t.Fatalf("declined workspace reimported %+v", r)
	}
	if _, err := s.BindProject(ctx, ProjectBinding{ProjectID: p.ID, ConnectionID: oc, ExternalProjectID: "stage", Workspace: "/w/stage"}); err != nil {
		t.Fatal(err)
	}
	// Rebinding away declines the previous workspace.
	if _, err := s.BindProject(ctx, ProjectBinding{ProjectID: p.ID, ConnectionID: oc, ExternalProjectID: "fork", Workspace: "/w/fork"}); err != nil {
		t.Fatal(err)
	}
	if r := refreshImport(t, s, ctx, oc, ws("stage", "/w/stage"), ws("fork", "/w/fork")); r != (ImportResult{}) {
		t.Fatalf("rebound-away workspace reimported %+v", r)
	}
}

func TestImportFollowsHarnessArchiveUntilUserDecides(t *testing.T) {
	s, ctx, pool := connectionStore(t)
	mustExec(t, pool, `INSERT INTO harness_connections(id,harness) VALUES(2,'opencode')`)
	refreshImport(t, s, ctx, oc, ws("gone", "/w/gone"), ws("busy", "/w/busy"), ws("mine", "/w/mine"))
	busy, _ := byTitle(t, s, ctx, "busy")
	mustExec(t, pool, `INSERT INTO tasks(project_id,title,prompt) VALUES(?,'t','p')`, busy.ID)
	mine, _ := byTitle(t, s, ctx, "mine")
	// The user unarchives then keeps it; harness archive no longer applies.
	if _, err := s.UpdateProject(ctx, mine.ID, "mine", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateProject(ctx, mine.ID, "mine", false); err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	archived := func(w *execution.WorkspaceInfo) { w.ArchivedAt = &at }
	r := refreshImport(t, s, ctx, oc, ws("gone", "/w/gone", archived), ws("busy", "/w/busy", archived), ws("mine", "/w/mine", archived))
	if r.Archived != 1 {
		t.Fatalf("result %+v", r)
	}
	for title, want := range map[string]bool{"gone": true, "busy": false, "mine": false} {
		p, _ := byTitle(t, s, ctx, title)
		if (p.ArchivedAt != nil) != want {
			t.Errorf("%s archived=%v", title, p.ArchivedAt != nil)
		}
	}
	// Live again: the harness-archived project returns.
	r = refreshImport(t, s, ctx, oc, ws("gone", "/w/gone"), ws("busy", "/w/busy"), ws("mine", "/w/mine"))
	if g, _ := byTitle(t, s, ctx, "gone"); r.Unarchived != 1 || g.ArchivedAt != nil {
		t.Fatalf("result %+v %+v", r, g)
	}
	// A user archive sticks and is never reimported.
	g, _ := byTitle(t, s, ctx, "gone")
	if _, err := s.UpdateProject(ctx, g.ID, "gone", true); err != nil {
		t.Fatal(err)
	}
	if r = refreshImport(t, s, ctx, oc, ws("gone", "/w/gone"), ws("busy", "/w/busy"), ws("mine", "/w/mine")); r != (ImportResult{}) {
		t.Fatalf("user archive overridden %+v", r)
	}
	if g, _ = byTitle(t, s, ctx, "gone"); g.ArchivedAt == nil {
		t.Fatal("user archive undone")
	}
}

func TestImportSkipsRetiredConnection(t *testing.T) {
	s, ctx, pool := connectionStore(t)
	mustExec(t, pool, `INSERT INTO harness_connections(id,harness,active,retired_at) VALUES(2,'opencode',0,stage_now())`)
	if r := refreshImport(t, s, ctx, oc, ws("stage", "/w/stage")); r != (ImportResult{}) {
		t.Fatalf("retired connection imported %+v", r)
	}
}
