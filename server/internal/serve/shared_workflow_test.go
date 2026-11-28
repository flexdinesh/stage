package serve_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/harness"
	"github.com/flexdinesh/stage/server/internal/harnesses"
	"github.com/flexdinesh/stage/server/internal/listen"
	"github.com/flexdinesh/stage/server/internal/machine"
	"github.com/flexdinesh/stage/server/internal/remote"
	"github.com/flexdinesh/stage/server/internal/serve"
	"github.com/flexdinesh/stage/server/internal/t3"
	"github.com/flexdinesh/stage/server/internal/webui"
	"github.com/flexdinesh/stage/server/internal/worker"
	"github.com/flexdinesh/stage/server/internal/workerchan"
	"github.com/flexdinesh/stage/server/internal/workersetup"
)

func TestSharedWorkflowAcrossPairedWorkers(t *testing.T) {
	sharedWorkflowAcrossPairedWorkers(t, cfg)
}

func sharedWorkflowAcrossPairedWorkers(t *testing.T, configure func(*testing.T, string, ...string) config.Config) {
	t.Helper()
	short := isolate(t)
	upstream := freePort(t)
	proxy := proxyFor(t, upstream)
	origin := proxy.URL
	c := configure(t, filepath.Join(short, "data"), upstream)
	c.PublicURL = origin
	mainHarness := newT3()
	setWorkflowHarnessIdentity(mainHarness.Fake, "main")
	personal := start(t, c, mainHarness)
	anon := proxy.Client()
	get := func(h *http.Client, path string, want int, out any) {
		t.Helper()
		(transportRequest{method: "GET", path: path}).send(t, h, origin, want, out)
	}
	var access client.AccessStatus
	get(anon, "/api/auth/status", http.StatusOK, &access)
	if access.Mode != "personal" {
		t.Fatalf("initial mode: %+v", access)
	}
	get(anon, "/api/me", http.StatusOK, nil)

	workerHarness := &reportBackT3{Fake: t3.NewFake()}
	setWorkflowHarnessIdentity(workerHarness.Fake, "first")
	secondHarness := t3.NewFake()
	setWorkflowHarnessIdentity(secondHarness, "second")
	workspace := t.TempDir()
	remoteProject := workerHarness.AddProject("Worker project", workspace)
	secondWorkspace := t.TempDir()
	secondProject := secondHarness.AddProject("Worker project", secondWorkspace)
	firstID, firstGateway := joinWorkflowWorker(t, proxy, short, "first", workerHarness)
	secondID, secondGateway := joinWorkflowWorker(t, proxy, short, "second", secondHarness)
	for _, gateway := range []listen.Addr{firstGateway, secondGateway} {
		(transportRequest{method: "GET", path: "/api/tasks"}).send(t, clientFor(gateway).HTTP, "http://stage", http.StatusOK, nil)
	}
	var remoteHarness client.HarnessStatus
	harnessPath := fmt.Sprintf("/api/machines/%d/harnesses/t3/status", firstID)
	get(anon, harnessPath, http.StatusOK, &remoteHarness)
	var spareHarness client.HarnessStatus
	get(anon, fmt.Sprintf("/api/machines/%d/harnesses/t3/status", secondID), http.StatusOK, &spareHarness)
	if !remoteHarness.Connected || remoteHarness.ConnectionID == 0 || !spareHarness.Connected ||
		spareHarness.ConnectionID == 0 || spareHarness.ConnectionID == remoteHarness.ConnectionID {
		t.Fatalf("personal worker harnesses: first %+v, second %+v", remoteHarness, spareHarness)
	}
	workspaceID := db.WorkspaceID(remoteHarness.ConnectionID, remoteProject.ID, workspace)
	var project int64
	awaitSharedWorkflow(t, "discovery to import the original worker project", func() bool {
		var catalog struct{ Workspaces []db.Workspace }
		get(anon, fmt.Sprintf("/api/connections/%d/workspaces?refresh=false", remoteHarness.ConnectionID), http.StatusOK, &catalog)
		for _, row := range catalog.Workspaces {
			if row.WorkspaceID == workspaceID && row.Available && row.ProjectID != nil {
				project = *row.ProjectID
				return true
			}
		}
		return false
	})
	projectPath := fmt.Sprintf("/api/projects/%d", project)
	var previousProject db.Project
	get(anon, projectPath, http.StatusOK, &previousProject)
	if previousProject.Origin == nil || *previousProject.Origin != "discovered" || previousProject.CreatedBy != db.DefaultUser {
		t.Fatalf("original project provenance: %+v", previousProject)
	}
	var previous client.Task
	browserPost(t, anon, origin, "/api/tasks", fmt.Sprintf(
		`{"title":"Personal worker task","prompt":"Run personal work","projectId":%d,"connectionId":%d,"workspaceId":%q,"start":true}`,
		project, remoteHarness.ConnectionID, workspaceID),
		http.StatusCreated, &previous)
	if previous.State != db.StateRunning || previous.Session == nil || previous.Session.ConnectionID != remoteHarness.ConnectionID {
		t.Fatalf("personal worker launch: %+v", previous)
	}
	if err := personal.stop(t); err != nil {
		t.Fatal(err)
	}

	c.MultiUser = new(true)
	output := &syncBuffer{}
	_, sharedLog := startSharedOwner(t, serve.Options{Config: c, BootstrapOutput: output, HarnessOverrides: map[string]any{t3.Kind: mainHarness}})
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(sharedLog.String())
		}
	})
	gateway := clientFor(firstGateway)
	(transportRequest{method: "GET", path: "/api/tasks"}).send(t, gateway.HTTP, "http://stage", http.StatusForbidden, nil)
	get(anon, "/api/auth/status", http.StatusOK, &access)
	if access.Mode != "setup_pending" || access.InstallationID == "" {
		t.Fatalf("pending mode: %+v", access)
	}
	installation := access.InstallationID
	owner := browserClient(t, proxy)
	claimBrowser(t, owner, origin, output.String())
	get(anon, "/api/auth/status", http.StatusOK, &access)
	if access.Mode != "claimed" || access.InstallationID != installation {
		t.Fatalf("claimed mode: %+v", access)
	}
	var ownerAccount client.Account
	get(owner, "/api/me", http.StatusOK, &ownerAccount)
	if ownerAccount.ID != db.DefaultUser || ownerAccount.Role != "admin" {
		t.Fatalf("claim did not preserve the original user: %+v", ownerAccount)
	}
	var old client.TaskDetail
	get(owner, fmt.Sprintf("/api/tasks/%d", previous.ID), http.StatusOK, &old)
	if old.CreatedBy != ownerAccount.ID || old.Session == nil || previous.Session == nil || *old.Session != *previous.Session ||
		old.LaunchAttempt != previous.LaunchAttempt || old.ProjectID != project || old.WorkspaceID != workspaceID ||
		len(old.Events) == 0 || old.Events[0].Actor.Kind != "personal" || old.Events[0].Actor.UserID != 0 {
		t.Fatalf("upgrade changed original task: %+v", old.Task)
	}

	var retainedProject db.Project
	get(owner, projectPath, http.StatusOK, &retainedProject)
	if retainedProject.ID != previousProject.ID || retainedProject.Title != previousProject.Title ||
		retainedProject.CreatedBy != previousProject.CreatedBy || retainedProject.Origin == nil || *retainedProject.Origin != *previousProject.Origin ||
		retainedProject.ImportedAt == nil || previousProject.ImportedAt == nil || !retainedProject.ImportedAt.Equal(*previousProject.ImportedAt) {
		t.Fatalf("upgrade changed project provenance: before %+v, after %+v", previousProject, retainedProject)
	}

	creator, creatorAccount := inviteWorkflowMember(t, proxy, owner, "A")
	member, memberAccount := inviteWorkflowMember(t, proxy, owner, "B")
	if creatorAccount.ID == memberAccount.ID {
		t.Fatalf("invitations resolved to the same account: %+v", memberAccount)
	}
	get(anon, "/api/tasks", http.StatusUnauthorized, nil)

	var machines struct{ Machines []client.Machine }
	awaitSharedWorkflow(t, "both original workers to reconnect", func() bool {
		get(member, "/api/machines", http.StatusOK, &machines)
		for _, id := range []int64{firstID, secondID} {
			if !slices.ContainsFunc(machines.Machines, func(m client.Machine) bool { return m.ID == id && m.Online }) {
				return false
			}
		}
		return true
	})
	var restored client.HarnessStatus
	get(member, harnessPath, http.StatusOK, &restored)
	if !restored.Connected || restored.ConnectionID != remoteHarness.ConnectionID {
		t.Fatalf("upgrade changed worker connection: before %+v, after %+v", remoteHarness, restored)
	}
	other := fixtureWorkspace(t, member, origin, spareHarness.ConnectionID, secondProject.ID, secondWorkspace)
	target := fixtureWorkflowDestination(t, creator, origin, restored.ConnectionID, workspaceID)
	if target.MachineLabel != "first" || target.ProjectID == nil || *target.ProjectID != project {
		t.Fatalf("first worker destination: %+v", target)
	}
	spare := fixtureWorkflowDestination(t, creator, origin, spareHarness.ConnectionID, other.WorkspaceID)
	if spare.MachineLabel != "second" {
		t.Fatalf("second worker destination: %+v", spare)
	}

	// This workflow requires the member to review and accept the result.
	(transportRequest{method: "PUT", path: fmt.Sprintf("/api/connections/%d/auto-complete", restored.ConnectionID),
		origin: origin, body: `{"autoComplete":false}`}).send(t, member, origin, http.StatusOK, nil)
	var task client.Task
	draft := fmt.Sprintf(`{"title":"Review worker result","prompt":"Run the fixture","projectId":%d,"connectionId":%d}`, project, target.ConnectionID)
	browserPost(t, creator, origin, "/api/tasks", draft, http.StatusCreated, &task)
	if task.State != db.StateBacklog || task.WorkspaceID != "" || task.Workspace != nil {
		t.Fatalf("draft selected a destination implicitly: %+v", task)
	}
	taskPath := fmt.Sprintf("/api/tasks/%d", task.ID)
	var failure struct{ Code string }
	browserPost(t, member, origin, taskPath+"/start", "", http.StatusConflict, &failure)
	if failure.Code != "destination_required" {
		t.Fatalf("unresolved task start error: %+v", failure)
	}
	(transportRequest{method: "PATCH", path: taskPath, origin: origin,
		body: fmt.Sprintf(`{"connectionId":%d,"workspaceId":%q}`, target.ConnectionID, spare.WorkspaceID)}).
		send(t, creator, origin, http.StatusConflict, &failure)
	if failure.Code != "destination_unavailable" {
		t.Fatalf("workspace from a different connection: %+v", failure)
	}
	(transportRequest{method: "PATCH", path: taskPath, origin: origin,
		body: fmt.Sprintf(`{"connectionId":%d,"workspaceId":%q}`, target.ConnectionID, target.WorkspaceID)}).
		send(t, creator, origin, http.StatusOK, &task)
	if task.WorkspaceID != target.WorkspaceID {
		t.Fatalf("explicit destination was lost: %+v", task)
	}
	var tree db.TreeDetail
	browserPost(t, creator, origin, "/api/trees", fmt.Sprintf(`{"title":"Shared launch","startMode":"manual","tasks":[{"taskId":%d}]}`, task.ID), http.StatusCreated, &tree)
	if tree.CreatedBy != creatorAccount.ID || len(tree.Tasks) != 1 || tree.Tasks[0].ID != task.ID {
		t.Fatalf("member A's tree: %+v", tree)
	}
	treePath := fmt.Sprintf("/api/trees/%d", tree.ID)
	var started client.Task
	browserPost(t, member, origin, taskPath+"/start", "", http.StatusOK, &started)
	if started.State != db.StateRunning || started.Session == nil || started.Session.ConnectionID != restored.ConnectionID ||
		started.CreatedBy != creatorAccount.ID || started.WorkspaceID != target.WorkspaceID || mainHarness.launches.Load() != 0 {
		t.Fatalf("member did not start the existing task on the worker: %+v", started)
	}
	session := *started.Session
	workerHarness.mu.Lock()
	prompt := workerHarness.prompt
	workerHarness.mu.Unlock()
	for _, expected := range []string{"Run the fixture", "this machine's private Stage worker socket", fmt.Sprintf("Stage taskId: %d.", task.ID)} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("worker launch prompt missing %q: %s", expected, prompt)
		}
	}

	const noteText = "Worker ran the fixture."
	var note db.JournalEntry
	body := fmt.Sprintf(`{"text":%q,"requestId":"workflow-result","sourceSession":{"connectionId":%d,"externalSessionId":%q}}`,
		noteText, session.ConnectionID, session.ExternalSessionID)
	(transportRequest{method: "POST", path: taskPath + "/journal", body: body}).
		send(t, gateway.HTTP, "http://stage", http.StatusCreated, &note)
	if note.Text != noteText || note.Actor.Kind != "agent" || note.Actor.UserID != 0 ||
		note.SourceMachineID == nil || *note.SourceMachineID != firstID || note.SourceSession == nil || *note.SourceSession != session {
		t.Fatalf("worker report attribution: %+v", note)
	}
	// The worker run waits on an approval: needs input is a shared work
	// event in every account's inbox (a finished run is not).
	request, err := workerHarness.AddApproval(session.ExternalSessionID)
	if err != nil {
		t.Fatal(err)
	}
	awaitSharedWorkflow(t, "worker task to need input", func() bool {
		var waiting client.TaskDetail
		get(member, taskPath, http.StatusOK, &waiting)
		return waiting.State == db.StateNeedsInput
	})
	if err := workerHarness.ResolveRequest(session.ExternalSessionID, request, "resolved"); err != nil {
		t.Fatal(err)
	}
	const reply = "Fixture passed on the worker."
	if err := workerHarness.SetReply(session.ExternalSessionID, t3.Reply{ItemID: "worker-reply", Text: reply}); err != nil {
		t.Fatal(err)
	}
	if err := workerHarness.SetStatus(session.ExternalSessionID, t3.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	if err := workerHarness.Settle(session.ExternalSessionID); err != nil {
		t.Fatal(err)
	}
	var settled client.TaskDetail
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("last worker task snapshot: state=%s final reply=%+v", settled.State, settled.FinalReply)
		}
	})
	awaitSharedWorkflow(t, "settled task and final reply to arrive from the worker", func() bool {
		get(member, taskPath, http.StatusOK, &settled)
		return settled.State == db.StateDone && settled.FinalReply != nil && settled.FinalReply.Text == reply
	})
	var preview db.CompletionPreview
	get(member, taskPath+"/completion-preview", http.StatusOK, &preview)
	if preview.BlockedReason != "" {
		t.Fatalf("worker result cannot be accepted: %s", preview.BlockedReason)
	}
	var accepted db.AcceptanceResult
	browserPost(t, member, origin, taskPath+"/complete",
		fmt.Sprintf(`{"requestId":"workflow-acceptance","revision":%d,"evidenceVersion":%q,"note":"Reviewed the worker result."}`, preview.Revision, preview.EvidenceVersion), http.StatusOK, &accepted)
	if accepted.AcceptanceID == nil {
		t.Fatal("member did not accept the result")
	}

	for _, account := range []*http.Client{owner, creator, member} {
		var detail client.TaskDetail
		get(account, taskPath, http.StatusOK, &detail)
		if len(detail.Acceptances) != 1 {
			t.Fatalf("accepted result: %+v", detail.Acceptances)
		}
		result := detail.Acceptances[0]
		if result.ID != *accepted.AcceptanceID || result.Actor.Kind != "human" || result.Actor.UserID != memberAccount.ID || result.Actor.DisplayName != memberAccount.DisplayName ||
			result.Evidence.Reply == nil || *result.Evidence.Reply != reply || result.Evidence.JournalThroughID < note.ID {
			t.Fatalf("accepted evidence or attribution: %+v", result)
		}
		var activity db.TreeDetail
		get(account, treePath, http.StatusOK, &activity)
		if !slices.ContainsFunc(activity.Events, func(e db.TaskEvent) bool {
			return e.ToState == db.StateLaunching && e.Actor.Kind == "human" && e.Actor.UserID == memberAccount.ID
		}) || !slices.ContainsFunc(activity.Events, func(e db.TaskEvent) bool {
			return e.ToState == db.StateDone && e.Actor.Kind == "system" && e.SourceMachine != nil && *e.SourceMachine == firstID
		}) {
			t.Fatalf("tree activity missing human launch or worker observation: %+v", activity.Events)
		}
		var journal db.JournalPage
		get(account, treePath+"/journal?kind=all", http.StatusOK, &journal)
		if !slices.ContainsFunc(journal.Entries, func(e db.JournalEntry) bool {
			return e.EventType == "tree_created" && e.Actor.Kind == "human" && e.Actor.UserID == creatorAccount.ID
		}) || !slices.ContainsFunc(journal.Entries, func(e db.JournalEntry) bool {
			return e.ID == note.ID && e.Actor.Kind == "agent" && e.SourceMachineID != nil && *e.SourceMachineID == firstID
		}) {
			t.Fatalf("tree journal missing member A's creation or worker report: %+v", journal.Entries)
		}
	}
	assertSharedWorkflowInbox(t, origin, creator, member, task.ID)
	assertSharedWorkflowSessions(t, proxy, creator, member, creatorAccount.ID, memberAccount.ID, gateway, workerHarness.Fake, remoteProject.ID, project, target)
	assertSharedWorkflowSchedule(t, origin, creator, member, creatorAccount.ID, memberAccount.ID, target)
}

