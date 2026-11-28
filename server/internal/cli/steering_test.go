package cli_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
)

func TestTreeSteerCommands(t *testing.T) {
	h := newHarness(t)
	p := h.project("/w/cli-steer")
	workspace := h.workspaceID(p)
	code, stdout, stderr := h.run("", "task", "add", "--project", "1", "--connection", "1", "--workspace-id", workspace, "--title", "member", "--prompt", "p", "--json")
	if code != 0 {
		t.Fatalf("add: %s", stderr)
	}
	member := decode[client.Task](t, stdout)
	code, stdout, stderr = h.run("", "tree", "new", "--title", "Steered", fmt.Sprint(member.ID), "--json")
	if code != 0 {
		t.Fatalf("tree new: %s", stderr)
	}
	tree := decode[client.TreeDetail](t, stdout)
	id := fmt.Sprint(tree.ID)
	if code, _, stderr := h.run("", "tree", "steer", id, "--connection", "1", "--workspace-id", workspace); code != 2 || !strings.Contains(stderr, "--prompt") {
		t.Fatalf("steer without prompt: %d %s", code, stderr)
	}
	if code, _, stderr := h.run("", "tree", "steer", id, "--prompt", "p", "--connection", "1", "--workspace-id", workspace, "--execution-config", "null"); code != 2 || !strings.Contains(stderr, "--execution-config") {
		t.Fatalf("steer with a null config: %d %s", code, stderr)
	}
	code, stdout, stderr = h.run("watch the tree", "tree", "steer", id, "--prompt", "-", "--connection", "1", "--workspace", workspace,
		"--execution-config", `{"options":[{"id":"worktree","value":false}]}`)
	if code != 0 || !strings.Contains(stdout, fmt.Sprintf("Steering tree %d with #", tree.ID)) || !strings.Contains(stdout, "Steer: Steered") {
		t.Fatalf("steer: %d %s %s", code, stdout, stderr)
	}
	if steer, err := h.store.GetTask(t.Context(), h.steererOf(tree.ID)); err != nil || len(steer.ExecutionConfig.Options) != 1 || steer.ExecutionConfig.Options[0].ID != "worktree" {
		t.Fatalf("steerer config %+v %v", steer.ExecutionConfig, err)
	}
	code, stdout, _ = h.run("", "tree", "show", id)
	if code != 0 || !strings.Contains(stdout, "steered by #") {
		t.Fatalf("show steered tree:\n%s", stdout)
	}
	code, stdout, _ = h.run("", "tree", "steering-events", id, "--json")
	if page := decode[client.SteeringEvents](t, stdout); code != 0 || len(page.Events) != 1 || page.Events[0].Kind != "steering_started" {
		t.Fatalf("steering-events: %s", stdout)
	}
	code, stdout, _ = h.run("", "tree", "steering-events", id)
	if code != 0 || !strings.Contains(stdout, "steering_started") {
		t.Fatalf("steering-events text:\n%s", stdout)
	}
	// tree edit --hold; task escalate only as the steerer.
	for _, bad := range []string{"1.5s", "-1m", "soon"} {
		if code, _, stderr := h.run("", "tree", "edit", id, "--hold", bad); code != 2 || !strings.Contains(stderr, "--hold") {
			t.Fatalf("--hold %s: %d %s", bad, code, stderr)
		}
	}
	code, stdout, stderr = h.run("", "tree", "edit", id, "--hold", "2m", "--json")
	if edited := decode[client.TreeDetail](t, stdout); code != 0 || edited.SteeringHoldSeconds != 120 {
		t.Fatalf("--hold 2m: %d %s %s", code, stdout, stderr)
	}
	code, stdout, _ = h.run("", "tree", "edit", id, "--hold", "off", "--json")
	if edited := decode[client.TreeDetail](t, stdout); code != 0 || edited.SteeringHoldSeconds != 0 {
		t.Fatalf("--hold off: %s", stdout)
	}
	memberRef := db.SessionRef{ConnectionID: 1, ExternalSessionID: "member-cli"}
	if _, err := h.store.TransitionTask(t.Context(), db.Transition{TaskID: member.ID, To: db.StateNeedsInput, Session: &memberRef}); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := h.run("", "task", "escalate", fmt.Sprint(member.ID), "--reason", "why"); code != 1 || !strings.Contains(stderr, "steerer") {
		t.Fatalf("escalate as user: %d %s", code, stderr)
	}
	steer, err := h.store.GetTask(t.Context(), h.steererOf(tree.ID))
	if err != nil || steer.Session == nil {
		t.Fatalf("steerer task %+v %v", steer, err)
	}
	h.env = map[string]string{"T3CODE_THREAD_ID": steer.Session.ExternalSessionID}
	code, stdout, stderr = h.run("", "task", "escalate", fmt.Sprint(member.ID), "--reason", "needs a product call", "--json")
	if got := decode[client.Task](t, stdout); code != 0 || got.ID != member.ID || got.Hold != nil {
		t.Fatalf("escalate as steerer: %d %s %s", code, stdout, stderr)
	}
	h.env = nil
	code, stdout, stderr = h.run("", "tree", "unsteer", id)
	if code != 0 || !strings.Contains(stdout, fmt.Sprintf("Stopped steering tree %d", tree.ID)) {
		t.Fatalf("unsteer: %d %s %s", code, stdout, stderr)
	}
	if code, _, stderr := h.run("", "tree", "unsteer", id); code != 1 || !strings.Contains(stderr, "no active steerer") {
		t.Fatalf("unsteer twice: %d %s", code, stderr)
	}
}

// steererOf is tree id's active steerer task.
func (h *harness) steererOf(id int64) int64 {
	h.t.Helper()
	tree, err := h.store.GetTree(h.t.Context(), id)
	if err != nil || tree.Steerer == nil {
		h.t.Fatalf("tree %d steerer: %v", id, err)
	}
	return tree.Steerer.TaskID
}
