package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
	coordinator "github.com/flexdinesh/stage/server/internal/sync"
)

func TestCheckoutRequiresExplicitSharedRoots(t *testing.T) {
	root := t.TempDir()
	if output, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", output, err)
	}
	sub := filepath.Join(root, "app")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	paths := []db.SharedPath{{ConnectionID: 1, HarnessRoot: "/t3", StageRoot: root}, {ConnectionID: 2, HarnessRoot: "/oc", StageRoot: root}}
	a, b := checkout(t.Context(), paths, 1, "/t3/app"), checkout(t.Context(), paths, 2, "/oc/app")
	if a.common == nil || b.common == nil || !os.SameFile(a.common, b.common) || a.subproject != "app" || a.subproject != b.subproject {
		t.Fatal(a, b)
	}
	if checkout(t.Context(), paths, 2, "/oc").subproject == a.subproject {
		t.Fatal("different subproject matched")
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{sub, "/t3/app", "/oc/escape"} {
		if checkout(t.Context(), paths, 2, dir).common != nil {
			t.Errorf("unexpected proof for %s", dir)
		}
	}
	if remoteIdentity("git@host:owner/repo.git") != remoteIdentity("https://host/owner/repo") || remoteIdentity(root) != "" {
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

func TestMatchProposesRemoteAndBindsVerifiedFamily(t *testing.T) {
	var temp dbtest.DB
	store := db.NewStore(temp.Reset(t))
	ctx := t.Context()
	connection, err := store.EnsureOpenCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
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
}
