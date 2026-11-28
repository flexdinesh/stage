package coordinator

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/flexdinesh/stage/server/internal/db"
)

// Tree steering (ADR 0013): a steerer's launch appends a Stage-composed
// <stage-steering-brief>: the tree snapshot, its latest notes, the steering
// rules and the playbook (the steering-playbook snippet).

//go:embed playbook.md
var defaultPlaybook string

const (
	// steeringBriefCap bounds the brief in bytes.
	steeringBriefCap = 24 << 10
	// steeringNoteLimit is how many of the newest tree notes the brief lists.
	steeringNoteLimit = 20
	// steeringExcerptRunes bounds a member's last word and a note preview.
	steeringExcerptRunes = 300
)

// steeringSnapshot is what a brief is composed from.
type steeringSnapshot struct {
	Tree db.Tree
	// SteererID is the steering task.
	SteererID int64
	// Members by id; Branch is the branch the member's session reports.
	Members []steeringMember
	// Notes are the tree's newest notes, newest first.
	Notes    []db.JournalEntry
	Playbook string
}

type steeringMember struct {
	Task   db.Task
	Branch string
}

// prepareSteering is the steering brief for an active steerer's launch
// attempt, frozen per attempt ("" when the task steers no tree).
func (s *Service) prepareSteering(ctx context.Context, t db.Task) (string, error) {
	if t.Steering == nil || !t.Steering.Active {
		return "", nil
	}
	snap, err := s.steeringSnapshot(ctx, t)
	if err != nil {
		return "", err
	}
	brief := composeSteeringBrief(snap, s.CLI, s.httpAPI(t.ConnectionID))
	return s.store.SteeringLaunchContext(ctx, t, brief)
}

func (s *Service) steeringSnapshot(ctx context.Context, t db.Task) (steeringSnapshot, error) {
	tree, err := s.store.GetTree(ctx, t.Steering.TreeID)
	if err != nil {
		return steeringSnapshot{}, err
	}
	snap := steeringSnapshot{Tree: tree.Tree, SteererID: t.ID}
	for _, m := range tree.Tasks {
		member := steeringMember{Task: m}
		if m.Session != nil {
			o, err := s.store.Observation(ctx, *m.Session)
			if err != nil && !errors.Is(err, db.ErrNotFound) {
				return steeringSnapshot{}, err
			}
			member.Branch = o.Branch
		}
		snap.Members = append(snap.Members, member)
	}
	treeID := tree.ID
	notes, err := s.store.ReadJournal(ctx, db.JournalOwner{TreeID: &treeID}, db.JournalQuery{Kind: "notes", Limit: steeringNoteLimit})
	if err != nil {
		return steeringSnapshot{}, err
	}
	snap.Notes = notes.Entries
	if snap.Playbook, err = s.store.SteeringPlaybook(ctx, defaultPlaybook); err != nil {
		return steeringSnapshot{}, err
	}
	return snap, nil
}

// composeSteeringBrief renders snap within steeringBriefCap: when over,
// the oldest notes go first, then the last members, each cut ending with a
// pointer to the full read; a playbook that alone overflows is cut last.
// cli is the installed CLI's name (empty: "stage"); api names the HTTP API.
func composeSteeringBrief(snap steeringSnapshot, cli, api string) string {
	if cli == "" {
		cli = "stage"
	}
	members, notes := len(snap.Members), len(snap.Notes)
	for {
		brief := renderSteeringBrief(snap, members, notes, snap.Playbook, cli, api)
		if len(brief) <= steeringBriefCap {
			return brief
		}
		switch {
		case notes > 0:
			notes--
		case members > 0:
			members--
		default:
			return renderSteeringBrief(snap, 0, 0, fitPlaybook(snap, cli, api), cli, api)
		}
	}
}

// fitPlaybook cuts the playbook so the brief without members and notes
// fits, ending it with a pointer to the snippet.
func fitPlaybook(snap steeringSnapshot, cli, api string) string {
	pointer := fmt.Sprintf("\n[Playbook truncated. Read the full snippet %q with list_snippets.]", db.SteeringPlaybookName)
	room := steeringBriefCap - len(renderSteeringBrief(snap, 0, 0, "", cli, api)) - len(pointer)
	if room <= 0 {
		return strings.TrimPrefix(pointer, "\n")
	}
	playbook := snap.Playbook
	if len(playbook) > room {
		playbook = playbook[:room]
		for !utf8.ValidString(playbook) {
			playbook = playbook[:len(playbook)-1]
		}
	}
	return playbook + pointer
}

