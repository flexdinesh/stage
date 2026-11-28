package cli_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/t3"
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

// task send --expect-revision is a guarded answer: the stale revision is
// refused with already_answered, the current one goes once; task show
// prints the revision.
func TestTaskSendExpectRevision(t *testing.T) {
	h := newHarness(t)
	p := h.project("/w/cli-expect")
	c := client.New(h.url)
	a, err := c.CreateTask(t.Context(), client.CreateTask{ProjectID: p.ID, ConnectionID: dbtest.T3Connection, WorkspaceID: h.workspaceID(p), Title: "asks", Prompt: "x", Start: true})
	if err != nil || a.ExternalSessionID == nil {
		t.Fatalf("task %+v %v", a, err)
	}
	if _, err := h.store.TransitionTask(t.Context(), db.Transition{TaskID: a.ID, To: db.StateNeedsInput}); err != nil {
		t.Fatal(err)
	}
	got, err := h.store.GetTask(t.Context(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	id, rev := fmt.Sprint(a.ID), got.StateRevision
	if code, stdout, stderr := h.run("", "task", "show", id); code != 0 || !strings.Contains(stdout, fmt.Sprintf("state revision %d", rev)) {
		t.Fatalf("show: %d %s %s", code, stdout, stderr)
	}
	if code, _, stderr := h.run("", "task", "send", id, "--text", "go", "--expect-revision", fmt.Sprint(rev-1)); code != 1 || !strings.Contains(stderr, "Already answered") {
		t.Fatalf("stale revision: %d %s", code, stderr)
	}
	if len(h.fake.Sent) != 0 {
		t.Fatalf("refused message sent: %+v", h.fake.Sent)
	}
	if err := h.fake.SetStatus(*a.ExternalSessionID, t3.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	if code, stdout, stderr := h.run("", "task", "send", id, "--text", "go", "--expect-revision", fmt.Sprint(rev)); code != 0 {
		t.Fatalf("send: %d %s %s", code, stdout, stderr)
	}
	if code, _, stderr := h.run("", "task", "send", id, "--text", "again", "--expect-revision", fmt.Sprint(rev)); code != 1 || !strings.Contains(stderr, "Already answered") {
		t.Fatalf("answered twice: %d %s", code, stderr)
	}
	if len(h.fake.Sent) != 1 {
		t.Fatalf("sent: %+v", h.fake.Sent)
	}
}

func TestTaskSessionSurfaceCommands(t *testing.T) {
	h := newHarness(t)
	p := h.project("/w/cli-delivery")
	c := client.New(h.url)
	a, err := c.CreateTask(t.Context(), client.CreateTask{ProjectID: p.ID, ConnectionID: dbtest.T3Connection, WorkspaceID: h.workspaceID(p), Title: "Delivery", Prompt: "x", Start: true})
	if err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprint(a.ID)
	for _, mode := range []struct {
		flag   string
		native t3.SendMode
	}{{"--steer", t3.SendSteer}, {"--queue", t3.SendQueue}} {
		if code, _, stderr := h.run("", "task", "send", id, "--text", "hello", mode.flag); code != 0 {
			t.Fatalf("task send %s: %d %s", mode.flag, code, stderr)
		}
		if n := len(h.fake.Sent); n == 0 || h.fake.Sent[n-1].Mode != mode.native {
			t.Fatalf("task send %s: %+v", mode.flag, h.fake.Sent)
		}
	}
	if code, _, stderr := h.run("", "task", "send", id, "--text", "hello", "--steer", "--queue"); code != 2 || !strings.Contains(stderr, "mutually exclusive") {
		t.Fatalf("conflicting flags: %d %s", code, stderr)
	}
	if len(h.fake.Sent) != 2 {
		t.Fatalf("conflicting delivery flags reached the harness: %+v", h.fake.Sent)
	}
	if a.Session == nil {
		t.Fatal("started task has no session")
	}
	code, stdout, stderr := h.run("", "task", "show", id)
	wantSession := fmt.Sprintf("attempt 1  running  %s (current)", a.Session.ExternalSessionID)
	if code != 0 || !strings.Contains(stdout, wantSession) {
		t.Fatalf("task show missing %q: %d %s %s", wantSession, code, stdout, stderr)
	}

	now := time.Date(2026, 11, 29, 4, 30, 0, 0, time.UTC)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/tasks/1/activity" || r.URL.Query().Get("limit") != "5" || r.URL.Query().Get("attempt") != "0" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		_ = json.NewEncoder(w).Encode(client.SessionActivity{Activity: execution.Activity{
			Items: []execution.ActivityItem{{ID: "one", At: now, Kind: "command", Title: "Run tests", Preview: "go test"}},
			Plan:  []execution.PlanStep{{Text: "Check result", Status: "done"}},
			Files: []execution.FileChange{
				{Path: "work.go", Additions: 2, Deletions: 1, Status: "modified"},
				{Path: "path-only.go", CountsUnknown: true},
			},
		}})
	}))
	defer srv.Close()
	h.url = srv.URL
	code, stdout, stderr = h.run("", "task", "activity", "1", "--attempt", "0", "--limit", "5")
	for _, want := range []string{now.Format(time.RFC3339), "command", "Run tests", "go test", "Check result", "work.go", "+2 -1"} {
		if code != 0 || !strings.Contains(stdout, want) {
			t.Fatalf("activity missing %q: %d %s %s", want, code, stdout, stderr)
		}
	}
	if !strings.Contains(stdout, "  path-only.go\n") {
		t.Fatalf("activity should list the path alone: %s", stdout)
	}
	code, stdout, stderr = h.run("", "task", "activity", "1", "--attempt", "0", "--limit", "5", "--json")
	out := decode[client.SessionActivity](t, stdout)
	if code != 0 || len(out.Activity.Items) != 1 || out.Activity.Items[0].ID != "one" {
		t.Fatalf("activity JSON: %d %s %s", code, stdout, stderr)
	}
}
