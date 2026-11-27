package storetest

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func init() {
	register("BlockedWorkspaceStaysListedButIneligible", caseBlockedWorkspace)
}

// A workspace the harness will not launch in (BlockedReason, e.g. not
// trusted) stays in inventory and destinations, but creation, selection and
// launch claims refuse it with the harness's reason until a refresh clears it.
func caseBlockedWorkspace(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	const connection = dbtest.T3Connection
	const reason = "Not trusted in Claude Code. Run claude in this folder once and accept the trust prompt, then refresh."
	p := mustProject(t, s, ctx, "blocked")
	listed := execution.WorkspaceInfo{ID: "open", WorkspaceRoot: "/w/open"}
	untrusted := execution.WorkspaceInfo{ID: "lab", WorkspaceRoot: "/w/lab", BlockedReason: reason}
	if _, err := s.RefreshWorkspaces(ctx, connection, []execution.WorkspaceInfo{listed, untrusted}); err != nil {
		t.Fatal(err)
	}
	openID := db.WorkspaceID(connection, listed.ID, listed.WorkspaceRoot)
	labID := db.WorkspaceID(connection, untrusted.ID, untrusted.WorkspaceRoot)
	assertBlocked := func(err error) {
		t.Helper()
		var c *db.ConflictError
		if !errors.As(err, &c) || c.Code != "destination_unavailable" || !strings.HasSuffix(c.Msg, reason) {
			t.Fatalf("got %v, want destination_unavailable naming the harness reason", err)
		}
	}

	rows, _, err := s.WorkspaceCatalog(ctx, connection)
	if err != nil || !slices.ContainsFunc(rows, func(w db.Workspace) bool { return w.ExternalID == untrusted.ID && w.BlockedReason == reason }) ||
		!slices.ContainsFunc(rows, func(w db.Workspace) bool { return w.ExternalID == listed.ID && w.BlockedReason == "" }) {
		t.Fatalf("catalog keeps the blocked row with its reason: %+v %v", rows, err)
	}
	destinations, err := s.DestinationWorkspaces(ctx)
	if err != nil || !slices.ContainsFunc(destinations, func(w db.Workspace) bool { return w.WorkspaceID == labID && w.BlockedReason == reason }) {
		t.Fatalf("destinations list the blocked row with its reason: %+v %v", destinations, err)
	}
	_, err = s.ResolveDestination(ctx, connection, labID)
	assertBlocked(err)
	_, err = s.CreateTask(ctx, db.NewTask{ProjectID: p.ID, ConnectionID: connection, WorkspaceID: labID, Title: "Blocked", Prompt: "p"})
	assertBlocked(err)

	task, err := s.CreateTask(ctx, db.NewTask{ProjectID: p.ID, ConnectionID: connection, WorkspaceID: openID, Title: "Open", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateTask(ctx, task.ID, db.TaskPatch{WorkspaceID: db.Optional[string]{Set: true, V: labID}})
	assertBlocked(err)

	// The open folder loses trust after the task pinned it: the launch claim refuses.
	listed.BlockedReason = reason
	if _, err := s.RefreshWorkspaces(ctx, connection, []execution.WorkspaceInfo{listed, untrusted}); err != nil {
		t.Fatal(err)
	}
	pin := db.WorkspacePin{ExternalID: listed.ID, Root: listed.WorkspaceRoot}
	launch := db.Transition{TaskID: task.ID, From: []db.TaskState{db.StateBacklog}, To: db.StateLaunching, BeginAttempt: true,
		LaunchCheck: &db.LaunchCheck{ConnectionID: connection, WorkspacePin: pin, Config: task.ExecutionConfig}}
	_, err = s.TransitionTask(ctx, launch)
	assertBlocked(err)
	if got := getTask(t, s, ctx, task.ID); got.State != db.StateBacklog || got.LaunchAttempt != 0 {
		t.Fatalf("a refused claim changed the task: %+v", got)
	}

	// Trusting the folder in the harness and refreshing makes it eligible again.
	listed.BlockedReason = ""
	if _, err := s.RefreshWorkspaces(ctx, connection, []execution.WorkspaceInfo{listed, untrusted}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.TransitionTask(ctx, launch); err != nil || !got.Changed {
		t.Fatalf("claim after trust %+v, %v", got, err)
	}
}
