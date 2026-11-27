package cli_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
)

// task preview, then task complete with its revision and evidence version;
// the caller's own task is refused.
func TestTaskCompleteFromPreview(t *testing.T) {
	h := newHarness(t)
	p := h.project("/w/cli-work")
	workspace := h.workspaceID(p)
	c := client.New(h.url)
	a, err := c.CreateTask(t.Context(), client.CreateTask{ProjectID: p.ID, ConnectionID: dbtest.T3Connection, WorkspaceID: workspace, Title: "a", Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := c.CreateTask(t.Context(), client.CreateTask{ProjectID: p.ID, ConnectionID: dbtest.T3Connection, WorkspaceID: workspace, Title: "worker", Prompt: "y", Start: true})
	if err != nil || worker.ExternalSessionID == nil {
		t.Fatalf("worker %+v %v", worker, err)
	}
	h.caller = agentctx.Caller{Session: &db.SessionRef{ConnectionID: dbtest.T3Connection, ExternalSessionID: *worker.ExternalSessionID}}
	id := fmt.Sprint(a.ID)
	if code, _, stderr := h.run("", "task", "complete", id); code != 2 || !strings.Contains(stderr, "task preview "+id) {
		t.Fatalf("complete without preview: %d %s", code, stderr)
	}
	code, stdout, stderr := h.run("", "task", "preview", id, "--json")
	preview := decode[db.CompletionPreview](t, stdout)
	if code != 0 || preview.EvidenceVersion == "" {
		t.Fatalf("preview: %d %s %s", code, stdout, stderr)
	}
	code, stdout, stderr = h.run("", "task", "complete", id, "--revision", fmt.Sprint(preview.Revision), "--evidence-version", preview.EvidenceVersion, "--json")
	if res := decode[db.AcceptanceResult](t, stdout); code != 0 || res.AcceptanceID == nil {
		t.Fatalf("complete: %d %s %s", code, stdout, stderr)
	}
	code, stdout, _ = h.run("", "task", "preview", fmt.Sprint(worker.ID), "--json")
	own := decode[db.CompletionPreview](t, stdout)
	if code != 0 {
		t.Fatalf("own preview: %d %s", code, stdout)
	}
	if code, _, stderr = h.run("", "task", "complete", fmt.Sprint(worker.ID), "--revision", fmt.Sprint(own.Revision), "--evidence-version", own.EvidenceVersion); code != 1 || !strings.Contains(stderr, "complete its own task") {
		t.Fatalf("complete own task: %d %s", code, stderr)
	}
	if code, _, stderr = h.run("", "task", "send", fmt.Sprint(worker.ID), "--text", "hi"); code != 1 || !strings.Contains(stderr, "message its own session") {
		t.Fatalf("send own session: %d %s", code, stderr)
	}
}
