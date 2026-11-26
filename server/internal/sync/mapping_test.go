package sync

import (
	"context"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
)

func TestWorkspaceSelection(t *testing.T) {
	branch := "feat-x"
	for _, base := range []string{"origin/main", "main", "feature/x"} {
		s := &Service{}
		w, err := s.workspace(context.Background(), db.Task{Workspace: db.Workspace{Type: "worktree", BaseRef: base, Branch: &branch}}, nil)
		if err != nil || w.Kind != "worktree" || w.Branch != branch || w.FromRemote != (base == "origin/main") {
			t.Fatalf("%+v %v", w, err)
		}
	}
}