func inviteWorkflowMember(t *testing.T, proxy *httptest.Server, owner *http.Client, name string) (*http.Client, client.Account) {
	t.Helper()
	var invitation struct{ URL string }
	browserPost(t, owner, proxy.URL, "/api/admin/invitations", fmt.Sprintf(`{"email":%q}`, "member"+strings.ToLower(name)+"@example.test"), http.StatusCreated, &invitation)
	token := linkToken(t, invitation.URL)
	shell := httptest.NewRecorder()
	webui.Handler().ServeHTTP(shell, httptest.NewRequest("GET", "/auth/invite", nil))
	(transportRequest{method: "GET", path: strings.TrimPrefix(invitation.URL, proxy.URL)}).send(t, proxy.Client(), proxy.URL, shell.Code, nil)
	member := browserClient(t, proxy)
	browserPost(t, member, proxy.URL, "/api/auth/invite/inspect", fmt.Sprintf(`{"token":%q}`, token), http.StatusOK, nil)
	browserPost(t, member, proxy.URL, "/api/auth/invite", fmt.Sprintf(`{"token":%q,"name":%q,"password":"starfish collides tonight"}`, token, name), http.StatusOK, nil)
	var account client.Account
	(transportRequest{method: "GET", path: "/api/me"}).send(t, member, proxy.URL, http.StatusOK, &account)
	if account.ID <= db.DefaultUser || account.Role != "member" {
		t.Fatalf("invited account: %+v", account)
	}
	(transportRequest{method: "GET", path: "/api/admin/accounts"}).send(t, member, proxy.URL, http.StatusForbidden, nil)
	return member, account
}

