package serve_test

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest/fakestate"
	"github.com/flexdinesh/stage/server/internal/claudecodesetup"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/serve"
)

func TestSharedClaudeNonRootDestinationAndForgottenHistory(t *testing.T) {
	short := isolate(t)
	upstream := freePort(t)
	proxy := proxyFor(t, upstream)
	origin := proxy.URL
	c := cfg(t, filepath.Join(short, "data"), upstream)
	c.PublicURL, c.MultiUser = origin, new(true)
	fake := claudetest.New(t)
	fake.GitInit()
	nested := filepath.Join(fake.Workspace, "subdir")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	const listedID = "f4cd468a-8f90-4290-9c5d-e397ec6e98c7"
	const listedTitle = "Check the nested workspace"
	fake.AddInteractive(listedID, nested, "cli", "Inspect the subdirectory.")
	fake.Update(func(state *fakestate.State) {
		state.Sessions[0].Name = listedTitle
	})
	output := &syncBuffer{}
	startSharedOwner(t, serve.Options{
		Config: c, BootstrapOutput: output,
		Profile: harness.Profile{Kind: harness.ProfileServer, LocalProcesses: true},
		HarnessOverrides: map[string]any{claudecode.Kind: claudecodesetup.Options{
			Home: fake.Home, Environ: fake.Environ(),
			LookPath: func(string) (string, error) { return fake.Bin, nil },
		}},
	})
	owner := browserClient(t, proxy)
	claimBrowser(t, owner, origin, output.String())
	member, account := inviteWorkflowMember(t, proxy, owner, "Claude")
	harnessPath := "/api/harnesses/" + claudecode.Kind
	browserPost(t, owner, origin, harnessPath+"/actions/"+claudecode.ActionConnect, "{}", http.StatusOK, nil)
	get := func(path string, want int, out any) {
		t.Helper()
		(transportRequest{method: "GET", path: path}).send(t, member, origin, want, out)
	}
	var status client.HarnessStatus
	get(harnessPath+"/status", http.StatusOK, &status)
	if !status.Connected || status.ConnectionID == 0 {
		t.Fatalf("Claude connection: %+v", status)
	}
	connection := fmt.Sprintf("/api/connections/%d", status.ConnectionID)
	row := fixtureWorkspace(t, member, origin, status.ConnectionID, nested, nested)
	if row.Importable || row.ProjectID != nil {
		t.Fatalf("nested workspace acquired its own project through discovery: %+v", row)
	}
	destination := fixtureWorkflowDestination(t, member, origin, status.ConnectionID, row.WorkspaceID)

	var roster workflowRoster
	get(connection+"/sessions?q="+url.QueryEscape(listedTitle), http.StatusOK, &roster)
	listed := db.SessionRef{ConnectionID: status.ConnectionID, ExternalSessionID: listedID}
	if len(roster.Sessions) != 1 || roster.Sessions[0].Session != listed || roster.Sessions[0].Title != listedTitle || roster.Sessions[0].Workspace != nested ||
		!roster.Sessions[0].Eligibility.Available || roster.Sessions[0].TrackedTaskID != nil {
		t.Fatalf("nested session browse: %+v", roster)
	}

	var tracked client.Task
	listedPath := connection + "/sessions/" + listedID
	get(listedPath+"/context", http.StatusOK, nil)
	browserPost(t, member, origin, listedPath+"/adopt", `{"initialNote":"Tracked from the Claude roster."}`, http.StatusCreated, &tracked)
	if tracked.Session == nil || *tracked.Session != listed || !tracked.Adopted || tracked.CreatedBy != account.ID {
		t.Fatalf("tracked nested session: %+v", tracked)
	}

	var task client.Task
	browserPost(t, member, origin, "/api/tasks", fmt.Sprintf(
		`{"connectionId":%d,"workspaceId":%q,"title":"Nested launch","prompt":"Run inside the subdirectory","start":true}`,
		destination.ConnectionID, destination.WorkspaceID), http.StatusCreated, &task)
	launches := fake.Launches()
	if task.Session == nil || task.State != db.StateRunning || task.ConnectionID != status.ConnectionID || task.WorkspaceID != row.WorkspaceID ||
		task.Workspace == nil || task.Workspace.Path != nested || len(launches) != 1 || launches[0].Dir != nested {
		t.Fatalf("exact nested launch: task %+v, Claude calls %+v", task, launches)
	}

	const reply = "Finished work inside the subdirectory."
	fake.Reply(task.Session.ExternalSessionID, reply, true)
	fake.SetState(task.Session.ExternalSessionID, "done", "idle", "")
	if code := fake.Hook(task.Session.ExternalSessionID, "Stop", nil); code != http.StatusNoContent {
		t.Fatalf("Claude stop hook returned %d", code)
	}
	taskPath := fmt.Sprintf("/api/tasks/%d", task.ID)
	awaitSharedWorkflow(t, "the Claude reply to be recorded", func() bool {
		var detail client.TaskDetail
		get(taskPath, http.StatusOK, &detail)
		return detail.State == db.StateDone && detail.FinalReply != nil && detail.FinalReply.Text == reply
	})
	(transportRequest{method: "DELETE", path: harnessPath + "/connection", origin: origin}).
		send(t, owner, origin, http.StatusOK, nil)
	(transportRequest{method: "DELETE", path: connection, origin: origin}).
		send(t, owner, origin, http.StatusNoContent, nil)

	get(connection+"/workspaces", http.StatusNotFound, nil)
	var catalog client.ProjectSummaries
	get("/api/projects?view=destinations", http.StatusOK, &catalog)
	if slices.ContainsFunc(catalog.Destinations, func(destination db.Destination) bool {
		return destination.ConnectionID == status.ConnectionID
	}) {
		t.Fatalf("forgotten Claude connection is still selectable: %+v", catalog.Destinations)
	}
	var history client.TaskDetail
	get(taskPath, http.StatusOK, &history)
	if history.ConnectionActive || history.Session == nil || *history.Session != *task.Session || history.CreatedBy != account.ID ||
		history.ProjectID != task.ProjectID || history.WorkspaceID != row.WorkspaceID ||
		history.State != db.StateDone || history.FinalReply == nil || history.FinalReply.Text != reply {
		t.Fatalf("forgetting Claude lost historical task data: %+v", history)
	}
	var trackedHistory client.TaskDetail
	get(fmt.Sprintf("/api/tasks/%d", tracked.ID), http.StatusOK, &trackedHistory)
	if trackedHistory.ConnectionActive || trackedHistory.Session == nil || *trackedHistory.Session != listed ||
		trackedHistory.ProjectID != tracked.ProjectID || trackedHistory.CreatedBy != account.ID || !trackedHistory.Adopted {
		t.Fatalf("forgotten Claude connection lost tracked history: %+v", trackedHistory)
	}
	get(fmt.Sprintf("/api/project-groups/%d", trackedHistory.ProjectID), http.StatusOK, nil)

}
