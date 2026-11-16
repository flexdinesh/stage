package sync_test

import (
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
)

func TestComposeHandoff(t *testing.T) {
	id := int64(12)
	settled := time.Date(2026, 11, 14, 10, 2, 0, 0, time.UTC)
	src := db.HandoffSource{TaskID: &id, ThreadID: "th-1", Title: "Build API", SettledAt: &settled}
	d := t3.ThreadDetail{Thread: t3.Thread{Title: "ignored", Link: "t3-thread://env/th-1", PullRequests: []t3.PullRequest{
		{Repository: "me/app", Number: 41, URL: "https://x/41", State: "merged"},
		{Repository: "me/lib", Number: 7, URL: "https://x/7"},
	}}, WorktreePath: "/w/wt"}
	got := t3sync.ComposeHandoff(src, d, &t3.Reply{Text: "All done.\n\n", Truncated: true}, 2666)
	want := `<handoff from="stage">
This task needs "Build API" (task #12), settled 2026-11-14T10:02:00Z.
- T3 thread: th-1 t3-thread://env/th-1 (read more with t3_thread_read)
- Worktree: /w/wt
- Pull requests:
  - me/app#41 (merged) https://x/41
  - me/lib#7 https://x/7
Final reply:
All done.
[truncated at 2666 chars; full reply via t3_thread_read th-1]
</handoff>`
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}

	// A need finished but not settled (started early), without link,
	// workspace, PRs or reply; the title falls back to the thread's.
	seven := int64(7)
	got = t3sync.ComposeHandoff(db.HandoffSource{TaskID: &seven, ThreadID: "th-2"}, t3.ThreadDetail{Thread: t3.Thread{Title: "Planner"}}, nil, 4000)
	want = `<handoff from="stage">
This task needs "Planner" (task #7), finished, not settled yet.
- T3 thread: th-2 (read more with t3_thread_read)
Final reply: none found.
</handoff>`
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(got, "Branch") {
		t.Fatal("empty workspace must be omitted")
	}
}

func TestReplyCap(t *testing.T) {
	for n, want := range map[int]int{0: 4000, 1: 4000, 2: 4000, 3: 2666, 4: 2000, 5: 1600, 8: 1000, 20: 1000} {
		if got := t3sync.ReplyCap(n); got != want {
			t.Fatalf("replyCap(%d) = %d, want %d", n, got, want)
		}
	}
}
