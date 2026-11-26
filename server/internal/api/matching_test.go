package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/opencode"
	coordinator "github.com/flexdinesh/stage/server/internal/sync"
)

func TestRemoteIdentityNormalizes(t *testing.T) {
	if remoteIdentity("git@host:owner/repo.git") != remoteIdentity("https://host/owner/repo") || remoteIdentity(t.TempDir()) != "" {
		t.Fatal("remote normalization")
	}
}

type workspaceInventory struct {
	execution.Adapter
	rows []execution.WorkspaceInfo
}

func (i workspaceInventory) Workspaces(context.Context) ([]execution.WorkspaceInfo, error) {
	return i.rows, nil
}

func matchConnection(t *testing.T, store *db.Store, machine int64) int64 {
	t.Helper()
	sw, err := store.SwitchInstance(t.Context(), machine, opencode.Descriptor, "matching-target", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	return sw.New
}

func TestMatchProposesRemoteAndBindsVerifiedFamily(t *testing.T) {
	var temp dbtest.DB
	store := db.NewStore(temp.Reset(t))
	ctx := t.Context()
	connection := matchConnection(t, store, db.LocalMachine)
	source := execution.WorkspaceInfo{ID: "t3-project", WorkspaceRoot: "/t3/repo", OriginRemote: "git@host:owner/repo.git"}
	target := execution.WorkspaceInfo{ID: "oc-project", WorkspaceRoot: "/oc/repo", OriginRemote: "https://host/owner/repo"}
	p, err := store.InsertProject(ctx, db.Project{Title: "Matched", Bindings: []db.ProjectBinding{{ConnectionID: 1, ExternalProjectID: source.ID, Workspace: source.WorkspaceRoot}}})
	if err != nil {
		t.Fatal(err)
	}
	service := &server{Deps: Deps{Store: store, Sync: coordinator.NewWithAdapters(store, map[int64]execution.Adapter{1: workspaceInventory{rows: []execution.WorkspaceInfo{source}}, connection: workspaceInventory{rows: []execution.WorkspaceInfo{target}}}, nil)}}
	req := httptest.NewRequest("POST", "/", nil)
	req.SetPathValue("id", strconv.FormatInt(p.ID, 10))
	req.SetPathValue("connectionId", strconv.FormatInt(connection, 10))
	check := func(wantBound bool) {
		t.Helper()
		rec := httptest.NewRecorder()
		if err := service.matchWorkspace(rec, req); err != nil {
			t.Fatal(err)
		}
		var out struct {
			Bound       *db.Project
			Suggestions map[string]string
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if (out.Bound != nil) != wantBound || out.Suggestions[target.ID] == "" {
			t.Fatalf("match: %+v", out)
		}
	}
	check(false)
	root := t.TempDir()
	if output, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", output, err)
	}
	for _, m := range []db.SharedPath{{ConnectionID: 1, HarnessRoot: source.WorkspaceRoot, StageRoot: root}, {ConnectionID: connection, HarnessRoot: target.WorkspaceRoot, StageRoot: root}} {
		if err := store.SetSharedPath(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	check(true)
	if err := service.chooseConnection(req, &taskConfigReq{ProjectID: &p.ID}); err == nil {
		t.Fatal("dual-bound project accepted an implicit destination")
	}
	// With a default connection, a dual-bound project starts there.
	if _, err := store.SetDefaultConnection(ctx, &connection); err != nil {
		t.Fatal(err)
	}
	implicit := taskConfigReq{ProjectID: &p.ID}
	if err := service.chooseConnection(req, &implicit); err != nil || implicit.ConnectionID == nil || *implicit.ConnectionID != connection {
		t.Fatalf("default destination %v, %v", implicit.ConnectionID, err)
	}
	one := int64(1)
	explicit := taskConfigReq{ProjectID: &p.ID, ConnectionID: &one}
	if err := service.chooseConnection(req, &explicit); err != nil || *explicit.ConnectionID != 1 {
		t.Fatalf("explicit destination %v, %v", explicit.ConnectionID, err)
	}
}

// Checkout proof holds within one machine: the same directory seen through
// shared paths on another machine's connection only suggests by remote.
func TestMatchNeverProvesACheckoutAcrossMachines(t *testing.T) {
	var temp dbtest.DB
	pool := temp.Reset(t)
	store := db.NewStore(pool)
	ctx := t.Context()
	if _, err := pool.ExecContext(ctx, `INSERT INTO machines(id,label) VALUES(2,'Laptop')`); err != nil {
		t.Fatal(err)
	}
	connection := matchConnection(t, store, 2)
	source := execution.WorkspaceInfo{ID: "t3-project", WorkspaceRoot: "/t3/repo", OriginRemote: "git@host:owner/repo.git"}
	target := execution.WorkspaceInfo{ID: "oc-project", WorkspaceRoot: "/oc/repo", OriginRemote: "https://host/owner/repo"}
	p, err := store.InsertProject(ctx, db.Project{Title: "Matched", Bindings: []db.ProjectBinding{{ConnectionID: 1, ExternalProjectID: source.ID, Workspace: source.WorkspaceRoot}}})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if output, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", output, err)
	}
	for _, m := range []db.SharedPath{{ConnectionID: 1, HarnessRoot: source.WorkspaceRoot, StageRoot: root}, {ConnectionID: connection, HarnessRoot: target.WorkspaceRoot, StageRoot: root}} {
		if err := store.SetSharedPath(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	service := &server{Deps: Deps{Store: store, Sync: coordinator.NewWithAdapters(store, map[int64]execution.Adapter{1: workspaceInventory{rows: []execution.WorkspaceInfo{source}}, connection: workspaceInventory{rows: []execution.WorkspaceInfo{target}}}, nil)}}
	req := httptest.NewRequest("POST", "/", nil)
	req.SetPathValue("id", strconv.FormatInt(p.ID, 10))
	req.SetPathValue("connectionId", strconv.FormatInt(connection, 10))
	rec := httptest.NewRecorder()
	if err := service.matchWorkspace(rec, req); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Bound       *db.Project
		Suggestions map[string]string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Bound != nil || out.Suggestions[target.ID] != "Same Git remote. Confirm the checkout." {
		t.Fatalf("cross-machine match: %+v", out)
	}
}

// Across machines the same remote and subproject is the project's only
// match, so it binds (ADR 0007 decision 11); an unknown subproject only
// suggests, and a monorepo sibling is never proposed.
func TestMatchRemoteAcrossMachinesRequiresSameSubproject(t *testing.T) {
	var temp dbtest.DB
	pool := temp.Reset(t)
	store := db.NewStore(pool)
	ctx := t.Context()
	if _, err := pool.ExecContext(ctx, `INSERT INTO machines(id,label) VALUES(2,'Laptop')`); err != nil {
		t.Fatal(err)
	}
	connection := matchConnection(t, store, 2)
	mono := func(id, dir, path string) execution.WorkspaceInfo {
		return execution.WorkspaceInfo{ID: id, WorkspaceRoot: dir, OriginRemote: "git@host:o/mono.git", Repo: "host/o/mono", RepoPath: path}
	}
	source := mono("t3-a", "/w/mono/packages/a", "packages/a")
	p, err := store.InsertProject(ctx, db.Project{Title: "A", Bindings: []db.ProjectBinding{{ConnectionID: 1, ExternalProjectID: source.ID, Workspace: source.WorkspaceRoot}}})
	if err != nil {
		t.Fatal(err)
	}
	same, sibling, unknown := mono("oc-a", "/l/mono/packages/a", "packages/a"), mono("oc-b", "/l/mono/packages/b", "packages/b"), mono("oc-x", "/l/x", "")
	service := &server{Deps: Deps{Store: store, Sync: coordinator.NewWithAdapters(store, map[int64]execution.Adapter{
		1:          workspaceInventory{rows: []execution.WorkspaceInfo{source}},
		connection: workspaceInventory{rows: []execution.WorkspaceInfo{same, sibling, unknown}},
	}, nil)}}
	req := httptest.NewRequest("POST", "/", nil)
	req.SetPathValue("id", strconv.FormatInt(p.ID, 10))
	req.SetPathValue("connectionId", strconv.FormatInt(connection, 10))
	rec := httptest.NewRecorder()
	if err := service.matchWorkspace(rec, req); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Bound       *db.Project
		Suggestions map[string]string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Bound == nil || out.Suggestions[same.ID] != "Same Git remote and subproject on another machine" || out.Suggestions[sibling.ID] != "" || out.Suggestions[unknown.ID] != "Same Git remote. Confirm the checkout." {
		t.Fatalf("suggestions %+v", out)
	}
	if i := slices.IndexFunc(out.Bound.Bindings, func(b db.ProjectBinding) bool { return b.ConnectionID == connection }); i < 0 || out.Bound.Bindings[i].ExternalProjectID != same.ID {
		t.Fatalf("bound %+v", out.Bound.Bindings)
	}
}

// Workspaces local to the machine (hosted harnesses, a local service) need
// no declared shared path: the machine's ops read them in place. The same
// path, or another worktree of the same checkout and subproject, binds.
func TestMatchLocalWorkspacesByPathOrCheckout(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo, worktree := filepath.Join(root, "app"), filepath.Join(root, "app-wt")
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
	}
	git("init", "-q", repo)
	git("-C", repo, "-c", "user.name=s", "-c", "user.email=s@example.invalid", "commit", "-q", "--allow-empty", "-m", "init")
	git("-C", repo, "worktree", "add", "-q", worktree)
	for _, tc := range []struct{ dir, reason string }{
		{repo, "Same path on this machine"},
		{worktree, "Shared Git checkout and subproject"},
	} {
		t.Run(filepath.Base(tc.dir), func(t *testing.T) {
			var temp dbtest.DB
			pool := temp.Reset(t)
			store := db.NewStore(pool)
			ctx := t.Context()
			var owner int64
			if err := pool.QueryRowContext(ctx, "INSERT INTO harness_connections(harness,machine_id) VALUES('claude',1) RETURNING id").Scan(&owner); err != nil {
				t.Fatal(err)
			}
			connection := matchConnection(t, store, db.LocalMachine)
			source := execution.WorkspaceInfo{ID: repo, WorkspaceRoot: repo, Local: true}
			target := execution.WorkspaceInfo{ID: "oc", WorkspaceRoot: tc.dir, Local: true}
			p, err := store.InsertProject(ctx, db.Project{Title: "App", Bindings: []db.ProjectBinding{{ConnectionID: owner, ExternalProjectID: source.ID, Workspace: source.WorkspaceRoot}}})
			if err != nil {
				t.Fatal(err)
			}
			service := &server{Deps: Deps{Store: store, Sync: coordinator.NewWithAdapters(store, map[int64]execution.Adapter{owner: workspaceInventory{rows: []execution.WorkspaceInfo{source}}, connection: workspaceInventory{rows: []execution.WorkspaceInfo{target}}}, nil)}}
			req := httptest.NewRequest("POST", "/", nil)
			req.SetPathValue("id", strconv.FormatInt(p.ID, 10))
			req.SetPathValue("connectionId", strconv.FormatInt(connection, 10))
			rec := httptest.NewRecorder()
			if err := service.matchWorkspace(rec, req); err != nil {
				t.Fatal(err)
			}
			var out struct {
				Bound       *db.Project
				Suggestions map[string]string
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if out.Bound == nil || out.Suggestions[target.ID] != tc.reason {
				t.Fatalf("match %+v", out)
			}
		})
	}
}
