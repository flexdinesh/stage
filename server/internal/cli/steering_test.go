package cli_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/client"
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
	code, stdout, stderr = h.run("watch the tree", "tree", "steer", id, "--prompt", "-", "--connection", "1", "--workspace", workspace)
	if code != 0 || !strings.Contains(stdout, fmt.Sprintf("Steering tree %d with #", tree.ID)) || !strings.Contains(stdout, "Steer: Steered") {
		t.Fatalf("steer: %d %s %s", code, stdout, stderr)
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
	code, stdout, stderr = h.run("", "tree", "unsteer", id)
	if code != 0 || !strings.Contains(stdout, fmt.Sprintf("Stopped steering tree %d", tree.ID)) {
		t.Fatalf("unsteer: %d %s %s", code, stdout, stderr)
	}
	if code, _, stderr := h.run("", "tree", "unsteer", id); code != 1 || !strings.Contains(stderr, "no active steerer") {
		t.Fatalf("unsteer twice: %d %s", code, stderr)
	}
}
