package db

import (
	"context"
	"slices"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db/sqlite"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Two machines: the local one (T3 1, OpenCode 2) and a laptop (T3 3,
// OpenCode 4); a third machine (T3 5) where a test needs one.
const (
	laptopMachine int64 = 2
	localOC       int64 = 2
	laptopT3      int64 = 3
	laptopOC      int64 = 4
	thirdT3       int64 = 5
)

func twoMachineStore(t *testing.T) (*Store, context.Context, *sqlite.DB) {
	t.Helper()
	s, ctx, pool := connectionStore(t)
	mustExec(t, pool, `INSERT INTO machines(id,label) VALUES(2,'Laptop'),(3,'Desktop')`)
	mustExec(t, pool, `INSERT INTO harness_connections(id,machine_id,harness) VALUES(2,1,'opencode'),(3,2,'t3'),(4,2,'opencode'),(5,3,'t3')`)
	return s, ctx, pool
}

func remote(repo, path string) func(*execution.WorkspaceInfo) {
	return func(w *execution.WorkspaceInfo) { w.Repo, w.RepoPath = repo, path }
}

// projectCount measures legacy imports, independent of catalog groups.
func projectCount(t *testing.T, pool *sqlite.DB) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(`SELECT count(*) FROM projects WHERE NOT catalog_group`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func bindingOn(t *testing.T, s *Store, ctx context.Context, project, connection int64) (ProjectBinding, bool) {
	t.Helper()
	p, err := s.GetProject(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(p.Bindings, func(b ProjectBinding) bool { return b.ConnectionID == connection })
	if i < 0 {
		return ProjectBinding{}, false
	}
	return p.Bindings[i], true
}

func TestImportJoinsSameRepoAtAnotherPathOnAnotherMachine(t *testing.T) {
	s, ctx, pool := twoMachineStore(t)
	local := s.ActiveID(LocalMachine, "t3")
	refreshImport(t, s, ctx, local, ws("stage", "/home/a/stage", remote("github.com/o/stage", ".")))
	p, ok := byTitle(t, s, ctx, "stage")
	if !ok {
		t.Fatal("local import")
	}
	r := refreshImport(t, s, ctx, laptopT3, ws("laptop-stage", "/Users/b/code/stage", remote("github.com/o/stage", ".")))
	if r.Created != 0 || r.Bound != 1 || projectCount(t, pool) != 1 {
		t.Fatalf("result %+v, %d projects", r, projectCount(t, pool))
	}
	b, ok := bindingOn(t, s, ctx, p.ID, laptopT3)
	if !ok || b.Rule != "remote" || b.Workspace != "/Users/b/code/stage" || b.ExternalProjectID != "laptop-stage" {
		t.Fatalf("laptop binding %+v", b)
	}
	// The local service reads the same remote and subpath on its file host.
	if r = refreshImport(t, s, ctx, laptopOC, ws("oc-stage", "/Users/b/code/stage", remote("github.com/o/stage", "."))); r.Bound != 1 {
		t.Fatalf("laptop OpenCode %+v", r)
	}
	if b, ok = bindingOn(t, s, ctx, p.ID, laptopOC); !ok || b.Rule != "remote" {
		t.Fatalf("laptop OpenCode binding %+v", b)
	}
	// A third machine matches the one project through two machines' bindings.
	if r = refreshImport(t, s, ctx, thirdT3, ws("desk", "/srv/stage", remote("github.com/o/stage", "."))); r.Bound != 1 {
		t.Fatalf("third machine %+v", r)
	}
	if b, ok = bindingOn(t, s, ctx, p.ID, thirdT3); !ok || b.Rule != "remote" {
		t.Fatalf("third machine binding %+v", b)
	}
	// Repeat passes are no-ops.
	if r = refreshImport(t, s, ctx, laptopT3, ws("laptop-stage", "/Users/b/code/stage", remote("github.com/o/stage", "."))); r != (ImportResult{}) {
		t.Fatalf("repeat %+v", r)
	}
}

// A path names a project only within one machine; the same path elsewhere
// is a different checkout unless its remote and subproject match.
func TestImportSamePathOnAnotherMachineNeverMerges(t *testing.T) {
	s, ctx, pool := twoMachineStore(t)
	local := s.ActiveID(LocalMachine, "t3")
	refreshImport(t, s, ctx, local,
		ws("plain", "/w/plain", directoryEvidence),
		ws("app", "/w/app", remote("github.com/o/app", ".")),
	)
	r := refreshImport(t, s, ctx, laptopT3,
		ws("plain-l", "/w/plain", directoryEvidence),
		ws("app-l", "/w/app", remote("github.com/o/other", ".")),
	)
	if r.Created != 2 || r.Bound != 0 || projectCount(t, pool) != 4 {
		t.Fatalf("result %+v, %d projects", r, projectCount(t, pool))
	}
}

func TestImportForkIsAnotherProject(t *testing.T) {
	s, ctx, pool := twoMachineStore(t)
	refreshImport(t, s, ctx, s.ActiveID(LocalMachine, "t3"), ws("upstream", "/w/stage", remote("github.com/o/stage", ".")))
	r := refreshImport(t, s, ctx, laptopT3, ws("fork", "/code/stage", remote("github.com/me/stage", ".")))
	if r.Created != 1 || r.Bound != 0 || projectCount(t, pool) != 2 {
		t.Fatalf("result %+v, %d projects", r, projectCount(t, pool))
	}
}

func TestImportMatchesMonorepoSubprojects(t *testing.T) {
	s, ctx, pool := twoMachineStore(t)
	const mono = "github.com/o/mono"
	refreshImport(t, s, ctx, s.ActiveID(LocalMachine, "t3"),
		ws("a", "/w/mono/packages/a", remote(mono, "packages/a")),
		ws("b", "/w/mono/packages/b", remote(mono, "packages/b")),
		ws("root", "/w/mono", remote(mono, ".")),
	)
	r := refreshImport(t, s, ctx, laptopT3,
		ws("a-l", "/src/mono/packages/a", remote(mono, "packages/a")),
		ws("root-l", "/src/mono", remote(mono, ".")),
		ws("c-l", "/src/mono/packages/c", remote(mono, "packages/c")),
	)
	if r.Created != 1 || r.Bound != 2 || projectCount(t, pool) != 4 {
		t.Fatalf("result %+v, %d projects", r, projectCount(t, pool))
	}
	for title, want := range map[string]string{"a": "/src/mono/packages/a", "root": "/src/mono"} {
		p, _ := byTitle(t, s, ctx, title)
		if b, ok := bindingOn(t, s, ctx, p.ID, laptopT3); !ok || b.Workspace != want || b.Rule != "remote" {
			t.Errorf("%s laptop binding %+v", title, b)
		}
	}
	if b, _ := byTitle(t, s, ctx, "b"); len(b.Bindings) != 1 {
		t.Errorf("b bindings %+v", b.Bindings)
	}
	if _, ok := byTitle(t, s, ctx, "c-l"); !ok {
		t.Error("unmatched subproject not created")
	}
}

func TestImportWithoutRemoteOrSubprojectNeverMergesAcrossMachines(t *testing.T) {
	s, ctx, pool := twoMachineStore(t)
	refreshImport(t, s, ctx, s.ActiveID(LocalMachine, "t3"), ws("app", "/w/app", remote("github.com/o/app", ".")))
	r := refreshImport(t, s, ctx, laptopT3,
		ws("no-remote", "/l/app"),
		// A remote with an unknown subproject (e.g. before the first
		// refresh after upgrade) is no match key.
		ws("no-path", "/l/app2", remote("github.com/o/app", "")),
	)
	if r.Created != 2 || r.Bound != 0 || projectCount(t, pool) != 3 {
		t.Fatalf("result %+v, %d projects", r, projectCount(t, pool))
	}
}

// Two manually separated projects with one key make a later match ambiguous.
func TestImportLeavesAmbiguousRemoteMatchForManualBinding(t *testing.T) {
	s, ctx, pool := twoMachineStore(t)
	const key = "github.com/o/stage"
	connection := s.ActiveID(LocalMachine, "t3")
	for _, one := range []struct{ id, path string }{{"one", "/a/stage"}, {"two", "/b/stage"}} {
		_, err := s.InsertProject(ctx, Project{Title: one.id, Bindings: []ProjectBinding{
			{ConnectionID: connection, ExternalProjectID: one.id, Workspace: one.path},
		}})
		if err != nil {
			t.Fatal(err)
		}
	}
	refreshImport(t, s, ctx, connection, ws("one", "/a/stage", remote(key, ".")), ws("two", "/b/stage", remote(key, ".")))
	if r := refreshImport(t, s, ctx, laptopT3, ws("l", "/l/stage", remote(key, "."))); r != (ImportResult{}) || projectCount(t, pool) != 2 {
		t.Fatalf("ambiguous %+v, %d projects", r, projectCount(t, pool))
	}
	// A manual bind still works.
	one, _ := byTitle(t, s, ctx, "one")
	if _, err := s.BindProject(ctx, ProjectBinding{ProjectID: one.ID, ConnectionID: laptopT3, ExternalProjectID: "l", Workspace: "/l/stage"}); err != nil {
		t.Fatal(err)
	}
	if b, ok := bindingOn(t, s, ctx, one.ID, laptopT3); !ok || b.Rule != "manual" {
		t.Fatalf("manual binding %+v", b)
	}
}

func TestImportJoinsRemoteOwnerPlacedOnThisMachine(t *testing.T) {
	s, ctx, pool := twoMachineStore(t)
	const key = "github.com/o/stage"
	refreshImport(t, s, ctx, laptopT3, ws("l", "/l/stage", remote(key, ".")))
	if r := refreshImport(t, s, ctx, s.ActiveID(LocalMachine, "t3"), ws("a", "/a/stage", remote(key, "."))); r.Bound != 1 {
		t.Fatalf("local join %+v", r)
	}
	if r := refreshImport(t, s, ctx, localOC, ws("c", "/c/stage", remote(key, "."))); r.Bound != 1 || projectCount(t, pool) != 1 {
		t.Fatalf("second local checkout %+v, %d projects", r, projectCount(t, pool))
	}
}

func TestImportRemoteMatchHonorsDeclines(t *testing.T) {
	s, ctx, _ := twoMachineStore(t)
	const key = "github.com/o/stage"
	refreshImport(t, s, ctx, s.ActiveID(LocalMachine, "t3"), ws("stage", "/a/stage", remote(key, ".")))
	p, _ := byTitle(t, s, ctx, "stage")
	laptop := ws("l", "/l/stage", remote(key, "."))
	if r := refreshImport(t, s, ctx, laptopT3, laptop); r.Bound != 1 {
		t.Fatalf("join %+v", r)
	}
	bound, ok := bindingOn(t, s, ctx, p.ID, laptopT3)
	if !ok {
		t.Fatal("expected laptop binding")
	}
	if _, err := s.UnbindProject(ctx, p.ID, bound.ID); err != nil {
		t.Fatal(err)
	}
	rediscovered := ws("new-laptop-id", "/l/stage", remote(key, "."))
	if r := refreshImport(t, s, ctx, laptopT3, rediscovered); r != (ImportResult{}) {
		t.Fatalf("declined workspace rejoined %+v", r)
	}
	// Restore the original row before binding it manually.
	refreshImport(t, s, ctx, laptopT3, laptop)
	if _, err := s.BindProject(ctx, ProjectBinding{ProjectID: p.ID, ConnectionID: laptopT3, ExternalProjectID: "l", Workspace: "/l/stage"}); err != nil {
		t.Fatal(err)
	}
	if b, ok := bindingOn(t, s, ctx, p.ID, laptopT3); !ok || b.Rule != "manual" {
		t.Fatalf("manual rebind %+v", b)
	}
	if s.ConnectionMachine(laptopT3) != laptopMachine {
		t.Fatal("laptop connection machine")
	}
}
