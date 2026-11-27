package storetest

import (
	"errors"
	"slices"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func init() {
	register("ExactUnboundWorkspaceAndStaleLaunchClaim", caseExactUnboundWorkspaceAndStaleLaunchClaim)
}

func caseExactUnboundWorkspaceAndStaleLaunchClaim(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	const connection = dbtest.T3Connection
	p := mustProject(t, s, ctx, "planning")
	first := execution.WorkspaceInfo{ID: "shared", WorkspaceRoot: "/w/first"}
	second := execution.WorkspaceInfo{ID: first.ID, WorkspaceRoot: "/w/second"}
	catalog := []execution.WorkspaceInfo{{ID: "planning", WorkspaceRoot: "/w/planning"}, first, second}
	if _, err := s.RefreshWorkspaces(ctx, connection, catalog); err != nil {
		t.Fatal(err)
	}
	firstID := db.WorkspaceID(connection, first.ID, first.WorkspaceRoot)
	secondID := db.WorkspaceID(connection, second.ID, second.WorkspaceRoot)
	w, err := s.ResolveDestination(ctx, connection, firstID)
	if err != nil || w.ProjectID != nil || w.ExternalID != first.ID || w.Workspace != first.WorkspaceRoot {
		t.Fatalf("unbound catalog row %+v, %v", w, err)
	}
	task, err := s.CreateTask(ctx, db.NewTask{ProjectID: p.ID, ConnectionID: connection, WorkspaceID: firstID, Title: "Exact", Prompt: "p"})
	firstPin := db.WorkspacePin{ExternalID: first.ID, Root: first.WorkspaceRoot}
	if err != nil || task.ProjectID != p.ID || task.BindingID != nil || task.WorkspaceID != firstID || task.WorkspacePin == nil || *task.WorkspacePin != firstPin {
		t.Fatalf("exact task %+v, %v", task, err)
	}
	evidence, _, _, err := s.ConnectionEvidence(ctx, connection)
	if err != nil || !slices.ContainsFunc(evidence, func(b db.ProjectBinding) bool {
		return b.ExternalProjectID == second.ID && b.Workspace == second.WorkspaceRoot
	}) {
		t.Fatalf("unbound catalog evidence %+v, %v", evidence, err)
	}
	if err := s.RecordWorkspaceRefreshFailure(ctx, connection, "catalog offline"); err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.DuplicateTask(ctx, task.ID)
	if err != nil || duplicate.ID == task.ID || duplicate.ProjectID != p.ID || duplicate.BindingID != nil || duplicate.WorkspaceID != firstID {
		t.Fatalf("duplicate %+v, %v", duplicate, err)
	}
	launch := db.Transition{TaskID: task.ID, From: []db.TaskState{db.StateBacklog}, To: db.StateLaunching, BeginAttempt: true,
		LaunchCheck: &db.LaunchCheck{ConnectionID: connection, WorkspacePin: firstPin, Config: task.ExecutionConfig}}
	_, err = s.TransitionTask(ctx, launch)
	assertProjectConflict(t, err, "destination_unavailable")
	if _, err := s.RefreshWorkspaces(ctx, connection, catalog); err != nil {
		t.Fatal(err)
	}

	// A user edits the selection after preflight while concurrent claimants still hold the old check.
	updated, err := s.UpdateTask(ctx, task.ID, db.TaskPatch{WorkspaceID: db.Optional[string]{Set: true, V: secondID}})
	secondPin := db.WorkspacePin{ExternalID: second.ID, Root: second.WorkspaceRoot}
	if err != nil || updated.WorkspaceID != secondID || updated.WorkspacePin == nil || *updated.WorkspacePin != secondPin {
		t.Fatalf("updated selection %+v, %v", updated, err)
	}
	for _, err := range race(racers, func(int) error {
		_, err := s.TransitionTask(ctx, launch)
		return err
	}) {
		if !isConflict(err) {
			t.Fatalf("stale claim: %v, want conflict", err)
		}
	}
	if got := getTask(t, s, ctx, task.ID); got.State != db.StateBacklog || got.LaunchAttempt != 0 || got.WorkspaceID != secondID {
		t.Fatalf("after stale claims: %+v", got)
	}
	if _, err := s.Attempt(ctx, task.ID, 1); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("stale claims left an attempt: %v", err)
	}
	if _, err := s.LaunchDestination(ctx, task.ID, 1); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("stale claims left a launch destination: %v", err)
	}

	launch.LaunchCheck = &db.LaunchCheck{ConnectionID: connection, WorkspacePin: secondPin, Config: updated.ExecutionConfig}
	if got, err := s.TransitionTask(ctx, launch); err != nil || !got.Changed || got.Attempt != 1 || got.RequestID == "" {
		t.Fatalf("fresh claim %+v, %v", got, err)
	}
	if got, err := s.LaunchDestination(ctx, task.ID, 1); err != nil || got != (db.LaunchDestination{ConnectionID: connection, ExternalProjectID: second.ID, Workspace: second.WorkspaceRoot}) {
		t.Fatalf("fresh destination %+v, %v", got, err)
	}
	launch.TaskID = duplicate.ID
	launch.LaunchCheck = &db.LaunchCheck{ConnectionID: connection, WorkspacePin: firstPin, Config: duplicate.ExecutionConfig}
	if got, err := s.TransitionTask(ctx, launch); err != nil || !got.Changed {
		t.Fatalf("duplicate claim %+v, %v", got, err)
	}
	if got, err := s.LaunchDestination(ctx, duplicate.ID, 1); err != nil || got != (db.LaunchDestination{ConnectionID: connection, ExternalProjectID: first.ID, Workspace: first.WorkspaceRoot}) {
		t.Fatalf("duplicate destination %+v, %v", got, err)
	}
}
