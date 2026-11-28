package agenttools_test

import (
	"testing"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
)

// set_tree_steerer launches a steerer whose get_context names the tree it
// steers; get_steering_events follows the feed; end_tree_steering ends it.
func TestSteeringTools(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	p := e.project("/w/steer")
	workspace := e.workspaceID(p.Bindings[0])
	member, err := e.client.CreateTask(ctx, client.CreateTask{ProjectID: p.ID, ConnectionID: dbtest.T3Connection, WorkspaceID: workspace, Title: "member", Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	tree, err := e.client.CreateTree(ctx, client.NewTree{Title: "Steered", Tasks: []db.TreeMember{{TaskID: member.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	cs := connect(t, &agenttools.Ops{Client: e.client.AsToolTransport()})
	var set client.SteererResult
	mustCall(t, cs, "set_tree_steerer", map[string]any{
		"treeId": tree.ID, "prompt": "steer it", "connectionId": dbtest.T3Connection, "workspaceId": workspace,
	}, &set)
	if set.Task == nil || set.Task.ExternalSessionID == nil || set.Tree.Steerer == nil || set.Tree.Steerer.TaskID != set.Task.ID || set.Task.Title != "Steer: Steered" {
		t.Fatalf("set_tree_steerer %+v", set)
	}
	if msg := call(t, cs, "set_tree_steerer", map[string]any{
		"treeId": tree.ID, "prompt": "again", "connectionId": dbtest.T3Connection, "workspaceId": workspace,
	}, nil); msg == "" {
		t.Fatal("second steerer accepted")
	}

	self := e.t3(*set.Task.ExternalSessionID)
	steerer := connect(t, &agenttools.Ops{Client: e.client.AsToolTransport(), Caller: agentctx.Caller{Session: self}})
	var contextOut agenttools.ContextOut
	mustCall(t, steerer, "get_context", nil, &contextOut)
	if contextOut.SteeringTreeID == nil || *contextOut.SteeringTreeID != tree.ID || contextOut.TreeID != nil {
		t.Fatalf("steerer context %+v", contextOut)
	}
	mustCall(t, steerer, "append_note", map[string]any{"taskId": member.ID, "category": "decision", "text": "ship a", "requestId": "d1"}, nil)
	var page client.SteeringEvents
	mustCall(t, steerer, "get_steering_events", map[string]any{"treeId": tree.ID}, &page)
	if len(page.Events) != 2 || page.Events[0].Kind != db.SteerStarted || page.Events[1].Kind != db.SteerNote || !page.Events[1].Self {
		t.Fatalf("feed %+v", page)
	}
	mustCall(t, steerer, "get_steering_events", map[string]any{"treeId": tree.ID, "after": page.Events[0].ID, "limit": 1}, &page)
	if len(page.Events) != 1 || page.HasMore {
		t.Fatalf("paged feed %+v", page)
	}

	// update_tree steeringHoldSeconds; escalate_to_user only from the steerer.
	var updated client.TreeDetail
	mustCall(t, cs, "update_tree", map[string]any{"id": tree.ID, "steeringHoldSeconds": 60}, &updated)
	if updated.SteeringHoldSeconds != 60 {
		t.Fatalf("update_tree hold %d", updated.SteeringHoldSeconds)
	}
	memberRef := db.SessionRef{ConnectionID: dbtest.T3Connection, ExternalSessionID: "member-esc"}
	if _, err := e.store.TransitionTask(ctx, db.Transition{TaskID: member.ID, To: db.StateNeedsInput, Session: &memberRef}); err != nil {
		t.Fatal(err)
	}
	if msg := call(t, cs, "escalate_to_user", map[string]any{"taskId": member.ID, "reason": "why"}, nil); msg == "" {
		t.Fatal("escalate_to_user from a non-steerer accepted")
	}
	var escalated client.Task
	mustCall(t, steerer, "escalate_to_user", map[string]any{"taskId": member.ID, "reason": "needs a product call"}, &escalated)
	if escalated.ID != member.ID || escalated.Hold != nil {
		t.Fatalf("escalate_to_user %+v", escalated)
	}
	ns, err := e.store.ListNotifications(ctx, 10, nil)
	if err != nil || len(ns) != 1 || ns[0].Note == nil || *ns[0].Note != "Steerer: needs a product call" {
		t.Fatalf("notifications %+v %v", ns, err)
	}

	var ended client.SteererResult
	mustCall(t, cs, "end_tree_steering", map[string]any{"treeId": tree.ID}, &ended)
	if ended.Tree.Steerer != nil {
		t.Fatalf("end_tree_steering %+v", ended.Tree.Steerer)
	}
	mustCall(t, steerer, "get_context", nil, &contextOut)
	if contextOut.SteeringTreeID != nil {
		t.Fatalf("context after end %+v", contextOut)
	}
	if msg := call(t, cs, "end_tree_steering", map[string]any{"treeId": tree.ID}, nil); msg == "" {
		t.Fatal("end again accepted")
	}
}
