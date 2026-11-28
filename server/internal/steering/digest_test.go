package steering

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
)

func event(id int64, kind string, task int64, payload map[string]any) db.SteeringEvent {
	raw, _ := json.Marshal(payload)
	e := db.SteeringEvent{ID: id, Kind: kind, Payload: raw}
	if task != 0 {
		e.TaskID = &task
	}
	return e
}

// Lines name the event, task and kind, with the tools that act on it.
func TestEventText(t *testing.T) {
	for _, tc := range []struct {
		e       db.SteeringEvent
		summary string
		want    []string
	}{
		{event(5, db.SteerMemberNeedsInput, 12, map[string]any{"title": "Fix\nbug", "stateRevision": 7}), noRequests,
			[]string{`#5 task #12 "Fix bug" member_needs_input (stateRevision 7): ` + noRequests, "respond_request {taskId: 12, requestId, expectedStateRevision: 7}", "send_message {taskId: 12, text, expectedStateRevision: 7}", "escalate_to_user {taskId: 12, reason}"}},
		{event(6, db.SteerMemberDone, 12, map[string]any{"title": "Fix", "reply": "shipped\nit", "replyTruncated": true}), "",
			[]string{`member_done: reply "shipped it" (truncated)`, "get_completion_preview {taskId: 12}, then complete_task"}},
		{event(7, db.SteerMemberFailed, 12, map[string]any{"error": "exit 1"}), "", []string{"member_failed: exit 1", "start_task {taskId: 12}"}},
		{event(8, db.SteerMemberReady, 13, map[string]any{"title": "Next"}), "", []string{"start_task {taskId: 13}"}},
		{event(9, db.SteerNote, 12, map[string]any{"noteId": 40, "category": "blocker", "text": "stuck", "truncated": true}), "", []string{`blocker note #40 "stuck"`, "get_journal_entry {id: 40}"}},
		{event(10, db.SteerTreeStatus, 0, map[string]any{"from": "open", "to": "done"}), "", []string{"#10 tree_status: open -> done"}},
		{event(11, db.SteerTreeFinished, 0, map[string]any{"reason": "members_completed"}), "", []string{"#11 tree_finished (members_completed)"}},
		{event(12, "hold_escalated", 12, map[string]any{"reason": "unsure"}), "", []string{`hold_escalated: {"reason":"unsure"}`}},
	} {
		got := eventText(tc.e, tc.summary)
		if strings.Contains(got, "\n") {
			t.Fatalf("multi-line %q", got)
		}
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Fatalf("%s line %q lacks %q", tc.e.Kind, got, w)
			}
		}
	}
}

// A digest keeps oldest lines within the cap and points at the feed for
// the rest; the final instruction and the reminder always fit.
func TestDigestCapAndOverflow(t *testing.T) {
	d := digest{Tree: 3, Steerer: 9, Final: true, Waiting: true}
	for id := int64(10); id < 110; id++ {
		d.Lines = append(d.Lines, eventLine{ID: id, Text: fmt.Sprintf("#%d %s", id, strings.Repeat("x", 600))})
	}
	d.Pending, d.FirstID, d.LastID, d.Through = 100, 10, 109, 112
	out := d.render(MaxDigest)
	if len(out) > MaxDigest {
		t.Fatalf("digest %d bytes > %d", len(out), MaxDigest)
	}
	lines := strings.Split(out, "\n")
	if lines[0] != `<stage-steering tree="3" through="112">` || lines[len(lines)-1] != "</stage-steering>" {
		t.Fatalf("wrapper %q ... %q", lines[0], lines[len(lines)-1])
	}
	shown := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "#") {
			shown++
		}
	}
	if shown == 0 || shown >= 100 || !strings.HasPrefix(lines[1], "#10 ") {
		t.Fatalf("shown %d, first %q", shown, lines[1])
	}
	last := int64(10 + shown - 1)
	want := fmt.Sprintf("%d more events (#%d-#109): get_steering_events {treeId: 3, after: %d}", 100-shown, last+1, last)
	if !strings.Contains(out, want) {
		t.Fatalf("no overflow %q in tail %q", want, lines[len(lines)-4:])
	}
	if !strings.Contains(out, "append_note {taskId: 9") || !strings.Contains(out, "Reminder: your own question to the user is still open (task #9") {
		t.Fatal("final instruction or reminder missing")
	}

	small := digest{Tree: 3, Steerer: 9, Lines: d.Lines[:2], Pending: 2, FirstID: 10, LastID: 11, Through: 11}
	if out := small.render(MaxDigest); strings.Contains(out, "more events") || strings.Contains(out, "Reminder") || strings.Contains(out, "outcome") {
		t.Fatalf("small digest %q", out)
	}
}

func TestBackoff(t *testing.T) {
	for n, want := range map[int]time.Duration{1: 10 * time.Second, 2: 20 * time.Second, 5: 160 * time.Second, 6: 5 * time.Minute, 30: 5 * time.Minute} {
		if got := backoff(n); got != want {
			t.Fatalf("backoff(%d) = %s, want %s", n, got, want)
		}
	}
}
