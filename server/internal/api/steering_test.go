package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/buildinfo"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/t3"
)

type steeringTask struct {
	task
	Steering *struct {
		TreeID    int64   `json:"treeId"`
		Active    bool    `json:"active"`
		EndReason *string `json:"endReason"`
	} `json:"steering"`
}

type steeringTree struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Steerer *struct {
		TaskID           int64  `json:"taskId"`
		Title            string `json:"title"`
		State            string `json:"state"`
		Since            string `json:"since"`
		DeliveredThrough int64  `json:"deliveredThrough"`
		Pending          int64  `json:"pending"`
	} `json:"steerer"`
}

type steererResponse struct {
	Tree     steeringTree  `json:"tree"`
	Task     *steeringTask `json:"task"`
	Warnings []string      `json:"warnings"`
}

type steeringPage struct {
	Events []struct {
		ID       int64          `json:"id"`
		Kind     string         `json:"kind"`
		TaskID   *int64         `json:"taskId"`
		Self     bool           `json:"self"`
		Payload  map[string]any `json:"payload"`
		CausedBy *struct {
			TaskID *int64 `json:"taskId"`
		} `json:"causedBy"`
	} `json:"events"`
	HasMore bool `json:"hasMore"`
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// steerBody is a set-steerer request on project p's workspace.
func (e *env) steerBody(projectID int64, requestID string, extra map[string]any) map[string]any {
	e.t.Helper()
	b := e.taskBody(projectID, "", nil)
	body := map[string]any{"prompt": "steer the tree", "connectionId": b["connectionId"], "workspaceId": b["workspaceId"], "requestId": requestID}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

func (e *env) steeringTree(projectID int64, title string) (steeringTree, task) {
	e.t.Helper()
	a := e.task(projectID, "member", nil)
	var tr steeringTree
	e.must("POST", "/api/trees", map[string]any{"title": title, "tasks": []any{map[string]any{"taskId": a.ID}}}, 201, &tr)
	return tr, a
}

// Set, replay, guard, feed, replace and stop a tree's steerer over HTTP.
func TestTreeSteering(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/steer")
	tr, a := e.steeringTree(p.ID, "Ship it")
	steerer := fmt.Sprintf("/api/trees/%d/steerer", tr.ID)
	feed := fmt.Sprintf("/api/trees/%d/steering/events", tr.ID)

	if code, b := e.raw("POST", steerer, `{"prompt":"x"}`); code != 400 {
		t.Fatalf("no requestId: %d %v", code, b)
	}
	var first steererResponse
	e.must("POST", steerer, e.steerBody(p.ID, "s1", nil), 201, &first)
	st := first.Task
	if st == nil || st.Title != "Steer: Ship it" || st.TreeID != nil || st.Steering == nil || !st.Steering.Active || st.Steering.TreeID != tr.ID ||
		st.LaunchAttempt != 1 || len(first.Warnings) != 0 {
		t.Fatalf("steerer %+v warnings %v", st, first.Warnings)
	}
	if s := first.Tree.Steerer; s == nil || s.TaskID != st.ID || s.Title != st.Title || s.Since == "" || s.DeliveredThrough != 0 {
		t.Fatalf("tree steerer %+v", first.Tree.Steerer)
	}
	var replay steererResponse
	e.must("POST", steerer, e.steerBody(p.ID, "s1", nil), 200, &replay)
	if replay.Task == nil || replay.Task.ID != st.ID {
		t.Fatalf("replay %+v", replay.Task)
	}
	if code, b := e.raw("POST", steerer, mustJSON(t, e.steerBody(p.ID, "s1", map[string]any{"prompt": "other"}))); code != 409 {
		t.Fatalf("replay with other content: %d %v", code, b)
	}
	if code, b := e.raw("POST", steerer, mustJSON(t, e.steerBody(p.ID, "s2", nil))); code != 409 || b["code"] != "steerer_exists" {
		t.Fatalf("second steerer: %d %v", code, b)
	}
	if code, b := e.raw("POST", fmt.Sprintf("/api/trees/%d/tasks", tr.ID), fmt.Sprintf(`{"taskId":%d}`, st.ID)); code != 400 || b["code"] != "steerer_not_member" {
		t.Fatalf("steerer as member: %d %v", code, b)
	}

	// The feed: the steerer's own note is self; another caller's is not.
	steerRef := sessionOf(e, st.ID)
	asSteerer := map[string]string{buildinfo.APIVersionHeader: apiVersion, agentctx.CallerHeader: fmt.Sprintf("%d:%s", steerRef.ConnectionID, steerRef.ExternalSessionID)}
	journal := fmt.Sprintf("/api/tasks/%d/journal", a.ID)
	if rec := raw(t, e.h, "POST", journal, asSteerer, map[string]any{"text": "merge order: a first", "category": "decision", "requestId": "n1"}, nil); rec.Code != 201 {
		t.Fatalf("steerer note: %d %s", rec.Code, rec.Body.String())
	}
	e.must("POST", journal, map[string]any{"text": "blocked on review", "category": "blocker", "requestId": "n2"}, 201, nil)
	var page steeringPage
	e.must("GET", feed, nil, 200, &page)
	if len(page.Events) != 3 || page.HasMore || page.Events[0].Kind != "steering_started" || page.Events[1].Kind != "note" || page.Events[2].Kind != "note" {
		t.Fatalf("feed %+v", page)
	}
	if n := page.Events[1]; !n.Self || n.CausedBy == nil || n.CausedBy.TaskID == nil || *n.CausedBy.TaskID != st.ID || n.Payload["category"] != "decision" {
		t.Fatalf("steerer note event %+v", n)
	}
	if page.Events[2].Self || page.Events[2].TaskID == nil || *page.Events[2].TaskID != a.ID {
		t.Fatalf("user note event %+v", page.Events[2])
	}
	var tree steeringTree
	e.must("GET", fmt.Sprintf("/api/trees/%d", tr.ID), nil, 200, &tree)
	if tree.Steerer == nil || tree.Steerer.Pending != 2 {
		t.Fatalf("pending %+v", tree.Steerer)
	}
	var paged steeringPage
	e.must("GET", fmt.Sprintf("%s?after=%d&limit=1", feed, page.Events[0].ID), nil, 200, &paged)
	if len(paged.Events) != 1 || !paged.HasMore || paged.Events[0].ID != page.Events[1].ID {
		t.Fatalf("page %+v", paged)
	}
	for _, q := range []string{"?limit=0", "?limit=201", "?after=-1", "?after=x"} {
		if code, b := e.raw("GET", feed+q, ""); code != 400 {
			t.Fatalf("%s: %d %v", q, code, b)
		}
	}

	// Replace: the old binding ends and its session stops.
	var second steererResponse
	e.must("POST", steerer, e.steerBody(p.ID, "s3", map[string]any{"replace": true, "title": "Night shift"}), 201, &second)
	if second.Task == nil || second.Task.ID == st.ID || second.Task.Title != "Night shift" || second.Tree.Steerer == nil || second.Tree.Steerer.TaskID != second.Task.ID || len(second.Warnings) != 0 {
		t.Fatalf("replace %+v", second)
	}
	if !slices.Contains(e.fake.Interrupted, steerRef.ExternalSessionID) {
		t.Fatalf("old steerer session not stopped: %v", e.fake.Interrupted)
	}
	var old steeringTask
	e.must("GET", fmt.Sprintf("/api/tasks/%d", st.ID), nil, 200, &old)
	if old.Steering == nil || old.Steering.Active || old.Steering.EndReason == nil || *old.Steering.EndReason != "replaced" {
		t.Fatalf("replaced steering %+v", old.Steering)
	}
	// Stop steering.
	var stopped steererResponse
	e.must("DELETE", steerer, nil, 200, &stopped)
	if stopped.Tree.Steerer != nil || len(stopped.Warnings) != 0 {
		t.Fatalf("stopped %+v", stopped)
	}
	if code, b := e.raw("DELETE", steerer, ""); code != 409 || b["code"] != "no_steerer" {
		t.Fatalf("stop again: %d %v", code, b)
	}
	e.must("GET", feed, nil, 200, &page)
	var kinds []string
	for _, ev := range page.Events {
		kinds = append(kinds, ev.Kind)
	}
	want := []string{"steering_started", "note", "note", "steering_ended", "steering_started", "steering_ended"}
	if !slices.Equal(kinds, want) {
		t.Fatalf("feed kinds %v, want %v", kinds, want)
	}

	// A closed tree takes no steerer; a missing one is 404.
	e.must("PATCH", fmt.Sprintf("/api/trees/%d", tr.ID), map[string]any{"status": "done"}, 200, nil)
	if code, b := e.raw("POST", steerer, mustJSON(t, e.steerBody(p.ID, "s4", nil))); code != 409 {
		t.Fatalf("closed tree: %d %v", code, b)
	}
	if code, _ := e.raw("POST", "/api/trees/999999/steerer", mustJSON(t, e.steerBody(p.ID, "s5", nil))); code != 404 {
		t.Fatalf("missing tree: %d", code)
	}
}

// noSend is T3 declaring send unavailable.
type noSend struct{ *t3.Adapter }

func (a noSend) Features(ctx context.Context) (execution.Capabilities, error) {
	c, err := a.Adapter.Features(ctx)
	if err == nil {
		c.Operations[execution.OpSend] = execution.Availability{Reason: "No follow-ups here."}
	}
	return c, err
}

// A connection without send cannot host a steerer (ADR 0013 P2).
func TestTreeSteeringNeedsSend(t *testing.T) {
	e := newEnvWrapped(t, func(a *t3.Adapter) execution.Adapter { return noSend{a} })
	p := e.project("/w/nosend")
	tr, _ := e.steeringTree(p.ID, "Quiet")
	code, b := e.raw("POST", fmt.Sprintf("/api/trees/%d/steerer", tr.ID), mustJSON(t, e.steerBody(p.ID, "s1", nil)))
	if code != 409 || b["code"] != "operation_unavailable" || b["error"] != "Steering needs operation send: No follow-ups here." {
		t.Fatalf("no send: %d %v", code, b)
	}
	if tasks, err := e.store.ListTasks(context.Background(), db.TaskFilter{}); err != nil || len(tasks) != 1 {
		t.Fatalf("tasks after refusal: %d %v", len(tasks), err)
	}
}

// failStop is T3 whose stop fails.
type failStop struct{ *t3.Adapter }

func (failStop) Stop(context.Context, execution.SessionRef) error {
	return errors.New("thread is locked")
}

// A failed stop still ends the binding and comes back as a warning.
func TestTreeSteeringStopFailureWarns(t *testing.T) {
	e := newEnvWrapped(t, func(a *t3.Adapter) execution.Adapter { return failStop{a} })
	p := e.project("/w/failstop")
	tr, _ := e.steeringTree(p.ID, "Sticky")
	steerer := fmt.Sprintf("/api/trees/%d/steerer", tr.ID)
	var first, second, stopped steererResponse
	e.must("POST", steerer, e.steerBody(p.ID, "s1", nil), 201, &first)
	e.must("POST", steerer, e.steerBody(p.ID, "s2", map[string]any{"replace": true}), 201, &second)
	if len(second.Warnings) != 1 || second.Tree.Steerer == nil || second.Tree.Steerer.TaskID != second.Task.ID {
		t.Fatalf("replace with failed stop %+v", second)
	}
	e.must("DELETE", steerer, nil, 200, &stopped)
	if len(stopped.Warnings) != 1 || stopped.Tree.Steerer != nil {
		t.Fatalf("stop with failed stop %+v", stopped)
	}
}

type steeringBriefTask struct {
	task
	CurrentSteering *struct {
		Attempt int    `json:"attempt"`
		Brief   string `json:"brief"`
	} `json:"currentSteering"`
}

func (e *env) steeringBriefTask(id int64) steeringBriefTask {
	e.t.Helper()
	var out steeringBriefTask
	e.must("GET", fmt.Sprintf("/api/tasks/%d", id), nil, 200, &out)
	return out
}

// An active steerer's launch appends the steering brief between the prompt
// and the journal block, frozen per attempt and shown as currentSteering;
// a relaunch recomposes it; other tasks carry none.
func TestSteeringBriefAtLaunch(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/brief")
	tr, a := e.steeringTree(p.ID, "Ship it")
	if got := e.steeringBriefTask(a.ID); got.CurrentSteering != nil {
		t.Fatalf("member before launch: %+v", got.CurrentSteering)
	}
	e.must("POST", fmt.Sprintf("/api/trees/%d/journal", tr.ID), map[string]any{"text": "merge order: member first", "category": "decision", "requestId": "d1"}, 201, nil)
	var res steererResponse
	e.must("POST", fmt.Sprintf("/api/trees/%d/steerer", tr.ID), e.steerBody(p.ID, "s1", nil), 201, &res)
	st := res.Task.ID

	msg := e.lastMessage()
	brief, journal := strings.Index(msg, "<stage-steering-brief tree="), strings.Index(msg, "<stage-journal>")
	if !strings.HasPrefix(msg, "steer the tree\n\n") || brief < 0 || journal < brief {
		t.Fatalf("launch prompt order:\n%s", msg)
	}
	got := e.steeringBriefTask(st)
	if got.CurrentSteering == nil || got.CurrentSteering.Attempt != 1 || !strings.Contains(msg, got.CurrentSteering.Brief) {
		t.Fatalf("currentSteering %+v", got.CurrentSteering)
	}
	for _, want := range []string{
		fmt.Sprintf("You steer Stage tree #%d \"Ship it\"", tr.ID),
		fmt.Sprintf("as task #%d.", st),
		fmt.Sprintf("- #%d \"member\": backlog", a.ID),
		"merge order: member first",
		"## Reviews",
	} {
		if !strings.Contains(got.CurrentSteering.Brief, want) {
			t.Fatalf("brief misses %q:\n%s", want, got.CurrentSteering.Brief)
		}
	}
	var snippets struct {
		Snippets []db.Snippet `json:"snippets"`
	}
	e.must("GET", "/api/snippets", nil, 200, &snippets)
	if !slices.ContainsFunc(snippets.Snippets, func(s db.Snippet) bool { return s.Name == "steering-playbook" && s.ProjectID == nil }) {
		t.Fatalf("playbook snippet not created: %+v", snippets)
	}
	if got := e.steeringBriefTask(a.ID); got.CurrentSteering != nil {
		t.Fatalf("member carries a brief: %+v", got.CurrentSteering)
	}

	// A new attempt recomposes from the tree as it is then.
	e.must("POST", fmt.Sprintf("/api/trees/%d/journal", tr.ID), map[string]any{"text": "second thoughts", "requestId": "d2"}, 201, nil)
	e.must("POST", fmt.Sprintf("/api/tasks/%d/stop", st), nil, 200, nil)
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", st), nil, 200, nil)
	again := e.steeringBriefTask(st)
	if again.CurrentSteering == nil || again.CurrentSteering.Attempt != 2 || !strings.Contains(again.CurrentSteering.Brief, "second thoughts") || !strings.Contains(e.lastMessage(), again.CurrentSteering.Brief) {
		t.Fatalf("relaunch brief %+v", again.CurrentSteering)
	}

	// Once steering stops, a relaunch carries no brief.
	e.must("DELETE", fmt.Sprintf("/api/trees/%d/steerer", tr.ID), nil, 200, nil)
	e.poll()
	if code, b := e.raw("POST", fmt.Sprintf("/api/tasks/%d/start", st), ""); code != 200 {
		t.Fatalf("relaunch ended steerer: %d %v (state %s)", code, b, e.getTask(st).State)
	}
	if got := e.steeringBriefTask(st); got.CurrentSteering != nil || strings.Contains(e.lastMessage(), "<stage-steering-brief") {
		t.Fatalf("ended steerer relaunch: %+v", got.CurrentSteering)
	}
}
