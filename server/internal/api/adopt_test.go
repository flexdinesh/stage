package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/principal"
	"github.com/flexdinesh/stage/server/internal/t3"
)

type sessionRef struct {
	ConnectionID      int64  `json:"connectionId"`
	ExternalSessionID string `json:"externalSessionId"`
}

type agentTask struct {
	task
	OriginSession      *sessionRef `json:"originSession"`
	OriginSessionTitle *string     `json:"originSessionTitle"`
}

// t3Session is a T3 session reference body (needsSession, originSession).
func (e *env) t3Session(id string) map[string]any {
	return map[string]any{"connectionId": e.store.ActiveID(db.LocalMachine, "t3"), "externalSessionId": id}
}

// isT3Session reports whether ref names T3 session id.
func (e *env) isT3Session(ref *sessionRef, id string) bool {
	return ref != nil && ref.ConnectionID == e.store.ActiveID(db.LocalMachine, "t3") && ref.ExternalSessionID == id
}

func (e *env) agentTask(projectID int64, title string, extra map[string]any, want int) agentTask {
	e.t.Helper()
	var out agentTask
	e.must("POST", "/api/tasks", e.taskBody(projectID, title, extra), want, &out)
	return out
}

func (e *env) getAgentTask(id int64) agentTask {
	e.t.Helper()
	var out agentTask
	e.must("GET", fmt.Sprintf("/api/tasks/%d", id), nil, 200, &out)
	return out
}

// launchSession launches an external (non-task) running thread in T3 only
// (not mirrored).
func launchSession(t *testing.T, e *env, project, title string) string {
	t.Helper()
	out, err := e.fake.LaunchThread(context.Background(), t3.LaunchInput{ProjectID: project, Title: title})
	if err != nil {
		t.Fatal(err)
	}
	return out.ThreadID
}

