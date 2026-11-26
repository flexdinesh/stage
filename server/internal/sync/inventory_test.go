package sync_test

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// A workspace under a declared shared path gets its repository metadata
// from the Stage-side checkout; one outside it gets none.
func TestInventoryReadsRepoThroughSharedPaths(t *testing.T) {
	store := db.NewStore(testDB.Reset(t))
	stageRoot := t.TempDir()
	repo := filepath.Join(stageRoot, "app")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "config"), []byte("[remote \"origin\"]\n\turl = https://github.com/o/app.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSharedPath(t.Context(), db.SharedPath{ConnectionID: store.ActiveID(db.LocalMachine, "t3"), HarnessRoot: "/harness", StageRoot: stageRoot}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "packages", "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	fake := t3.NewFake()
	fake.AddProject("app", "/harness/app")
	fake.AddProject("web", "/harness/app/packages/web")
	fake.AddProject("other", "/elsewhere/other")
	svc := t3sync.NewWithAdapters(store, map[int64]execution.Adapter{dbtest.T3Connection: t3.NewAdapter(fake)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws, err := svc.Inventory(t.Context(), store.ActiveID(db.LocalMachine, "t3"))
	if err != nil {
		t.Fatal(err)
	}
	repos := map[string][2]string{}
	for _, w := range ws {
		repos[w.WorkspaceRoot] = [2]string{w.Repo, w.RepoPath}
	}
	if repos["/harness/app"] != [2]string{"github.com/o/app", "."} ||
		repos["/harness/app/packages/web"] != [2]string{"github.com/o/app", "packages/web"} ||
		repos["/elsewhere/other"] != [2]string{} {
		t.Fatalf("repos %v", repos)
	}
}