func fixtureWorkflowDestination(t *testing.T, h *http.Client, base string, connection int64, workspace string) db.Destination {
	t.Helper()
	c := client.New(base)
	c.HTTP = h
	catalog, err := c.ProjectSummaries(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range catalog.Destinations {
		if row.ConnectionID == connection && row.WorkspaceID == workspace && row.Available {
			return row
		}
	}
	t.Fatalf("destination %d/%s missing from %+v", connection, workspace, catalog.Destinations)
	return db.Destination{}
}

type workflowRoster struct {
	Sessions []struct {
		Session       db.SessionRef
		Title         string
		Workspace     string
		Eligibility   execution.Availability
		TrackedTaskID *int64
	}
}

func assertSharedWorkflowSessions(t *testing.T, proxy *httptest.Server, creator, member *http.Client, creatorID, memberID int64, gateway *client.Client, fake *t3.Fake, external string, project int64, target db.Destination) {
	t.Helper()
	origin := proxy.URL
	connectionPath := fmt.Sprintf("/api/connections/%d/sessions", target.ConnectionID)
	seed := func(title string) db.SessionRef {
		t.Helper()
		res, err := fake.LaunchThread(t.Context(), t3.LaunchInput{ProjectID: external, Title: title})
		if err != nil {
			t.Fatal(err)
		}
		if err := fake.SetThread(res.ThreadID, func(thread *t3.ThreadDetail) {
			thread.Status, thread.Settled = t3.StatusCompleted, true
		}); err != nil {
			t.Fatal(err)
		}
		return db.SessionRef{ConnectionID: target.ConnectionID, ExternalSessionID: res.ThreadID}
	}
	const listedTitle = "Worker session for member B"
	listed := seed(listedTitle)
	rosterPath := connectionPath + "?q=" + url.QueryEscape(listedTitle)
	var credential struct{ Token string }
	browserPost(t, creator, origin, "/api/account/agent-credential", "", http.StatusOK, &credential)
	agentHTTP := *proxy.Client()
	agentHTTP.Transport = credentialTransport{agentHTTP.Transport, credential.Token}
	c := client.New(origin)
	c.HTTP = &agentHTTP
	(transportRequest{method: "GET", path: rosterPath}).send(t, c.HTTP, origin, http.StatusForbidden, nil)
	(transportRequest{method: "GET", path: rosterPath}).send(t, gateway.HTTP, "http://stage", http.StatusForbidden, nil)

	var roster workflowRoster
	(transportRequest{method: "GET", path: rosterPath}).send(t, member, origin, http.StatusOK, &roster)
	if len(roster.Sessions) != 1 || roster.Sessions[0].Session != listed || roster.Sessions[0].Title != listedTitle ||
		!roster.Sessions[0].Eligibility.Available || roster.Sessions[0].TrackedTaskID != nil {
		t.Fatalf("member B's worker session search: %+v", roster)
	}
	var tracked client.Task
	sessionPath := connectionPath + "/" + url.PathEscape(roster.Sessions[0].Session.ExternalSessionID)
	browserPost(t, member, origin, sessionPath+"/adopt", "{}", http.StatusCreated, &tracked)
	if !tracked.Adopted || tracked.CreatedBy != memberID || tracked.Session == nil || *tracked.Session != listed {
		t.Fatalf("member B did not track the selected session: %+v", tracked)
	}
	roster = workflowRoster{}
	(transportRequest{method: "GET", path: rosterPath}).send(t, creator, origin, http.StatusOK, &roster)
	if len(roster.Sessions) != 1 || roster.Sessions[0].TrackedTaskID == nil || *roster.Sessions[0].TrackedTaskID != tracked.ID {
		t.Fatalf("member A's tracked session search: %+v", roster)
	}

	destination := fixtureWorkflowDestination(t, c.HTTP, origin, target.ConnectionID, target.WorkspaceID)
	in := client.CreateTask{
		ProjectID: project, ConnectionID: destination.ConnectionID, Title: "Agent worker task", Prompt: "Work on the worker", Start: true,
	}
	var failure *client.Error
	if _, err := c.CreateTask(t.Context(), in); !errors.As(err, &failure) || failure.Status != http.StatusConflict || failure.Code != "destination_required" {
		t.Fatalf("agent launch without workspace: %v", err)
	}
	in.Start, in.WorkspaceID = false, destination.WorkspaceID
	task, err := c.CreateTask(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	started, err := c.StartTask(t.Context(), task.ID)
	if err != nil || started.ID != task.ID || started.CreatedBy != creatorID || started.Session == nil ||
		started.Session.ConnectionID != destination.ConnectionID || started.WorkspaceID != destination.WorkspaceID || started.State != db.StateRunning {
		t.Fatalf("agent did not launch on the selected worker workspace: %+v, %v", started, err)
	}

	ref := seed("Worker session for named adoption")
	preview, err := c.SessionContext(t.Context(), ref)
	if err != nil || preview.Session != ref || preview.Task != nil {
		t.Fatalf("named agent lookup: %+v, %v", preview, err)
	}
	adopted, created, err := c.AdoptScoped(t.Context(), ref, client.AdoptSession{ProjectID: &project})
	if err != nil || !created || !adopted.Adopted || adopted.CreatedBy != creatorID || adopted.Session == nil || *adopted.Session != ref || adopted.ProjectID != project {
		t.Fatalf("named agent adoption: %+v, %v", adopted, err)
	}
}

func assertSharedWorkflowSchedule(t *testing.T, origin string, creator, member *http.Client, creatorID, memberID int64, target db.Destination) {
	t.Helper()
	var schedule db.Schedule
	browserPost(t, creator, origin, "/api/schedules", fmt.Sprintf(
		`{"title":"Worker schedule","prompt":"Run scheduled work","connectionId":%d,"workspaceId":%q,"cron":"0 3 * * *","tz":"UTC","paused":true}`,
		target.ConnectionID, target.WorkspaceID), http.StatusCreated, &schedule)
	if schedule.CreatedBy != creatorID || schedule.ConnectionID != target.ConnectionID || schedule.WorkspaceID != target.WorkspaceID {
		t.Fatalf("schedule creation: %+v", schedule)
	}
	path := fmt.Sprintf("/api/schedules/%d", schedule.ID)
	var task client.Task
	browserPost(t, member, origin, path+"/run", "", http.StatusCreated, &task)
	if task.CreatedBy != creatorID || task.ScheduleID == nil || *task.ScheduleID != schedule.ID ||
		task.State != db.StateRunning || task.Session == nil || task.Session.ConnectionID != target.ConnectionID || task.WorkspaceID != target.WorkspaceID {
		t.Fatalf("manual schedule launch: %+v", task)
	}
	var detail client.ScheduleDetail
	(transportRequest{method: "GET", path: path}).send(t, creator, origin, http.StatusOK, &detail)
	if len(detail.Runs) != 1 {
		t.Fatalf("schedule runs: %+v", detail.Runs)
	}
	run := detail.Runs[0]
	if run.Outcome != db.RunManual || run.Actor.Kind != "human" || run.Actor.UserID != memberID ||
		run.Task == nil || run.Task.ID != task.ID || run.Task.CreatedBy != creatorID {
		t.Fatalf("schedule run actor and creator: %+v", run)
	}
	var taskDetail client.TaskDetail
	(transportRequest{method: "GET", path: fmt.Sprintf("/api/tasks/%d", task.ID)}).send(t, creator, origin, http.StatusOK, &taskDetail)
	if !slices.ContainsFunc(taskDetail.Events, func(event db.TaskEvent) bool {
		return event.ToState == db.StateLaunching && event.Actor.Kind == "human" && event.Actor.UserID == memberID &&
			event.Cause != nil && event.Cause.Kind == "schedule" && event.Cause.ScheduleID == schedule.ID
	}) {
		t.Fatalf("scheduled task launch actor: %+v", taskDetail.Events)
	}
}

func setWorkflowHarnessIdentity(fake *t3.Fake, label string) {
	// The stock fakes advertise the same environment. Use separate instances
	// so discovery sees two hosts and no moved T3 environment.
	fake.SetProbe(t3.Probe{Reachable: true, CheckedAt: time.Now(), Identity: &t3.Identity{EnvironmentID: "workflow-" + label, Label: label}})
}

func joinWorkflowWorker(t *testing.T, proxy *httptest.Server, short, label string, fake t3.Client) (int64, listen.Addr) {
	t.Helper()
	var pair client.Pairing
	browserPost(t, proxy.Client(), proxy.URL, "/api/machines/pairings", fmt.Sprintf(`{"label":%q}`, label), http.StatusCreated, &pair)
	dir := filepath.Join(short, label)
	cfgPath := filepath.Join(dir, worker.ConfigFile)
	joined, err := worker.Join(t.Context(), worker.JoinOptions{Link: pair.Link, ConfigPath: cfgPath, HTTP: proxy.Client()})
	if err != nil {
		t.Fatal(err)
	}
	getenv := func(key string) string {
		if key == "STAGE_DATA_DIR" {
			return dir
		}
		return ""
	}
	socket, err := config.DefaultSocket(getenv, dir)
	if err != nil {
		t.Fatal(err)
	}
	gateway := listen.Addr{Network: "unix", Address: socket, Private: true}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- worker.Run(ctx, worker.RunOptions{
			ConfigPath: cfgPath, HTTP: proxy.Client(), Logger: quiet(), Getenv: getenv,
			MinBackoff: 20 * time.Millisecond, MaxBackoff: 200 * time.Millisecond,
			ScopedHost: func(id int64, identity string, call workerchan.Upcall) (worker.Host, func(context.Context) (bool, error)) {
				host := remote.NewHost(machine.NewLocal(id, harness.Profile{Kind: harness.ProfileServer}, machine.LocalOps{}))
				setup := workersetup.Attach(workersetup.Options{
					Host: host, Ports: workersetup.Ports{Call: call, Creds: worker.NewCredentialStore(cfgPath, harnesses.LegacySecrets()...)},
					Logger: quiet(), StateDir: dir, CacheIdentity: identity, Getenv: func(string) string { return "" },
					Overrides: map[string]any{t3.Kind: fake},
				})
				return host, func(ctx context.Context) (bool, error) {
					err := setup.Restore(ctx)
					if errors.Is(err, workersetup.ErrNoCache) {
						return false, nil
					}
					return err == nil, err
				}
			},
		})
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("worker %s: %v", label, err)
			}
		case <-time.After(10 * time.Second):
			t.Errorf("worker %s did not stop", label)
		}
	})
	c := client.New(proxy.URL)
	c.HTTP = proxy.Client()
	awaitSharedWorkflow(t, "personal worker "+label+" to come online", func() bool {
		select {
		case err := <-done:
			t.Fatalf("worker %s stopped before ready: %v", label, err)
		default:
		}
		machines, err := c.Machines(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return slices.ContainsFunc(machines, func(m client.Machine) bool { return m.ID == joined.MachineID && m.Online })
	})
	return joined.MachineID, gateway
}

