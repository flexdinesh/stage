package db_test

import (
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
)

func TestPreviewText(t *testing.T) {
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
		if got := db.PreviewText(c.in); got != c.want {
			t.Errorf("%s: PreviewText = %q, want %q", c.name, got, c.want)
		}
	}
	long := db.PreviewText(strings.Repeat("é", 500))
	if n := len([]rune(long)); n != 240 {
		t.Fatalf("preview capped at %d runes, want 240", n)
	}
}

func TestThreadLastWord(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "proj-1")
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := s.UpsertThreads(ctx, t3.Observations([]t3.Thread{{ThreadID: "th-1", ProjectID: "proj-1", Title: "x", Status: t3.StatusCompleted, CreatedAt: now, UpdatedAt: now}})); err != nil {
		t.Fatal(err)
	}
	if at, err := s.LastWordCheckedAt(ctx, "th-1"); err != nil || at != nil {
		t.Fatalf("fresh marker = %v, %v", at, err)
	}
	if lw, err := s.GetThreadLastWord(ctx, "th-1"); err != nil || lw != nil {
		t.Fatalf("fresh last word = %+v, %v", lw, err)
	}
	if _, err := s.LastWordCheckedAt(ctx, "missing"); err != db.ErrNotFound {
		t.Fatalf("missing thread: %v", err)
	}

	replyAt := now.Add(-time.Minute)
	r := t3.Reply{ItemID: "item-1", RunID: "run-2", Text: "## Done\nFixed **it**.", Truncated: true, UpdatedAt: replyAt}
	if err := s.SetThreadLastWord(ctx, "th-1", now, &r); err != nil {
		t.Fatal(err)
	}
	lw, err := s.GetThreadLastWord(ctx, "th-1")
	if err != nil || lw == nil {
		t.Fatalf("last word = %+v, %v", lw, err)
	}
	if lw.Text != r.Text || !lw.Truncated || lw.RunID == nil || *lw.RunID != "run-2" || lw.At == nil || !lw.At.Equal(replyAt) {
		t.Fatalf("last word %+v", lw)
	}
	th, err := s.GetThread(ctx, "th-1")
	if err != nil {
		t.Fatal(err)
	}
	if th.LastWordPreview == nil || *th.LastWordPreview != "Done Fixed it." || th.LastWordAt == nil || th.LastWord != nil {
		t.Fatalf("thread preview %v at %v word %v", th.LastWordPreview, th.LastWordAt, th.LastWord)
	}

	// A stale check (older thread updated_at) never overwrites a newer one.
	old := t3.Reply{ItemID: "item-0", Text: "older"}
	if err := s.SetThreadLastWord(ctx, "th-1", now.Add(-time.Hour), &old); err != nil {
		t.Fatal(err)
	}
	if lw, _ := s.GetThreadLastWord(ctx, "th-1"); lw == nil || lw.Text != r.Text {
		t.Fatalf("stale check overwrote: %+v", lw)
	}

	// No reply found: marker advances, stored reply kept.
	later := now.Add(time.Hour)
	if err := s.SetThreadLastWord(ctx, "th-1", later, nil); err != nil {
		t.Fatal(err)
	}
	if at, err := s.LastWordCheckedAt(ctx, "th-1"); err != nil || at == nil || !at.Equal(later) {
		t.Fatalf("marker = %v, %v", at, err)
	}
	if lw, _ := s.GetThreadLastWord(ctx, "th-1"); lw == nil || lw.Text != r.Text {
		t.Fatalf("nil reply cleared stored: %+v", lw)
	}

	// Tasks carry the preview of their thread.
	task := mustTask(t, s, ctx, p.ID, "t", nil)
	tid := "th-1"
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: task.ID, From: []db.TaskState{db.StateBacklog}, To: db.StateRunning, ThreadID: &tid}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastWordPreview == nil || *got.LastWordPreview != "Done Fixed it." || got.LastWordAt == nil {
		t.Fatalf("task preview %v at %v", got.LastWordPreview, got.LastWordAt)
	}
}
