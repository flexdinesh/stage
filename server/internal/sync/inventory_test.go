package sync_test

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
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
	if err := store.SetSharedPath(t.Context(), db.SharedPath{ConnectionID: store.T3ID(), HarnessRoot: "/harness", StageRoot: stageRoot}); err != nil {
		t.Fatal(err)
	}
	fake := t3.NewFake()
	fake.AddProject("app", "/harness/app")
	fake.AddProject("other", "/elsewhere/other")
	svc := t3sync.New(store, t3.NewAdapter(fake), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws, err := svc.Inventory(t.Context(), store.T3ID())
	if err != nil {
		t.Fatal(err)
	}
	repos := map[string]string{}
	for _, w := range ws {
		repos[w.WorkspaceRoot] = w.Repo
	}
	if repos["/harness/app"] != "github.com/o/app" || repos["/elsewhere/other"] != "" {
		t.Fatalf("repos %v", repos)
	}
}