// runningSession launches an external (non-task) thread that is running
// and mirrors it.
func runningSession(t *testing.T, e *env, project, title string) string {
	t.Helper()
	id := launchSession(t, e, project, title)
	if err := e.engine.Coordinator.RefreshScopedSession(context.Background(), execution.SessionRef{ConnectionID: e.store.ActiveID(db.LocalMachine, "t3"), ExternalSessionID: id}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAdoptSession(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/adopt")
	// Not mirrored yet: adopt reads it from T3 first.
	s := launchSession(t, e, p.T3ProjectID, "Planner")
	if err := e.fake.SetThread(s, func(d *t3.ThreadDetail) {
		d.ProviderInstanceID = "codex"
		d.Model = "model-from-session"
		d.RuntimeMode = "auto"
		d.InteractionMode = "plan"
	}); err != nil {
		t.Fatal(err)
	}

	var a agentTask
	e.must("POST", "/api/connections/1/sessions/"+s+"/adopt", map[string]any{"initialNote": "n"}, 201, &a)
	if c := a.Config; c == nil || c.Model != t3.ModelKey("codex", "model-from-session") || c.Permission != "auto" || c.Mode != "plan" {
		t.Fatalf("adoption lost the session's execution config: %+v", a.Config)
	}
	if !a.Adopted || a.Prompt != "" || a.Title != "Planner" || a.State != "running" || a.ProjectID != p.ID ||
		a.ExternalSessionID == nil || *a.ExternalSessionID != s || a.LaunchAttempt != 1 || a.TreeID != nil {
		t.Fatalf("adopted %+v", a)
	}
	events, err := e.store.TaskEvents(t.Context(), a.ID)
	if err != nil || len(events) != 1 || events[0].Actor.Kind != "personal" || events[0].Actor.UserID != 0 {
		t.Fatalf("adopted task attribution: %+v, %v", events, err)
	}
	var again agentTask
	e.must("POST", "/api/connections/1/sessions/"+s+"/adopt", map[string]any{"title": "ignored"}, 200, &again)
	if again.ID != a.ID || again.Title != "Planner" {
		t.Fatalf("adopt again %+v", again)
	}
	var sc struct {
		Task *agentTask `json:"task"`
	}
	e.must("GET", "/api/connections/1/sessions/"+s+"/context", nil, 200, &sc)
	if sc.Task == nil || sc.Task.ID != a.ID {
		t.Fatalf("session now owned %+v", sc.Task)
	}

	// Stop works; start and duplicate do not.
	e.must("POST", fmt.Sprintf("/api/tasks/%d/stop", a.ID), nil, 200, nil)
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", a.ID), nil, 409, nil)
	e.must("POST", fmt.Sprintf("/api/tasks/%d/duplicate", a.ID), nil, 409, nil)

	// Its state follows the session (polled like any task).
	_ = e.fake.SetStatus(s, t3.StatusCompleted)
	e.poll()
	if got := e.getTask(a.ID); got.State != "done" {
		t.Fatalf("adopted task after completion %s", got.State)
	}
	_ = e.fake.Settle(s)
	e.poll()
	if got := e.getTask(a.ID); got.State != "done" {
		t.Fatalf("adopted task after settle %s", got.State)
	}

	// Refusals.
	e.must("POST", "/api/connections/1/sessions/nope/adopt", nil, 404, nil)
	sub := runningSession(t, e, p.T3ProjectID, "Sub")
	_ = e.fake.SetThread(sub, func(d *t3.ThreadDetail) { d.ParentThreadID, d.RelationshipToParent = s, "subagent" })
	e.must("POST", "/api/connections/1/sessions/"+sub+"/adopt", nil, 409, nil)
	other := runningSession(t, e, p.T3ProjectID, "Other")
	e.must("POST", "/api/connections/1/sessions/"+other+"/adopt", map[string]any{"treeId": 9999}, 404, nil)
}

type adoptionPreview struct {
	Session           db.SessionRef
	Task              *db.Task
	Project           *db.Project
	Title             string
	Workspace         string
	UnavailableReason string
}

func TestConcurrentHumanSessionTracking(t *testing.T) {
	e, _, accounts, owner, historical := sharedNotificationEnv(t)
	member := addNotificationMember(t, accounts)
	ctx := t.Context()
	external := e.fake.AddProject("Native workspace", t.TempDir())
	id := launchSession(t, e, external.ID, "Unbound session")
	ref := db.SessionRef{ConnectionID: e.store.ActiveID(db.LocalMachine, t3.Kind), ExternalSessionID: id}
	path := rosterPath(ref.ConnectionID) + "/" + id

	var preview adoptionPreview
	notifyRequest(t, e, owner, http.MethodGet, path+"/context", "", 200, &preview)
	resolve := fmt.Sprintf("/api/refs/resolve?connectionId=%d&ref=%s", ref.ConnectionID, url.QueryEscape(id))
	notifyRequest(t, e, member, http.MethodGet, resolve, "", 200, nil)
	if preview.Session != ref || preview.Task != nil || preview.Project != nil || preview.Title != "Unbound session" ||
		preview.Workspace != external.WorkspaceRoot || preview.UnavailableReason != "" {
		t.Fatalf("preview: %+v", preview)
	}
	if _, err := e.store.Observation(ctx, ref); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("preview recorded an observation: %v", err)
	}
	if rows, err := e.store.Workspaces(ctx, ref.ConnectionID); err != nil || len(rows) != 0 {
		t.Fatalf("preview changed the workspace catalog: %+v, %v", rows, err)
	}
	inbox, err := e.store.ListNotifications(db.WithUser(ctx, owner.Account.ID), 10, nil)
	if err != nil || len(inbox) != 1 || inbox[0].ID != historical || inbox[0].ReadAt != nil {
		t.Fatalf("preview changed notifications or read receipts: %+v, %v", inbox, err)
	}

	type result struct {
		user int64
		note string
		code int
		body []byte
	}
	results := make(chan result, 2)
	shared := privateAdoptionHandler(e)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	browsers := []struct {
		user, token, note string
		id                int64
	}{
		{"Owner", owner.Token.Reveal(), "Owner note", owner.Account.ID},
		{"Member", member.Token.Reveal(), "Member note", member.Account.ID},
	}
	for _, browser := range browsers {
		reached := make(chan struct{}, 1)
		e.t3.listGate.Store(&listGate{release, reached})
		go func() {
			rec := raw(t, shared, http.MethodPost, notificationOrigin+path+"/adopt", map[string]string{
				"Cookie": "stage_session=" + browser.token,
				"Origin": notificationOrigin,
			}, map[string]string{"title": browser.user, "initialNote": browser.note}, nil)
			results <- result{browser.id, browser.note, rec.Code, rec.Body.Bytes()}
		}()
		select {
		case <-reached:
		case early := <-results:
			t.Fatalf("confirmation returned before live verification: %d %s", early.code, early.body)
		case <-time.After(10 * time.Second):
			t.Fatal("confirmation did not reach the harness")
		}
	}
	close(release)

	var winner, loser result
	for range browsers {
		r := <-results
		switch r.code {
		case http.StatusCreated:
			winner = r
		case http.StatusOK:
			loser = r
		default:
			t.Fatalf("unexpected confirmation response: %d %s", r.code, r.body)
		}
	}
	if winner.user == 0 || loser.user == 0 {
		t.Fatalf("expected one creation and one existing task: %+v %+v", winner, loser)
	}
	var created, existing db.Task
	if err := json.Unmarshal(winner.body, &created); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(loser.body, &existing); err != nil {
		t.Fatal(err)
	}
	if created.ID != existing.ID || winner.user != created.CreatedBy || created.CreatedBy != existing.CreatedBy ||
		created.Title != existing.Title || created.BindingID != nil || created.Session == nil || *created.Session != ref {
		t.Fatalf("task attribution or ownership: %+v %+v", created, existing)
	}
	events, err := e.store.TaskEvents(ctx, created.ID)
	if err != nil || len(events) != 1 || events[0].Detail != "adopted session" || events[0].Actor.UserID != winner.user {
		t.Fatalf("adoption event: %+v, %v", events, err)
	}
	journal, err := e.store.ReadJournal(ctx, db.JournalOwner{TaskID: &created.ID}, db.JournalQuery{Kind: "notes"})
	if err != nil || len(journal.Entries) != 1 || journal.Entries[0].Text != winner.note || journal.Entries[0].CreatedBy != winner.user {
		t.Fatalf("initial note: %+v, %v", journal, err)
	}
	projects, err := e.store.ListProjects(ctx, "all")
	if err != nil || len(projects) != 1 || projects[0].ID != created.ProjectID || projects[0].Title != external.Title ||
		projects[0].CreatedBy != winner.user || len(projects[0].Bindings) != 0 {
		t.Fatalf("historical project: %+v, %v", projects, err)
	}
	second := rosterPath(ref.ConnectionID) + "/" + launchSession(t, e, external.ID, "Same workspace")
	preview = adoptionPreview{}
	notifyRequest(t, e, member, http.MethodGet, second+"/context", "", 200, &preview)
	if preview.Project == nil || preview.Project.ID != created.ProjectID {
		t.Fatalf("preview did not reuse the historical project: %+v", preview)
	}
	var next db.Task
	notifyRequest(t, e, member, http.MethodPost, second+"/adopt", "{}", 201, &next)
	if next.ProjectID != created.ProjectID || next.ID == created.ID || next.BindingID != nil {
		t.Fatalf("repeat adoption in the same workspace: %+v", next)
	}
	tree, err := e.store.CreateTree(ctx, db.NewTree{Title: "Explicit tree", Tasks: []db.TreeMember{{TaskID: next.ID}}})
	tree = mustValue(t, tree, err)
	e.t3.failCatalog.Store(true)
	var joined db.Task
	notifyRequest(t, e, member, http.MethodPost, path+"/adopt", fmt.Sprintf(`{"treeId":%d}`, tree.ID), 200, &joined)
	if joined.ID != created.ID || joined.TreeID == nil || *joined.TreeID != tree.ID {
		t.Fatalf("existing task did not join the explicit tree: %+v", joined)
	}
}

func TestScopedAgentNamedSessionAdoption(t *testing.T) {
	e, _, accounts, _, _ := sharedNotificationEnv(t)
	ctx := t.Context()
	member := addNotificationMember(t, accounts)
	cred, err := accounts.RotateAgent(ctx, member.Token.Reveal(), store.SessionBrowser)
	bearer := "Bearer " + principal.AgentTokenPrefix + mustValue(t, cred, err).Token.Reveal()
	headers := map[string]string{"Authorization": bearer}
	shared := privateAdoptionHandler(e)
	connection := e.store.ActiveID(db.LocalMachine, t3.Kind)
	external := e.fake.AddProject("Agent workspace", t.TempDir())
	project, err := e.store.InsertProject(ctx, db.Project{Title: "Agent project"})
	project = mustValue(t, project, err)
	session := launchSession(t, e, external.ID, "Agent's named session")
	path := notificationOrigin + rosterPath(connection) + "/" + session
	var preview adoptionPreview
	w := raw(t, shared, http.MethodGet, path+"/context", headers, nil, &preview)
	if w.Code != 200 || preview.Task != nil || preview.Title != "Agent's named session" || preview.Project != nil {
		t.Fatalf("named lookup: %d %s", w.Code, w.Body)
	}
	w = raw(t, shared, http.MethodPost, path+"/adopt", headers, map[string]string{}, nil)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "Choose a Stage project or supply newProjectTitle") {
		t.Fatalf("scoped agent without a bound project: %d %s", w.Code, w.Body)
	}
	var adopted db.Task
	w = raw(t, shared, http.MethodPost, path+"/adopt", headers, map[string]any{"projectId": project.ID, "initialNote": "Via agent"}, &adopted)
	if w.Code != 201 || adopted.ProjectID != project.ID || adopted.CreatedBy != member.Account.ID ||
		adopted.Session == nil || adopted.Session.ExternalSessionID != session {
		t.Fatalf("agent adoption: %d %s", w.Code, w.Body)
	}
	bound, err := e.store.BoundProject(ctx, db.ProjectBinding{ConnectionID: connection, ExternalProjectID: external.ID, Workspace: external.WorkspaceRoot})
	if err != nil || bound.ID != project.ID {
		t.Fatalf("explicit legacy binding: %+v, %v", bound, err)
	}
}