func awaitSharedWorkflow(t *testing.T, description string, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for !ready() {
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", description)
		case <-tick.C:
		}
	}
}

func assertSharedWorkflowInbox(t *testing.T, origin string, owner, member *http.Client, taskID int64) {
	t.Helper()
	find := func(h *http.Client) db.Notification {
		t.Helper()
		var page struct{ Notifications []db.Notification }
		(transportRequest{method: "GET", path: "/api/notifications"}).send(t, h, origin, http.StatusOK, &page)
		for _, row := range page.Notifications {
			if row.TaskID != nil && *row.TaskID == taskID && row.Event == db.EventNeedsInput {
				return row
			}
		}
		return db.Notification{}
	}
	var ownerItem, memberItem db.Notification
	awaitSharedWorkflow(t, "the same work event in both inboxes", func() bool {
		ownerItem, memberItem = find(owner), find(member)
		return ownerItem.ID != 0 && memberItem.ID != 0
	})
	if ownerItem.ID != memberItem.ID || ownerItem.ReadAt != nil || memberItem.ReadAt != nil {
		t.Fatalf("initial inbox entries: owner %+v, member %+v", ownerItem, memberItem)
	}
	badge := func(h *http.Client) int {
		t.Helper()
		var result struct{ Unread int }
		(transportRequest{method: "GET", path: "/api/badge"}).send(t, h, origin, http.StatusOK, &result)
		return result.Unread
	}
	ownerBefore, memberBefore := badge(owner), badge(member)
	browserPost(t, member, origin, "/api/notifications/read", fmt.Sprintf(`{"groups":["task:%d"]}`, taskID), http.StatusNoContent, nil)
	if ownerBefore < 1 || memberBefore < 1 || badge(owner) != ownerBefore || badge(member) != memberBefore-1 ||
		find(owner).ReadAt != nil || find(member).ReadAt == nil {
		t.Fatal("reading the task updated the wrong account's inbox or badge")
	}
}
