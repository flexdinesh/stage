package serve_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/config"
	"github.com/flexdinesh/stage/server/internal/db"
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
	short := isolate(t)
	upstream := freePort(t)
	proxy := proxyFor(t, upstream)
	origin := proxy.URL
	c := cfg(t, filepath.Join(short, "data"), upstream)
	c.PublicURL = origin
	mainHarness := newT3()
	setWorkflowHarnessIdentity(mainHarness.Fake, "main")
	personal := start(t, c, mainHarness)
	anon := proxy.Client()
	get := func(h *http.Client, path string, want int, out any) {
		t.Helper()
		(transportRequest{method: "GET", path: path}).send(t, h, origin, want, out)
	}
	get(anon, "/api/me", http.StatusOK, nil)

	workerHarness := &reportBackT3{Fake: t3.NewFake()}
	setWorkflowHarnessIdentity(workerHarness.Fake, "first")
	secondHarness := t3.NewFake()
	setWorkflowHarnessIdentity(secondHarness, "second")
	workspace := t.TempDir()
	remoteProject := workerHarness.AddProject("Worker project", workspace)
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
	project := reportBackProject(t, anon, origin, t3.Kind, remoteHarness.ConnectionID, remoteProject.ID, workspace)
	var previous client.Task
	browserPost(t, anon, origin, "/api/tasks", fmt.Sprintf(
		`{"title":"Personal worker task","prompt":"Run personal work","projectId":%d,"connectionId":%d,"start":true}`, project, remoteHarness.ConnectionID),
		http.StatusCreated, &previous)
	if previous.State != db.StateRunning || previous.Session == nil || previous.Session.ConnectionID != remoteHarness.ConnectionID {
		t.Fatalf("personal worker launch: %+v", previous)
	}
	if err := personal.stop(t); err != nil {
		t.Fatal(err)
	}

	c.MultiUser = new(true)
	output := &syncBuffer{}
	startSharedOwner(t, serve.Options{Config: c, BootstrapOutput: output, HarnessOverrides: map[string]any{t3.Kind: mainHarness}})
	gateway := clientFor(firstGateway)
	(transportRequest{method: "GET", path: "/api/tasks"}).send(t, gateway.HTTP, "http://stage", http.StatusForbidden, nil)
	owner, member := browserClient(t, proxy), browserClient(t, proxy)
	claimBrowser(t, owner, origin, output.String())
	var ownerAccount client.Account
	get(owner, "/api/me", http.StatusOK, &ownerAccount)
	if ownerAccount.ID != db.DefaultUser || ownerAccount.Role != "admin" {
		t.Fatalf("claim did not preserve the original user: %+v", ownerAccount)
	}
	var old client.TaskDetail
	get(owner, fmt.Sprintf("/api/tasks/%d", previous.ID), http.StatusOK, &old)
	if old.CreatedBy != ownerAccount.ID || old.Session == nil || previous.Session == nil || *old.Session != *previous.Session ||
		old.LaunchAttempt != previous.LaunchAttempt {
		t.Fatalf("upgrade changed original task: %+v", old.Task)
	}

	var invitation struct{ URL string }
	browserPost(t, owner, origin, "/api/admin/invitations", `{"email":"member@example.test"}`, http.StatusCreated, &invitation)
	token := linkToken(t, invitation.URL)
	shell := httptest.NewRecorder()
	webui.Handler().ServeHTTP(shell, httptest.NewRequest("GET", "/auth/invite", nil))
	get(anon, strings.TrimPrefix(invitation.URL, origin), shell.Code, nil)
	browserPost(t, member, origin, "/api/auth/invite/inspect", fmt.Sprintf(`{"token":%q}`, token), http.StatusOK, nil)
	browserPost(t, member, origin, "/api/auth/invite", fmt.Sprintf(`{"token":%q,"name":"Member","password":"starfish collides tonight"}`, token), http.StatusOK, nil)
	var memberAccount client.Account
	get(member, "/api/me", http.StatusOK, &memberAccount)
	if memberAccount.ID == ownerAccount.ID || memberAccount.Role != "member" {
		t.Fatalf("second account: %+v", memberAccount)
	}
	get(member, "/api/admin/accounts", http.StatusForbidden, nil)
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
	// This workflow requires the member to review and accept the result.
	(transportRequest{method: "PUT", path: fmt.Sprintf("/api/connections/%d/auto-complete", restored.ConnectionID),
		origin: origin, body: `{"autoComplete":false}`}).send(t, member, origin, http.StatusOK, nil)
	var task client.Task
	browserPost(t, owner, origin, "/api/tasks", fmt.Sprintf(`{"title":"Review worker result","prompt":"Run the fixture","projectId":%d,"connectionId":%d}`, project, restored.ConnectionID), http.StatusCreated, &task)
	var tree db.TreeDetail
	browserPost(t, owner, origin, "/api/trees", fmt.Sprintf(`{"title":"Shared launch","startMode":"manual","tasks":[{"taskId":%d}]}`, task.ID), http.StatusCreated, &tree)
	if tree.CreatedBy != ownerAccount.ID || len(tree.Tasks) != 1 || tree.Tasks[0].ID != task.ID {
		t.Fatalf("owner's tree: %+v", tree)
	}
	treePath := fmt.Sprintf("/api/trees/%d", tree.ID)
	taskPath := fmt.Sprintf("/api/tasks/%d", task.ID)
	var started client.Task
	browserPost(t, member, origin, taskPath+"/start", "", http.StatusOK, &started)
	if started.State != db.StateRunning || started.Session == nil || started.Session.ConnectionID != restored.ConnectionID ||
		started.CreatedBy != ownerAccount.ID || mainHarness.launches.Load() != 0 {
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
	awaitSharedWorkflow(t, "settled task and final reply to arrive from the worker", func() bool {
		var detail client.TaskDetail
		get(member, taskPath, http.StatusOK, &detail)
		return detail.State == db.StateDone && detail.FinalReply != nil && detail.FinalReply.Text == reply
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

	for _, account := range []*http.Client{owner, member} {
		var detail client.TaskDetail
		get(account, taskPath, http.StatusOK, &detail)
		if len(detail.Acceptances) != 1 {
			t.Fatalf("accepted result: %+v", detail.Acceptances)
		}
		result := detail.Acceptances[0]
		if result.ID != *accepted.AcceptanceID || result.Actor.Kind != "human" || result.Actor.UserID != memberAccount.ID || result.Actor.DisplayName != "Member" ||
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
			return e.EventType == "tree_created" && e.Actor.Kind == "human" && e.Actor.UserID == ownerAccount.ID
		}) || !slices.ContainsFunc(journal.Entries, func(e db.JournalEntry) bool {
			return e.ID == note.ID && e.Actor.Kind == "agent" && e.SourceMachineID != nil && *e.SourceMachineID == firstID
		}) {
			t.Fatalf("tree journal missing owner's creation or worker report: %+v", journal.Entries)
		}
	}
	assertSharedWorkflowInbox(t, origin, owner, member, task.ID)
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
			if row.TaskID != nil && *row.TaskID == taskID && row.Event == db.StateDone {
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