func privateAdoptionHandler(e *env) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = "127.0.0.1:32101"
		e.h.ServeHTTP(w, r)
	})
}

func TestSessionTrackingRevalidatesLiveGuards(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		change       func(*env, string, string) error
	}{
		{"parent only", "top-level", func(e *env, id, _ string) error {
			return e.fake.SetThread(id, func(d *t3.ThreadDetail) { d.ParentThreadID = "parent" })
		}},
		{"archived session", "session is archived", func(e *env, id, _ string) error {
			return e.fake.SetThread(id, func(d *t3.ThreadDetail) { d.Archived = true })
		}},
		{"archived workspace", "workspace is unavailable", func(e *env, _, project string) error {
			now := time.Now()
			return e.fake.SetProject(project, func(p *t3.Project) { p.ArchivedAt = &now })
		}},
		{"deleted workspace", "workspace is unavailable", func(e *env, _, project string) error {
			now := time.Now()
			return e.fake.SetProject(project, func(p *t3.Project) { p.DeletedAt = &now })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			connection := e.store.ActiveID(db.LocalMachine, t3.Kind)
			external := e.fake.AddProject("Native workspace", t.TempDir())
			id := launchSession(t, e, external.ID, "Session")
			path := rosterPath(connection) + "/" + id
			ref := db.SessionRef{ConnectionID: connection, ExternalSessionID: id}
			thread, err := e.fake.ReadThread(t.Context(), id)
			o := mustValue(t, thread, err).Observation()
			o.Session, o.UpdatedAt = ref, time.Now().Add(time.Hour)
			if err := e.store.PutObservation(t.Context(), o); err != nil {
				t.Fatal(err)
			}
			var preview adoptionPreview
			e.must(http.MethodGet, path+"/context", nil, 200, &preview)
			if preview.UnavailableReason != "" {
				t.Fatal(preview.UnavailableReason)
			}
			if err := tc.change(e, id, external.ID); err != nil {
				t.Fatal(err)
			}
			var failure errOut
			e.must(http.MethodPost, path+"/adopt", nil, 409, &failure)
			if !strings.Contains(failure.Error, tc.reason) {
				t.Fatalf("confirm: %+v", failure)
			}
			e.must(http.MethodGet, path+"/context", nil, 200, &preview)
			if !strings.Contains(preview.UnavailableReason, tc.reason) {
				t.Fatalf("preview: %+v", preview)
			}
			if _, err := e.store.TaskBySession(t.Context(), ref); !errors.Is(err, db.ErrNotFound) {
				t.Fatalf("unavailable session was tracked: %v", err)
			}
		})
	}
}

