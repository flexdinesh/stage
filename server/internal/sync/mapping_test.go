package sync

import (
	"testing"

	"github.com/flexdinesh/t3-stage/server/internal/db"
	"github.com/flexdinesh/t3-stage/server/internal/t3"
)

func TestMapThreadState(t *testing.T) {
	tests := []struct {
		name      string
		current   db.TaskState
		status    t3.ThreadStatus
		settled   bool
		pending   int
		latestRun t3.ThreadStatus
		want      db.TaskState
	}{
		{"settled wins over everything", db.StateCompleted, t3.StatusRunning, true, 2, t3.StatusRunning, db.StateSettled},
		{"settled while idle", db.StateCompleted, t3.StatusIdle, true, 0, "", db.StateSettled},
		{"pending requests -> needs_input", db.StateRunning, t3.StatusRunning, false, 1, t3.StatusRunning, db.StateNeedsInput},
		{"waiting -> needs_input", db.StateRunning, t3.StatusWaiting, false, 0, t3.StatusWaiting, db.StateNeedsInput},
		{"pending beats completed", db.StateRunning, t3.StatusCompleted, false, 1, t3.StatusCompleted, db.StateNeedsInput},
		{"preparing -> running", db.StateLaunching, t3.StatusPreparing, false, 0, "", db.StateRunning},
		{"queued -> running", db.StateLaunching, t3.StatusQueued, false, 0, "", db.StateRunning},
		{"starting -> running", db.StateLaunching, t3.StatusStarting, false, 0, "", db.StateRunning},
		{"running -> running", db.StateNeedsInput, t3.StatusRunning, false, 0, "", db.StateRunning},
		{"completed", db.StateRunning, t3.StatusCompleted, false, 0, "", db.StateCompleted},
		{"failed", db.StateRunning, t3.StatusFailed, false, 0, "", db.StateFailed},
		{"interrupted", db.StateRunning, t3.StatusInterrupted, false, 0, "", db.StateInterrupted},
		{"cancelled", db.StateRunning, t3.StatusCancelled, false, 0, "", db.StateCancelled},
		{"rolled_back -> cancelled", db.StateRunning, t3.StatusRolledBack, false, 0, "", db.StateCancelled},
		{"idle with latest run completed -> completed", db.StateRunning, t3.StatusIdle, false, 0, t3.StatusCompleted, db.StateCompleted},
		{"idle with latest run running -> running", db.StateLaunching, t3.StatusIdle, false, 0, t3.StatusRunning, db.StateRunning},
		{"idle with latest run failed -> failed", db.StateRunning, t3.StatusIdle, false, 0, t3.StatusFailed, db.StateFailed},
		{"idle with latest run interrupted", db.StateRunning, t3.StatusIdle, false, 0, t3.StatusInterrupted, db.StateInterrupted},
		{"idle with latest run rolled_back", db.StateRunning, t3.StatusIdle, false, 0, t3.StatusRolledBack, db.StateCancelled},
		{"idle no runs keeps state", db.StateRunning, t3.StatusIdle, false, 0, "", db.StateRunning},
		{"idle no runs keeps completed", db.StateCompleted, t3.StatusIdle, false, 0, "", db.StateCompleted},
		{"idle with latest run waiting keeps state", db.StateRunning, t3.StatusIdle, false, 0, t3.StatusWaiting, db.StateRunning},
		{"unknown status keeps state", db.StateRunning, t3.ThreadStatus("weird"), false, 0, t3.StatusCompleted, db.StateRunning},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MapThreadState(tt.current, tt.status, tt.settled, tt.pending, tt.latestRun)
			if got != tt.want {
				t.Fatalf("MapThreadState(%s, %s, settled=%v, pending=%d, latest=%q) = %s, want %s",
					tt.current, tt.status, tt.settled, tt.pending, tt.latestRun, got, tt.want)
			}
		})
	}
}

func TestMapDetailUsesNewestRun(t *testing.T) {
	d := t3.ThreadDetail{
		Thread:     t3.Thread{Status: t3.StatusIdle},
		RecentRuns: []t3.Run{{Status: t3.StatusCompleted}, {Status: t3.StatusFailed}},
	}
	if got := mapDetail(db.StateRunning, d); got != db.StateCompleted {
		t.Fatalf("got %s, want completed", got)
	}
}

func TestWorkspaceStrategy(t *testing.T) {
	branch := "feat-x"
	tests := []struct {
		name       string
		in         db.Workspace
		wantType   t3.WorkspaceType
		wantOrigin *bool
		wantBranch string
	}{
		{"root", db.Workspace{Type: "root"}, t3.WorkspaceRoot, nil, ""},
		{"worktree origin", db.Workspace{Type: "worktree", BaseRef: "origin/main", Branch: &branch}, t3.WorkspaceWorktree, ptr(true), "feat-x"},
		{"worktree local", db.Workspace{Type: "worktree", BaseRef: "main"}, t3.WorkspaceWorktree, ptr(false), ""},
		{"worktree local slash branch", db.Workspace{Type: "worktree", BaseRef: "feature/x"}, t3.WorkspaceWorktree, ptr(false), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WorkspaceStrategy(tt.in)
			if got.Type != tt.wantType || got.Branch != tt.wantBranch {
				t.Fatalf("got %+v", got)
			}
			if (got.StartFromOrigin == nil) != (tt.wantOrigin == nil) ||
				(got.StartFromOrigin != nil && *got.StartFromOrigin != *tt.wantOrigin) {
				t.Fatalf("startFromOrigin = %v, want %v", got.StartFromOrigin, tt.wantOrigin)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }
