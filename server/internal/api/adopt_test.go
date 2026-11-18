package api_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/flexdinesh/stage/server/internal/t3"
)

type agentTask struct {
	task
	OriginThreadID    *string `json:"originThreadId"`
	OriginThreadTitle *string `json:"originThreadTitle"`
}

func (e *env) agentTask(projectID int64, title string, extra map[string]any, want int) agentTask {
	e.t.Helper()
	body := map[string]any{"projectId": projectID, "title": title, "prompt": "reply OK"}
	for k, v := range extra {
		body[k] = v
	}
	var out agentTask
	e.must("POST", "/api/tasks", body, want, &out)
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
	if err := e.sync.RefreshSession(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAdoptSession(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/adopt")
	// Not mirrored yet: adopt reads it from T3 first.
	s := launchSession(t, e, p.T3ProjectID, "Planner")

	var a agentTask
	e.must("POST", "/api/connections/1/sessions/"+s+"/adopt", map[string]any{"initialNote": "n", "tags": []string{" plan ", ""}}, 201, &a)
	if !a.Adopted || a.Prompt != "" || a.Title != "Planner" || a.State != "running" || a.ProjectID != p.ID ||
		a.T3ThreadID == nil || *a.T3ThreadID != s || a.LaunchAttempt != 1 || !slices.Equal(a.Tags, []string{"plan"}) || a.TreeID != nil {
		t.Fatalf("adopted %+v", a)
	}
	var again agentTask
	e.must("POST", "/api/connections/1/sessions/"+s+"/adopt", map[string]any{"title": "ignored"}, 200, &again)
	if again.ID != a.ID || again.Title != "Planner" {
		t.Fatalf("adopt again %+v", again)
	}
	var td struct {
		Thread thread `json:"thread"`
	}
	e.must("GET", "/api/connections/1/sessions/"+s, nil, 200, &td)
	if th := td.Thread; th.TaskID == nil || *th.TaskID != a.ID || th.Source != "app" {
		t.Fatalf("session now owned %+v", th)
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
	if got := e.getTask(a.ID); got.State != "settled" {
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

func TestAdoptionCreatesBindingAtomically(t *testing.T) {
	e := newEnv(t)
	external := e.fake.AddProject("External", "/adoption")
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
	var adopted agentTask
	e.must("POST", path, map[string]any{"newProjectTitle": "Plan"}, 201, &adopted)
	e.must("POST", path, map[string]any{"newProjectTitle": "Duplicate"}, 200, nil)
	e.must("GET", "/api/projects", nil, 200, &projects)
	if len(projects.Projects) != 1 || projects.Projects[0].ID != adopted.ProjectID || projects.Projects[0].Title != "Plan" {
		t.Fatal("adoption duplicated project", projects)
	}
}

// needsSessionId (agent afterMe) adopts the caller's session and needs it.
func TestTaskNeedsSession(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/after-session")
	s := runningSession(t, e, p.T3ProjectID, "Agent session")

	e.agentTask(p.ID, "x", map[string]any{"needsSessionId": s, "startMode": "auto", "start": true}, 400)
	e.agentTask(p.ID, "y", map[string]any{"needsSessionId": s, "startMode": "auto", "startLocal": "2999-01-01T10:00", "startTz": "UTC"}, 400)
	a := e.agentTask(p.ID, "a", map[string]any{"needsSessionId": s, "startMode": "auto", "originThreadId": s}, 201)
	b := e.agentTask(p.ID, "b", map[string]any{"needsSessionId": s, "startMode": "auto"}, 201) // fan-out
	if a.State != "backlog" || len(a.Needs) != 1 || a.Readiness == nil || *a.Readiness != "blocked" ||
		a.OriginThreadID == nil || *a.OriginThreadID != s || a.OriginThreadTitle == nil || *a.OriginThreadTitle != "Agent session" {
		t.Fatalf("a %+v", a)
	}
	caller := e.getTask(a.Needs[0])
	if !caller.Adopted || caller.T3ThreadID == nil || *caller.T3ThreadID != s || caller.State != "running" ||
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
		if got.State != "running" || got.T3ThreadID == nil || *got.T3ThreadID == s || len(got.Needs) != 1 {
			t.Fatalf("after settle task %d must run, keeping its needs: %+v", id, got)
		}
	}

	// A settled session can still be needed (it releases at once); unknown
	// sessions are 404.
	c := e.agentTask(p.ID, "c", map[string]any{"needsSessionId": s, "startMode": "auto"}, 201)
	e.reconcile()
	if got := e.getTask(c.ID); got.State != "running" {
		t.Fatalf("needing a settled session releases: %+v", got)
	}
	e.agentTask(p.ID, "d", map[string]any{"needsSessionId": "nope"}, 404)
}

func TestCancelledSessionHoldsUntilSettled(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/after-cancelled-session")
	s := runningSession(t, e, p.T3ProjectID, "Agent session")
	a := e.agentTask(p.ID, "a", map[string]any{"needsSessionId": s, "startMode": "auto"}, 201)

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
	b := e.agentTask(p.ID, "b", map[string]any{"needsSessionId": s, "startMode": "auto"}, 201)

	// Settled with the last run still cancelled: passes.
	_ = e.fake.Settle(s)
	if _, err := e.sync.FullSync(context.Background()); err != nil {
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
	b := e.agentTask(p.ID, "b", map[string]any{"needsSessionId": *a.T3ThreadID}, 201)
	c := e.agentTask(p.ID, "c", map[string]any{"needsSessionId": *a.T3ThreadID}, 201)
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
		ID             int64   `json:"id"`
		OriginThreadID *string `json:"originThreadId"`
	}
	e.must("POST", "/api/schedules", map[string]any{
		"projectId": p.ID, "title": "nightly", "prompt": "go", "cron": "0 2 * * *", "tz": "UTC", "originThreadId": s,
	}, 201, &sc)
	if sc.OriginThreadID == nil || *sc.OriginThreadID != s {
		t.Fatalf("schedule %+v", sc)
	}
	e.must("POST", "/api/schedules", map[string]any{
		"projectId": p.ID, "title": "n", "prompt": "go", "cron": "0 2 * * *", "tz": "UTC", "needsSessionId": s, "startMode": "auto",
	}, 400, nil)
	var spawned agentTask
	e.must("POST", fmt.Sprintf("/api/schedules/%d/run", sc.ID), nil, 201, &spawned)
	if spawned.OriginThreadID == nil || *spawned.OriginThreadID != s || spawned.TreeID != nil {
		t.Fatalf("spawned %+v", spawned)
	}
}

type createdTasks struct {
	Trees []struct {
		Tree struct {
			ID        int64  `json:"id"`
			Title     string `json:"title"`
			StartMode string `json:"startMode"`
		} `json:"tree"`
		Tasks []agentTask `json:"tasks"`
	} `json:"trees"`
	Independent []agentTask `json:"independent"`
}

func batchIDs(c createdTasks) ([][]int64, []int64) {
	trees := make([][]int64, len(c.Trees))
	for i, tr := range c.Trees {
		for _, t := range tr.Tasks {
			trees[i] = append(trees[i], t.ID)
		}
	}
	var ind []int64
	for _, t := range c.Independent {
		ind = append(ind, t.ID)
	}
	return trees, ind
}

func TestSessionBatchByTree(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/batch")
	s := runningSession(t, e, p.T3ProjectID, "Planner")
	a := e.agentTask(p.ID, "a", map[string]any{"originThreadId": s, "needsSessionId": s, "startMode": "auto"}, 201)
	b := e.agentTask(p.ID, "b", map[string]any{"originThreadId": s}, 201)
	_ = e.agentTask(p.ID, "c", map[string]any{"originThreadId": s, "needs": []int64{a.ID}}, 201)
	solo := e.agentTask(p.ID, "solo", nil, 201)

	var rows struct {
		Tasks []task `json:"tasks"`
	}
	e.must("GET", "/api/tasks?originThreadId="+s, nil, 200, &rows)
	if len(rows.Tasks) != 3 {
		t.Fatalf("batch filter %+v", rows)
	}
	var td struct {
		Batch *createdTasks `json:"batch"`
	}
	e.must("GET", fmt.Sprintf("/api/tasks/%d", b.ID), nil, 200, &td)
	if td.Batch == nil || len(td.Batch.Trees) != 1 || len(td.Batch.Independent) != 1 {
		t.Fatalf("task batch %+v", td.Batch)
	}
	td.Batch = nil
	e.must("GET", fmt.Sprintf("/api/tasks/%d", solo.ID), nil, 200, &td)
	if td.Batch != nil {
		t.Fatalf("no origin: batch must be null, got %+v", td.Batch)
	}
}