func TestSessionTrackingRejectsRestampedConnection(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	connection := e.store.ActiveID(db.LocalMachine, t3.Kind)
	external := e.fake.AddProject("Workspace", t.TempDir())
	id := launchSession(t, e, external.ID, "Previous instance")
	ref := db.SessionRef{ConnectionID: connection, ExternalSessionID: id}
	path := rosterPath(connection) + "/" + id + "/adopt"
	gate, reached := make(chan struct{}), make(chan struct{}, 1)
	e.t3.listGate.Store(&listGate{gate, reached})
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		result <- raw(t, e.h, http.MethodPost, path, nil, map[string]any{}, nil)
	}()
	select {
	case <-reached:
	case early := <-result:
		close(gate)
		t.Fatalf("confirmation returned before observation: %d %s", early.Code, early.Body)
	case <-time.After(10 * time.Second):
		close(gate)
		t.Fatal("confirmation did not reach the harness")
	}
	switched, err := e.store.SwitchInstance(ctx, db.LocalMachine, t3.Descriptor, "replacement", "Replacement", 0)
	close(gate)
	if err != nil || switched.New != connection {
		t.Fatalf("test requires an in-place connection restamp: %+v, %v", switched, err)
	}
	rec := <-result
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "harness connection changed") {
		t.Fatalf("replacement connection: %d %s", rec.Code, rec.Body)
	}
	if _, err := e.store.Observation(ctx, ref); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("old instance observation was recorded on the replacement: %v", err)
	}
}

