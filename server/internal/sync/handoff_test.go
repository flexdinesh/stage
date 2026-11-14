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
	got := t3sync.ComposeHandoff(src, d, &t3.Reply{Text: "All done.\n\n", Truncated: true})
	want := `<handoff from="stage">
This task runs after "Build API" (task #12), settled 2026-11-14T10:02:00Z.
- T3 thread: th-1 t3-thread://env/th-1 (read more with t3_thread_read)
- Worktree: /w/wt
- Pull requests:
  - me/app#41 (merged) https://x/41
  - me/lib#7 https://x/7
Final reply:
All done.
[truncated at 4000 chars; full reply via t3_thread_read th-1]
</handoff>`
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}

	// Session source without settle time, link, workspace, PRs or reply.
	got = t3sync.ComposeHandoff(db.HandoffSource{ThreadID: "th-2"}, t3.ThreadDetail{Thread: t3.Thread{Title: "Planner"}}, nil)
	want = `<handoff from="stage">
This task runs after "Planner" (session).
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