func renderSteeringBrief(snap steeringSnapshot, members, notes int, playbook, cli, api string) string {
	tree, me := snap.Tree, snap.SteererID
	var b strings.Builder
	fmt.Fprintf(&b, "<stage-steering-brief tree=%d>\n", tree.ID)
	fmt.Fprintf(&b, "You steer Stage tree #%d %q (start mode %s, status %s) as task #%d. You are not a member of it. Stage sends you the tree's changes and you act on its members in the user's place; the user can answer first and take over at any time.\n",
		tree.ID, tree.Title, tree.StartMode, tree.Status, me)

	fmt.Fprintf(&b, "\nMembers at launch (%d):\n", len(snap.Members))
	if len(snap.Members) == 0 {
		b.WriteString("- none yet\n")
	}
	for _, m := range snap.Members[:members] {
		writeSteeringMember(&b, m)
	}
	if left := len(snap.Members) - members; left > 0 {
		fmt.Fprintf(&b, "- %d more members omitted to fit; read them with get_tree %d.\n", left, tree.ID)
	}

	fmt.Fprintf(&b, "\nTree notes (newest first, previews; full text with get_journal_entry):\n")
	if len(snap.Notes) == 0 {
		b.WriteString("- none yet\n")
	}
	for _, n := range snap.Notes[:notes] {
		writeSteeringNote(&b, n)
	}
	if left := len(snap.Notes) - notes; left > 0 {
		fmt.Fprintf(&b, "- %d older notes omitted to fit; read them with read_journal treeId=%d.\n", left, tree.ID)
	}

	fmt.Fprintf(&b, `
Rules:
- Use whichever Stage interface you have: the %[3]s CLI (%[3]s call TOOL JSON runs any tool named here), Stage MCP tools, or %[4]s; pass your caller session as <stage-journal> says. HTTP equivalents: get_tree is GET /api/trees/{id}; get_steering_events is GET /api/trees/{id}/steering/events?after=N; send_message is POST /api/tasks/{id}/messages; respond_request is POST /api/tasks/{id}/requests/{requestId}; get_completion_preview is GET /api/tasks/{id}/completion-preview; complete_task is POST /api/tasks/{id}/complete; create_task is POST /api/tasks; escalate_to_user is POST /api/tasks/{id}/escalate.
- Answer a member (send_message, respond_request) with expectedStateRevision set to its stateRevision. 409 already_answered means another answer won: re-read the member and move on.
- Accept a member's result on your own: get_completion_preview, then complete_task with its revision and evidenceVersion.
- Add follow-up work with create_task, treeId %[1]d and needs.
- Record decisions as tree notes: append_note with treeId %[1]d, sourceTaskId %[2]d, category decision.
- Escalate by ending your turn with a question to the user, or with escalate_to_user {taskId, reason} for a held member.
- Never act on your own task #%[2]d; Stage refuses it.
- Updates arrive as <stage-steering tree=%[1]d through=N> messages. Catch up with get_steering_events treeId=%[1]d after=N. Duplicates are harmless: skip what you have seen.
- When told the tree finished, write an outcome note and end your turn.
`, tree.ID, me, cli, api)

	fmt.Fprintf(&b, "\nPlaybook (snippet %q; the user may edit it):\n", db.SteeringPlaybookName)
	b.WriteString(strings.TrimSpace(playbook))
	b.WriteString("\n</stage-steering-brief>")
	return b.String()
}

func writeSteeringMember(b *strings.Builder, m steeringMember) {
	t := m.Task
	state := string(t.State)
	if t.Readiness != nil {
		state += " (" + string(*t.Readiness) + ")"
	}
	fmt.Fprintf(b, "- #%d %q: %s, stateRevision %d", t.ID, t.Title, state, t.StateRevision)
	if len(t.Needs) > 0 {
		ids := make([]string, len(t.Needs))
		for i, n := range t.Needs {
			ids[i] = fmt.Sprintf("#%d", n)
		}
		fmt.Fprintf(b, ", needs %s", strings.Join(ids, " "))
	}
	if m.Branch != "" {
		fmt.Fprintf(b, ", branch %s", m.Branch)
	}
	b.WriteString("\n")
	if t.LastWordPreview != nil {
		fmt.Fprintf(b, "  Last word: %s\n", excerpt(*t.LastWordPreview, steeringExcerptRunes))
	}
}

func writeSteeringNote(b *strings.Builder, n db.JournalEntry) {
	fmt.Fprintf(b, "- #%d", n.ID)
	if n.Category != "" {
		fmt.Fprintf(b, " %s", n.Category)
	}
	switch {
	case n.TaskID != nil:
		fmt.Fprintf(b, " on #%d", *n.TaskID)
	case n.SourceTaskID != nil:
		fmt.Fprintf(b, " from #%d", *n.SourceTaskID)
	}
	fmt.Fprintf(b, ": %s\n", excerpt(n.Text, steeringExcerptRunes))
}

// excerpt is text on one line, at most limit runes (an ellipsis marks a cut).
func excerpt(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}