func TestSessionTrackingDisambiguatesOnlyExactReportedRoot(t *testing.T) {
	e := newEnv(t)
	connection := e.store.ActiveID(db.LocalMachine, t3.Kind)
	first := e.fake.AddProject("First", "/repo/first")
	second := e.fake.AddProject("Second", "/repo/second")
	if err := e.fake.SetProject(second.ID, func(p *t3.Project) { p.ID = first.ID }); err != nil {
		t.Fatal(err)
	}
	id := launchSession(t, e, first.ID, "Session in second root")
	path := rosterPath(connection) + "/" + id
	var preview adoptionPreview
	e.must(http.MethodGet, path+"/context", nil, 200, &preview)
	if !strings.Contains(preview.UnavailableReason, "ambiguous") {
		t.Fatalf("ambiguous native ID: %+v", preview)
	}
	e.must(http.MethodPost, path+"/adopt", nil, 409, nil)

	if err := e.fake.SetThread(id, func(d *t3.ThreadDetail) { d.WorktreePath = second.WorkspaceRoot }); err != nil {
		t.Fatal(err)
	}
	preview = adoptionPreview{}
	e.must(http.MethodGet, path+"/context", nil, 200, &preview)
	if preview.Workspace != second.WorkspaceRoot || preview.UnavailableReason != "" {
		t.Fatalf("exact native root: %+v", preview)
	}
	var adopted db.Task
	e.must(http.MethodPost, path+"/adopt", nil, 201, &adopted)
	p, err := e.store.GetProject(t.Context(), adopted.ProjectID)
	if err != nil || p.Title != second.Title || len(p.Bindings) != 0 {
		t.Fatalf("exact workspace project: %+v, %v", p, err)
	}
}

