package engine_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// Adoption starts from the same observation the poller uses. Idle becomes
// backlog or done according to whether the session recorded work.
func TestAdoptStateMatchesObservation(t *testing.T) {
	store := db.NewStore(testDB.Reset(t))
	ctx := context.Background()
	if _, err := store.InsertProject(ctx, db.Project{Title: "app", Bindings: []db.ProjectBinding{{ConnectionID: 1, ExternalProjectID: "proj-1", Workspace: "/w"}}}); err != nil {
		t.Fatal(err)
	}
	statuses := []t3.ThreadStatus{
		t3.StatusIdle, t3.StatusPreparing, t3.StatusQueued, t3.StatusStarting, t3.StatusRunning, t3.StatusWaiting,
		t3.StatusCompleted, t3.StatusFailed, t3.StatusInterrupted, t3.StatusCancelled, t3.StatusRolledBack,
	}
	latest := append([]t3.ThreadStatus{""}, statuses...)
	now := time.Now().UTC()
	n := 0
	for _, status := range statuses {
		for _, run := range latest {
			for _, settled := range []bool{false, true} {
				for _, pending := range []int{0, 1} {
					n++
					id := fmt.Sprintf("th-%d", n)
					d := t3.ThreadDetail{
						Thread:              t3.Thread{ThreadID: id, ProjectID: "proj-1", Title: id, Status: status, Settled: settled, CreatedAt: now, UpdatedAt: now},
						PendingRequestCount: pending,
					}
					if run != "" {
						d.RecentRuns = []t3.Run{{RunID: "r1", Status: run, RequestedAt: now}}
					}
					if err := store.PutObservation(ctx, d.Observation()); err != nil {
						t.Fatal(err)
					}
					task, created, err := store.AdoptSession(ctx, db.AdoptInput{Session: db.SessionRef{ConnectionID: dbtest.T3Connection, ExternalSessionID: id}})
					if err != nil || !created {
						t.Fatalf("adopt %s: %v", id, err)
					}
					// Idle: done once the harness reported an execution, else backlog.
					o := d.Observation()
					want := db.TaskState(o.State)
					if want == "idle" {
						want = db.StateDone
						if o.StartedAt == nil && o.EndedAt == nil {
							want = db.StateBacklog
						}
					}
					if task.State != want {
						t.Fatalf("status=%s latest=%q settled=%v pending=%d: adopted %s, want %s", status, run, settled, pending, task.State, want)
					}
				}
			}
		}
	}
}
