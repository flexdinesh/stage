package coordinator_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/coordinator"
	"github.com/flexdinesh/stage/server/internal/db"
)

func TestComposeAcceptedHandoff(t *testing.T) {
	reply := "Accepted answer"
	run := "r1"
	session := db.SessionRef{ConnectionID: 3, ExternalSessionID: "session-1"}
	link := "https://example.test/sessions/1"
	branch := "feature"
	at := time.Date(2026, 11, 14, 10, 2, 0, 0, time.UTC)
	a := db.Acceptance{ID: 7, TaskID: 12, AcceptedAt: at, CapturedAt: at, Provenance: "user", Note: "Reviewed", Evidence: db.AcceptedEvidence{TaskID: 12, Title: "Build API", Session: &session, ExternalSessionID: &session.ExternalSessionID, SessionLink: &link, Branch: &branch, RunID: &run, ReplyRunID: &run, Reply: &reply, JournalThroughID: 99}}
	got := coordinator.ComposeAcceptedHandoff(a, 4000)
	for _, want := range []string{"task #12", "Accepted result #7", "entry #99", "Accepted answer", "User acceptance note:\nReviewed", "Harness session: session-1 " + link + " (connection 3)", "Branch: feature"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q: %s", want, got)
		}
	}
	a.Evidence.Reply = nil
	if got = coordinator.ComposeAcceptedHandoff(a, 4000); !strings.Contains(got, "Final reply: missing at acceptance.") {
		t.Fatal(got)
	}
}

func TestComposeAcceptedHandoffFromLegacyEvidence(t *testing.T) {
	var evidence db.AcceptedEvidence
	raw := []byte(`{"taskId":12,"title":"Build API","threadId":"old-id","threadLink":"https://example.test/old","artifacts":[{"url":"https://example.test/pulls/1"}]}`)
	if err := json.Unmarshal(raw, &evidence); err != nil {
		t.Fatal(err)
	}
	got := coordinator.ComposeAcceptedHandoff(db.Acceptance{Evidence: evidence}, 4000)
	if !strings.Contains(got, "Harness session: old-id https://example.test/old\n") ||
		strings.Contains(got, "Artifact:") || strings.Contains(got, "/pulls/1") {
		t.Fatal(got)
	}
}

func TestReplyCap(t *testing.T) {
	for n, want := range map[int]int{0: 4000, 1: 4000, 2: 4000, 3: 2666, 4: 2000, 5: 1600, 8: 1000, 20: 1000} {
		if got := coordinator.ReplyCap(n); got != want {
			t.Fatalf("replyCap(%d) = %d, want %d", n, got, want)
		}
	}
}