func TestAdoptionCreatesBindingAtomically(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	connection := e.store.ActiveID(db.LocalMachine, t3.Kind)
	external := e.fake.AddProject("External", t.TempDir())
	if err := e.fake.SetProject(external.ID, func(p *t3.Project) { p.Local = true }); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.RefreshWorkspaces(ctx, connection, []execution.WorkspaceInfo{external}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.RefreshWorkspaces(ctx, connection, nil); err != nil {
		t.Fatal(err)
	}
	session := launchSession(t, e, external.ID, "Planner")
	path := "/api/connections/1/sessions/" + session + "/adopt"
	e.must("POST", path, map[string]any{"newProjectTitle": "Plan", "treeId": 9999}, 404, nil)
	var projects struct {
		Projects []project `json:"projects"`
	}
	e.must("GET", "/api/projects", nil, 200, &projects)
	if len(projects.Projects) != 0 {
		t.Fatal("failed adoption left a project", projects)
	}
	if workspaces, err := e.store.Workspaces(ctx, connection); err != nil || len(workspaces) != 0 {
		t.Fatalf("failed adoption changed the inventory: %+v, %v", workspaces, err)
	}
	var adopted agentTask
	e.must("POST", path, map[string]any{"newProjectTitle": "Plan"}, 201, &adopted)
	e.must("POST", path, map[string]any{"newProjectTitle": "Duplicate"}, 200, nil)
	e.must("GET", "/api/projects", nil, 200, &projects)
	if len(projects.Projects) != 1 || projects.Projects[0].ID != adopted.ProjectID || projects.Projects[0].Title != "Plan" {
		t.Fatal("adoption duplicated project", projects)
	}
	workspaces, err := e.store.Workspaces(ctx, connection)
	if err != nil || len(workspaces) != 1 {
		t.Fatalf("adoption did not refresh the inventory: %+v, %v", workspaces, err)
	}
	w := workspaces[0]
	if w.ExternalID != external.ID || w.Workspace != external.WorkspaceRoot || w.ProjectID == nil || *w.ProjectID != adopted.ProjectID ||
		w.FileHost == nil || *w.FileHost != db.LocalMachine || w.EvidenceSource != "machine" || w.CanonicalPath == "" {
		t.Fatalf("adoption did not record the bound workspace with machine evidence: %+v", w)
	}
}

// needsSession (agent afterMe) adopts the caller's session and needs it.
func TestTaskNeedsSession(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/after-session")
	s := runningSession(t, e, p.T3ProjectID, "Agent session")

	e.agentTask(p.ID, "x", map[string]any{"needsSession": e.t3Session(s), "startMode": "auto", "start": true}, 400)
	e.agentTask(p.ID, "y", map[string]any{"needsSession": e.t3Session(s), "startMode": "auto", "startLocal": "2999-01-01T10:00", "startTz": "UTC"}, 400)
	// Removed v5 inputs are rejected.
	e.agentTask(p.ID, "z", map[string]any{"needsSessionId": s, "startMode": "auto"}, 400)
	e.agentTask(p.ID, "z", map[string]any{"originThreadId": s}, 400)
	a := e.agentTask(p.ID, "a", map[string]any{"needsSession": e.t3Session(s), "startMode": "auto", "originSession": e.t3Session(s)}, 201)
	b := e.agentTask(p.ID, "b", map[string]any{"needsSession": e.t3Session(s), "startMode": "auto"}, 201) // fan-out
	if a.State != "backlog" || len(a.Needs) != 1 || a.Readiness == nil || *a.Readiness != "blocked" ||
		!e.isT3Session(a.OriginSession, s) || a.OriginSessionTitle == nil || *a.OriginSessionTitle != "Agent session" {
		t.Fatalf("a %+v", a)
	}
	caller := e.getTask(a.Needs[0])
	if !caller.Adopted || caller.ExternalSessionID == nil || *caller.ExternalSessionID != s || caller.State != "running" ||
		!slices.Equal(b.Needs, a.Needs) || b.TreeID == nil || *b.TreeID != *a.TreeID || caller.Tree == nil || caller.Tree.Title != "Agent session" {
		t.Fatalf("caller task %+v, b %+v", caller, b)
	}

	// Done but unsettled, then failed: keeps waiting.
	_ = e.fake.SetStatus(s, t3.StatusCompleted)
	e.poll()
	e.reconcile()
	if got := e.getTask(a.ID); got.State != "backlog" {
		t.Fatalf("unsettled session must hold: %+v", got)
	}
	_ = e.fake.SetStatus(s, t3.StatusFailed)
	e.poll()
	e.reconcile()
	if got := e.getTask(b.ID); got.State != "backlog" || got.Readiness == nil || *got.Readiness != "blocked" {
		t.Fatalf("failed session must hold: %+v", got)
	}

	_ = e.fake.SetStatus(s, t3.StatusCompleted)
	_ = e.fake.Settle(s)
	e.poll()
	e.complete(a.Needs[0])
	e.reconcile()
	for _, id := range []int64{a.ID, b.ID} {
		got := e.getTask(id)
		if got.State != "running" || got.ExternalSessionID == nil || *got.ExternalSessionID == s || len(got.Needs) != 1 {
			t.Fatalf("after settle task %d must run, keeping its needs: %+v", id, got)
		}
	}

	// A settled session can still be needed (it releases at once); unknown
	// sessions are 404.
	c := e.agentTask(p.ID, "c", map[string]any{"needsSession": e.t3Session(s), "startMode": "auto"}, 201)
	e.reconcile()
	if got := e.getTask(c.ID); got.State != "running" {
		t.Fatalf("needing a settled session releases: %+v", got)
	}
	e.agentTask(p.ID, "d", map[string]any{"needsSession": e.t3Session("nope")}, 404)
}

func TestCancelledSessionHoldsUntilSettled(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/after-cancelled-session")
	s := runningSession(t, e, p.T3ProjectID, "Agent session")
	a := e.agentTask(p.ID, "a", map[string]any{"needsSession": e.t3Session(s), "startMode": "auto"}, 201)

	// Cancelled: keeps waiting, and can still be needed.
	_ = e.fake.SetStatus(s, t3.StatusCancelled)
	e.poll()
	e.reconcile()
	if got := e.getTask(a.ID); got.State != "backlog" {
		t.Fatalf("cancelled session must hold: %+v", got)
	}
	if got := e.getTask(a.Needs[0]); got.State != "cancelled" {
		t.Fatalf("caller task %s", got.State)
	}
	b := e.agentTask(p.ID, "b", map[string]any{"needsSession": e.t3Session(s), "startMode": "auto"}, 201)

	// Settled with the last run still cancelled: passes.
	_ = e.fake.Settle(s)
	if _, err := e.engine.Discovery.FullSync(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.poll()
	e.complete(a.Needs[0])
	e.reconcile()
	for _, id := range []int64{a.ID, b.ID} {
		if got := e.getTask(id); got.State != "running" {
			t.Fatalf("after settle task %d must run: %+v", id, got)
		}
	}
}

func TestNeedsSessionOwnedByTask(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/after-task-session")
	a := e.agentTask(p.ID, "a", map[string]any{"start": true}, 201)
	b := e.agentTask(p.ID, "b", map[string]any{"needsSession": e.t3Session(*a.ExternalSessionID)}, 201)
	c := e.agentTask(p.ID, "c", map[string]any{"needsSession": e.t3Session(*a.ExternalSessionID)}, 201)
	if !slices.Equal(b.Needs, []int64{a.ID}) || !slices.Equal(c.Needs, []int64{a.ID}) || b.Adopted {
		t.Fatalf("b and c need task %d: %+v %+v", a.ID, b, c)
	}
	if got := e.getTask(a.ID); got.Adopted || !slices.Equal(got.Dependents, []int64{b.ID, c.ID}) {
		t.Fatalf("a %+v", got)
	}
}

func TestScheduleOriginInherited(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/schedule-origin")
	s := runningSession(t, e, p.T3ProjectID, "S")
	var sc struct {
		ID            int64       `json:"id"`
		OriginSession *sessionRef `json:"originSession"`
	}
	e.must("POST", "/api/schedules", map[string]any{
		"projectId": p.ID, "title": "nightly", "prompt": "go", "cron": "0 2 * * *", "tz": "UTC", "originThreadId": s,
	}, 400, nil)
	e.must("POST", "/api/schedules", map[string]any{
		"projectId": p.ID, "connectionId": 1, "workspaceId": p.workspaceID(), "title": "nightly", "prompt": "go", "cron": "0 2 * * *", "tz": "UTC", "originSession": e.t3Session(s),
	}, 201, &sc)
	if !e.isT3Session(sc.OriginSession, s) {
		t.Fatalf("schedule %+v", sc)
	}
	e.must("POST", "/api/schedules", map[string]any{
		"projectId": p.ID, "connectionId": 1, "workspaceId": p.workspaceID(), "title": "n", "prompt": "go", "cron": "0 2 * * *", "tz": "UTC", "needsSession": e.t3Session(s), "startMode": "auto",
	}, 400, nil)
	var spawned agentTask
	e.must("POST", fmt.Sprintf("/api/schedules/%d/run", sc.ID), nil, 201, &spawned)
	if !e.isT3Session(spawned.OriginSession, s) || spawned.TreeID != nil {
		t.Fatalf("spawned %+v", spawned)
	}
	// ?schedule= lists only the tasks that schedule spawned.
	e.agentTask(p.ID, "unscheduled", nil, 201)
	var rows struct {
		Tasks []task `json:"tasks"`
	}
	e.must("GET", fmt.Sprintf("/api/tasks?schedule=%d", sc.ID), nil, 200, &rows)
	if len(rows.Tasks) != 1 || rows.Tasks[0].ID != spawned.ID {
		t.Fatalf("schedule filter %+v", rows.Tasks)
	}
	e.must("GET", fmt.Sprintf("/api/tasks?schedule=%d", sc.ID+100), nil, 200, &rows)
	if len(rows.Tasks) != 0 {
		t.Fatalf("unknown schedule filter %+v", rows.Tasks)
	}
	e.must("GET", "/api/tasks?schedule=x", nil, 400, nil)
}

func TestTaskDetailAndOriginSessionFilter(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/origin")
	s := runningSession(t, e, p.T3ProjectID, "Planner")
	a := e.agentTask(p.ID, "a", map[string]any{"originSession": e.t3Session(s), "needsSession": e.t3Session(s), "startMode": "auto"}, 201)
	b := e.agentTask(p.ID, "b", map[string]any{"originSession": e.t3Session(s)}, 201)
	c := e.agentTask(p.ID, "c", map[string]any{"originSession": e.t3Session(s), "needs": []int64{a.ID}}, 201)
	solo := e.agentTask(p.ID, "solo", nil, 201)

	var rows struct {
		Tasks []task `json:"tasks"`
	}
	e.must("GET", "/api/tasks?originThreadId="+s, nil, 400, nil)
	e.must("GET", fmt.Sprintf("/api/tasks?originConnectionId=%d&originSessionId=%s", e.store.ActiveID(db.LocalMachine, "t3"), url.QueryEscape(s)), nil, 200, &rows)
	ids := make([]int64, 0, len(rows.Tasks))
	for _, row := range rows.Tasks {
		ids = append(ids, row.ID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []int64{a.ID, b.ID, c.ID}) {
		t.Fatalf("tasks created by %s: %v", s, ids)
	}

	for _, want := range []agentTask{c, b, solo} {
		var detail struct {
			agentTask
			Batch json.RawMessage `json:"batch"`
		}
		e.must("GET", taskPath(want.ID), nil, 200, &detail)
		if detail.Batch != nil {
			t.Fatalf("task %d has batch field", want.ID)
		}
		if detail.ID != want.ID || (want.ID == solo.ID && detail.OriginSession != nil) ||
			(want.ID != solo.ID && !e.isT3Session(detail.OriginSession, s)) {
			t.Fatalf("task %d detail: %+v", want.ID, detail.agentTask)
		}
		if want.ID == c.ID {
			if detail.Tree == nil || c.TreeID == nil || detail.Tree.ID != *c.TreeID ||
				!slices.ContainsFunc(detail.Tree.Tasks, func(task task) bool { return task.ID == a.ID }) {
				t.Fatalf("task %d tree: %+v", want.ID, detail.Tree)
			}
		} else if detail.Tree != nil {
			t.Fatalf("task %d has unexpected tree: %+v", want.ID, detail.Tree)
		}
	}
}
