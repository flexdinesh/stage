package coordinator

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/flexdinesh/stage/server/internal/db"
)

func ptr[T any](v T) *T { return &v }

func briefSnapshot() steeringSnapshot {
	ready := db.ReadinessReady
	return steeringSnapshot{
		Tree:      db.Tree{ID: 12, Title: "Ship steering", StartMode: db.StartAuto, Status: db.TreeOpen},
		SteererID: 40,
		Members: []steeringMember{
			{Task: db.Task{ID: 41, Title: "Build API", State: db.StateNeedsInput, StateRevision: 3, LastWordPreview: ptr("Which table name?\nsteering or steer?")}, Branch: "feat/api"},
			{Task: db.Task{ID: 42, Title: "Write docs", State: db.StateBacklog, Readiness: &ready, Needs: []int64{41, 39}}},
		},
		Notes: []db.JournalEntry{
			{ID: 900, Category: "decision", TaskID: ptr(int64(41)), Text: "Use   steering_launch_context."},
			{ID: 899, SourceTaskID: ptr(int64(40)), Text: "Merge #41 first."},
		},
		Playbook: "## Reviews\n- Review first.\n",
	}
}

func TestComposeSteeringBriefSnapshot(t *testing.T) {
	got := composeSteeringBrief(briefSnapshot(), "", "the HTTP API at http://stage.test/api")
	want := `<stage-steering-brief tree=12>
You steer Stage tree #12 "Ship steering" (start mode auto, status open) as task #40. You are not a member of it. Stage sends you the tree's changes and you act on its members in the user's place; the user can answer first and take over at any time.

Members at launch (2):
- #41 "Build API": needs_input, stateRevision 3, branch feat/api
  Last word: Which table name? steering or steer?
- #42 "Write docs": backlog (ready), stateRevision 0, needs #41 #39

Tree notes (newest first, previews; full text with get_journal_entry):
- #900 decision on #41: Use steering_launch_context.
- #899 from #40: Merge #41 first.

Rules:
- Use whichever Stage interface you have: the stage CLI (stage call TOOL JSON runs any tool named here), Stage MCP tools, or the HTTP API at http://stage.test/api; pass your caller session as <stage-journal> says. HTTP equivalents: get_tree is GET /api/trees/{id}; get_steering_events is GET /api/trees/{id}/steering/events?after=N; send_message is POST /api/tasks/{id}/messages; respond_request is POST /api/tasks/{id}/requests/{requestId} (percent-encode requestId: some contain : or %); get_completion_preview is GET /api/tasks/{id}/completion-preview; complete_task is POST /api/tasks/{id}/complete; create_task is POST /api/tasks; escalate_to_user is POST /api/tasks/{id}/escalate.
- Answer a member (send_message, respond_request) with expectedStateRevision set to its stateRevision. 409 already_answered means another answer won: re-read the member and move on.
- Accept a member's result on your own: get_completion_preview, then complete_task with its revision and evidenceVersion.
- Add follow-up work with create_task, treeId 12 and needs.
- Record decisions as tree notes: append_note with treeId 12, sourceTaskId 40, category decision.
- Escalate by ending your turn with a question to the user, or with escalate_to_user {taskId, reason} for a held member.
- Never act on your own task #40; Stage refuses it.
- Updates arrive as <stage-steering tree=12 through=N> messages. Catch up with get_steering_events treeId=12 after=N. Duplicates are harmless: skip what you have seen.
- When told the tree finished, write an outcome note and end your turn.

Playbook (snippet "steering-playbook"; the user may edit it):
## Reviews
- Review first.
</stage-steering-brief>`
	if got != want {
		t.Fatalf("brief:\n%s\n\nwant:\n%s", got, want)
	}
}

