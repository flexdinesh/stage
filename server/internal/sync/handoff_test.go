package sync_test

import (
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
)

func TestComposeAcceptedHandoff(t *testing.T) {
	reply := "Accepted answer"
	run := "r1"
	thread := "thread-1"
	at := time.Date(2026, 11, 14, 10, 2, 0, 0, time.UTC)
	a := db.Acceptance{ID: 7, TaskID: 12, AcceptedAt: at, CapturedAt: at, Provenance: "user", Note: "Reviewed", Evidence: db.AcceptedEvidence{TaskID: 12, Title: "Build API", ThreadID: &thread, RunID: &run, ReplyRunID: &run, Reply: &reply, JournalThroughID: 99, Artifacts: []t3.PullRequest{{URL: "https://x/41"}}}}
	got := t3sync.ComposeAcceptedHandoff(a, 4000)
	for _, want := range []string{"task #12", "Accepted result #7", "entry #99", "Accepted answer", "User acceptance note:\nReviewed", "https://x/41"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q: %s", want, got)
		}
	}
	a.Evidence.Reply = nil
	if got = t3sync.ComposeAcceptedHandoff(a, 4000); !strings.Contains(got, "Final reply: missing at acceptance.") {
		t.Fatal(got)
	}
}

func TestReplyCap(t *testing.T) {
	for n, want := range map[int]int{0: 4000, 1: 4000, 2: 4000, 3: 2666, 4: 2000, 5: 1600, 8: 1000, 20: 1000} {
		if got := t3sync.ReplyCap(n); got != want {
			t.Fatalf("replyCap(%d) = %d, want %d", n, got, want)
		}
	}
}
