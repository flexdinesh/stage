package claudecode_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/claudecode"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest"
	"github.com/flexdinesh/stage/server/internal/claudecode/claudetest/fakestate"
	"github.com/flexdinesh/stage/server/internal/execution"
)

const activitySessionID = "00000000-0000-4000-8000-000000000537"

func activityFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "transcripts", name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeActivityTranscript(t *testing.T, f *claudetest.Fake, cwd, body string) string {
	t.Helper()
	dir := filepath.Join(f.ProjectsDir(), claudecode.EncodeProject(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, activitySessionID+".jsonl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestActivityTranscript(t *testing.T) {
	f := claudetest.New(t)
	body := activityFixture(t, "activity")
	path := writeActivityTranscript(t, f, f.Workspace, body)
	a := f.Adapter(conn)
	ref := execution.SessionRef{ConnectionID: conn, ExternalSessionID: activitySessionID}
	got, err := a.Activity(t.Context(), ref, execution.ActivityQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	wantItems := []struct{ kind, title, text, preview, status string }{
		{"message_user", "", "Update the examples.", "", ""},
		{"reasoning", "Thinking", "", "", ""},
		{"message_agent", "", "I will update the two examples.", "", ""},
		{"reasoning", "Thinking", "", "", ""},
		{"command", "Bash", "", "printf ok", "done"},
		{"command", "Bash", "", "false", "failed"},
		{"file_change", "Edit", "", "/example/a.txt", "done"},
		{"file_change", "Write", "", "/example/b.txt", "done"},
		{"file_change", "Write", "", "/example/a.txt", "done"},
		{"file_change", "MultiEdit", "", "/example/a.txt", "failed"},
		{"file_change", "NotebookEdit", "", "/example/demo.ipynb", "done"},
		{"plan", "TodoWrite", "", "", "done"},
		{"plan", "TodoWrite", "", "", "done"},
		{"plan", "TodoWrite", "", "", "failed"},
		{"tool", "Read", "", `{"file_path":"/example/a.txt"}`, "done"},
		{"message_agent", "", "The examples are updated.", "", ""},
		{"command", "Bash", "", "wc -l /example/a.txt", "running"},
	}
	if len(got.Items) != len(wantItems) || got.Truncated || got.At.IsZero() {
		t.Fatalf("activity has %d items, truncated=%v, at=%s", len(got.Items), got.Truncated, got.At)
	}
	ids := map[string]bool{}
	for i, item := range got.Items {
		want := wantItems[i]
		if item.Kind != want.kind || item.Title != want.title || item.Text != want.text ||
			item.Preview != want.preview || item.Status != want.status {
			t.Errorf("item %d = %+v, want %+v", i, item, want)
		}
		if item.ID == "" || ids[item.ID] || item.At.IsZero() || (i > 0 && item.At.Before(got.Items[i-1].At)) {
			t.Errorf("item %d has invalid id or ordering: %+v", i, item)
		}
		ids[item.ID] = true
	}
	wantPlan := []execution.PlanStep{
		{Text: "Update the examples", Status: "done"},
		{Text: "Check the notebook", Status: "in_progress"},
		{Text: "Summarize", Status: "pending"},
	}
	wantFiles := []execution.FileChange{
		{Path: "/example/a.txt", Additions: 2, Deletions: 1, Status: "modified"},
		{Path: "/example/b.txt", Additions: 3, Status: "added"},
	}
	if !slices.Equal(got.Plan, wantPlan) || !slices.Equal(got.Files, wantFiles) || got.Model != "claude-opus-4-8" {
		t.Fatalf("plan=%+v files=%+v model=%q", got.Plan, got.Files, got.Model)
	}
	checkActivityDetails(t, got.Details, map[string]string{
		"permissionMode": "plan",
		"effort":         "max",
		"worktree":       "/home/user/src/project/.claude/worktrees/sample",
	})

	tail, err := a.Activity(t.Context(), ref, execution.ActivityQuery{Limit: 3})
	if err != nil || !tail.Truncated || !slices.Equal(tail.Items, got.Items[len(got.Items)-3:]) ||
		!slices.Equal(tail.Plan, wantPlan) || !slices.Equal(tail.Files, wantFiles) {
		t.Fatalf("tail=%+v err=%v", tail, err)
	}

	// A subsequent result updates the existing row ID even at limit 1.
	updated := body + `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_bash_pending","is_error":false}]},"toolUseResult":{"interrupted":false}}
{"type":"worktree-state","worktreeSession":null}
`
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	last, err := a.Activity(t.Context(), ref, execution.ActivityQuery{Limit: 1})
	if err != nil || len(last.Items) != 1 || last.Items[0].ID != got.Items[len(got.Items)-1].ID || last.Items[0].Status != "done" {
		t.Fatalf("updated tail=%+v err=%v", last, err)
	}
	checkActivityDetails(t, last.Details, map[string]string{"permissionMode": "plan", "effort": "max"})
}

func checkActivityDetails(t *testing.T, details json.RawMessage, want map[string]string) {
	t.Helper()
	var got map[string]string
	if err := json.Unmarshal(details, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("details=%v want=%v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("details[%q]=%q want=%q", k, got[k], v)
		}
	}
}

func TestActivityPrefersRosterDirectory(t *testing.T) {
	f := claudetest.New(t)
	// Two files share the same session ID. The roster's directory wins.
	writeActivityTranscript(t, f, "/000", `{"type":"user","message":{"content":"wrong transcript"}}`+"\n")
	writeActivityTranscript(t, f, f.Workspace, activityFixture(t, "worktree"))
	f.Update(func(s *fakestate.State) {
		s.Sessions = append(s.Sessions, fakestate.Session{Entry: fakestate.Entry{
			SessionID: activitySessionID, Cwd: f.Workspace, Kind: claudecode.KindInteractive,
		}})
	})
	a := f.Adapter(conn)
	ref := execution.SessionRef{ConnectionID: conn, ExternalSessionID: activitySessionID}
	got, err := a.Activity(t.Context(), ref, execution.ActivityQuery{})
	if err != nil {
		t.Fatal(err)
	}
	checkActivityDetails(t, got.Details, map[string]string{
		"permissionMode": "bypassPermissions",
		"effort":         "medium",
		"worktree":       "/home/user/src/spike/repo/.claude/worktrees/spikewt",
	})
	ref.ConnectionID++
	if _, err := a.Activity(t.Context(), ref, execution.ActivityQuery{}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("wrong connection err=%v", err)
	}
	ref.ConnectionID = conn
	ref.ExternalSessionID = "90000000-0000-4000-8000-000000000537"
	if _, err := a.Activity(t.Context(), ref, execution.ActivityQuery{}); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("missing session err=%v", err)
	}
}

func TestActivityBoundsFeed(t *testing.T) {
	f := claudetest.New(t)
	var body strings.Builder
	for i := range 105 {
		fmt.Fprintf(&body, `{"type":"user","uuid":"user-%d","message":{"content":%q}}`+"\n", i, strings.Repeat("a", 2100))
	}
	body.WriteString("partial transcript row\n")
	writeActivityTranscript(t, f, f.Workspace, body.String())
	a := f.Adapter(conn)
	ref := execution.SessionRef{ConnectionID: conn, ExternalSessionID: activitySessionID}
	for _, tt := range []struct {
		limit, want int
		firstID     string
	}{{0, 30, "user-75"}, {1000, 100, "user-5"}} {
		got, err := a.Activity(t.Context(), ref, execution.ActivityQuery{Limit: tt.limit})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Items) != tt.want || !got.Truncated || got.Items[0].ID != tt.firstID ||
			got.Items[len(got.Items)-1].ID != "user-104" || len(got.Items[0].Text) != 2000 || len(got.Details) != 0 {
			t.Fatalf("limit=%d gave %d items, truncated=%v, details=%s", tt.limit, len(got.Items), got.Truncated, got.Details)
		}
	}
}