func TestComposeSteeringBriefEmptyTreeAndCLIName(t *testing.T) {
	snap := briefSnapshot()
	snap.Members, snap.Notes = nil, nil
	got := composeSteeringBrief(snap, "stage-dev", "the Stage server's HTTP API")
	for _, want := range []string{"Members at launch (0):\n- none yet\n", "(newest first, previews; full text with get_journal_entry):\n- none yet\n", "the stage-dev CLI (stage-dev call TOOL JSON"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
}

func TestSteeringBriefExcerpts(t *testing.T) {
	snap := briefSnapshot()
	long := strings.Repeat("é", 400)
	snap.Members[0].Task.LastWordPreview = &long
	snap.Notes[0].Text = long
	got := composeSteeringBrief(snap, "", "api")
	cut := strings.Repeat("é", steeringExcerptRunes-1) + "…"
	if !strings.Contains(got, "  Last word: "+cut+"\n") || !strings.Contains(got, "on #41: "+cut+"\n") {
		t.Fatalf("excerpts not cut to %d runes:\n%s", steeringExcerptRunes, got)
	}
}

// Over the cap, the oldest notes go first, then the last members, each
// with a pointer; the brief never exceeds the cap.
func TestSteeringBriefTruncation(t *testing.T) {
	snap := briefSnapshot()
	snap.Members, snap.Notes = nil, nil
	text := strings.Repeat("x", 280)
	for i := range 80 {
		snap.Members = append(snap.Members, steeringMember{Task: db.Task{ID: int64(100 + i), Title: fmt.Sprintf("m%d", i), State: db.StateRunning, LastWordPreview: &text}})
	}
	for i := range 20 {
		snap.Notes = append(snap.Notes, db.JournalEntry{ID: int64(500 - i), Text: text})
	}

	// Notes first: members that fit stay whole.
	notesOnly := snap
	notesOnly.Members = snap.Members[:60]
	got := composeSteeringBrief(notesOnly, "", "api")
	if len(got) > steeringBriefCap || !strings.Contains(got, "- #159 \"m59\"") || strings.Contains(got, "members omitted") {
		t.Fatalf("members cut before notes (%d bytes)", len(got))
	}
	if !strings.Contains(got, "- #500: ") || strings.Contains(got, "- #481: ") || !strings.Contains(got, "older notes omitted to fit; read them with read_journal treeId=12.") {
		t.Fatalf("notes not cut oldest first:\n%s", got)
	}

	// Then members, from the end.
	got = composeSteeringBrief(snap, "", "api")
	if len(got) > steeringBriefCap || strings.Contains(got, "- #500: ") || !strings.Contains(got, "- 20 older notes omitted") {
		t.Fatalf("notes not cut first (%d bytes)", len(got))
	}
	if !strings.Contains(got, "- #100 \"m0\"") || strings.Contains(got, "- #179 \"m79\"") || !strings.Contains(got, "more members omitted to fit; read them with get_tree 12.") {
		t.Fatalf("members not cut from the end:\n%s", got)
	}
	if !strings.Contains(got, "Members at launch (80):") || !strings.HasSuffix(got, "</stage-steering-brief>") {
		t.Fatal("header or close tag lost")
	}
}

// A playbook that alone overflows is cut on a rune boundary with a pointer.
func TestSteeringBriefOversizedPlaybook(t *testing.T) {
	snap := briefSnapshot()
	snap.Playbook = strings.Repeat("ü", steeringBriefCap)
	got := composeSteeringBrief(snap, "", "api")
	if len(got) > steeringBriefCap || !utf8.ValidString(got) {
		t.Fatalf("brief %d bytes, valid %v", len(got), utf8.ValidString(got))
	}
	if !strings.Contains(got, `[Playbook truncated. Read the full snippet "steering-playbook" with list_snippets.]`) ||
		!strings.Contains(got, "- 2 more members omitted") || !strings.Contains(got, "- 2 older notes omitted") {
		t.Fatalf("pointers missing:\n%s", got[:2000])
	}
}

func TestDefaultPlaybookSections(t *testing.T) {
	for _, want := range []string{"## Working with tasks", "## Steering toward the user's intent", "## Making decisions", "## Reviews", "## Merging", "## Adding follow-ups", "## When to ask the user"} {
		if !strings.Contains(defaultPlaybook, want) {
			t.Fatalf("default playbook misses %q", want)
		}
	}
}
