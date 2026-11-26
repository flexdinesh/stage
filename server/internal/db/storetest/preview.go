package storetest

import (
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/flexdinesh/stage/server/internal/execution"
)

func init() {
	register("PreviewText", casePreviewText)
	register("TaskReplyPreview", caseTaskReplyPreview)
}

func casePreviewText(t *testing.T, open Factory) {
	cases := []struct{ name, in, want string }{
		{"plain", "Done.", "Done."},
		{"heading and list", "## Summary\n\n- Fixed the race in `PollTask`\n- Added **regression** test\n1. Ran `go test`", "Summary Fixed the race in PollTask Added regression test Ran go test"},
		{"fence and rule", "Changed:\n```go\nx := 1\n```\n---\nok", "Changed: x := 1 ok"},
		{"links and images", "See [PR #12](https://x/pr/12) and ![shot](a.png).", "See PR #12 and shot."},
		{"quote and checkbox", "> note\n- [x] done item", "note done item"},
		{"table", "| a | b |\n|---|---|\n| 1 | 2 |", "a b 1 2"},
		{"identifiers kept", "set snake_case and *ptr", "set snake_case and *ptr"},
		{"whitespace", "  a\t\tb  \n\n\n c ", "a b c"},
		{"empty", "```\n```", ""},
	}
	for _, c := range cases {
		if got := store.PreviewText(c.in); got != c.want {
			t.Errorf("%s: PreviewText = %q, want %q", c.name, got, c.want)
		}
	}
	long := store.PreviewText(strings.Repeat("é", 500))
	if n := len([]rune(long)); n != 240 {
		t.Fatalf("preview capped at %d runes, want 240", n)
	}
}

// A task carries the plain preview of its session's observed reply, for
// any harness.
func caseTaskReplyPreview(t *testing.T, open Factory) {
	s, ctx := setup(t, open)
	p := mustProject(t, s, ctx, "proj-1")
	task := mustTask(t, s, ctx, p.ID, "t", nil)
	ref := session("th-1")
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, To: db.StateRunning, Session: &ref}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := s.PutObservation(ctx, execution.Observation{Session: ref, Project: "proj-1", Title: "t", State: "done", CreatedAt: now, UpdatedAt: now,
		Reply: &execution.Reply{Text: "## Done\nFixed **it**.", UpdatedAt: now}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastWordPreview == nil || *got.LastWordPreview != "Done Fixed it." || got.LastWordAt == nil || !got.LastWordAt.Equal(now) {
		t.Fatalf("task preview %v at %v", got.LastWordPreview, got.LastWordAt)
	}
}
