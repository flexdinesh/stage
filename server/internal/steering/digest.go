package steering

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
)

// A digest is one steering update: the binding's pending events, one line
// each, oldest first, wrapped in <stage-steering tree= through=> and capped
// at MaxDigest bytes (docs/api.md "Steering delivery").

// digest is what one update says.
type digest struct {
	Tree, Through int64
	// Steerer is the steerer task (the final instruction names it).
	Steerer int64
	// Lines are the event lines, oldest first, with their event ids.
	Lines []eventLine
	// Pending counts every pending event (Lines may hold fewer); FirstID and
	// LastID bound them.
	Pending         int64
	FirstID, LastID int64
	// Final: the tree finished; the update tells the steerer to end.
	Final bool
	// Waiting: the steerer waits on the user; the update reminds it.
	Waiting bool
}

type eventLine struct {
	ID   int64
	Text string
}

// maxLine bounds one event line (bytes), so one event never fills an update.
const maxLine = 2 << 10

// render is d's text, at most limit bytes: lines that do not fit, and the
// pending events not in Lines, are summarized by one pointer to the feed.
func (d digest) render(limit int) string {
	head := fmt.Sprintf(`<stage-steering tree="%d" through="%d">`, d.Tree, d.Through)
	const foot = "</stage-steering>"
	var tail []string
	if d.Final {
		tail = append(tail, fmt.Sprintf("The tree finished and steering ends with this update. Write an outcome note on your task (append_note {taskId: %d, category: \"outcome\", text, requestId}) summarizing what the tree shipped and what is left, then end your turn. Stage completes your task when this turn ends.", d.Steerer))
	}
	if d.Waiting {
		tail = append(tail, fmt.Sprintf("Reminder: your own question to the user is still open (task #%d needs input). Their answer still applies; act on these updates without dropping it.", d.Steerer))
	}
	// Reserve the overflow pointer at its longest so it always fits.
	reserve := len(d.overflow(d.FirstID, d.LastID, d.Pending, d.LastID)) + 1
	budget := limit - len(head) - len(foot) - 2 - reserve
	for _, t := range tail {
		budget -= len(t) + 1
	}
	var b strings.Builder
	b.WriteString(head)
	b.WriteByte('\n')
	shown, last := int64(0), d.FirstID-1
	for _, l := range d.Lines {
		text := clip(l.Text, maxLine)
		if len(text)+1 > budget {
			break
		}
		budget -= len(text) + 1
		b.WriteString(text)
		b.WriteByte('\n')
		shown++
		last = l.ID
	}
	if rest := d.Pending - shown; rest > 0 {
		first := d.FirstID
		if shown > 0 {
			first = d.nextAfter(last)
		}
		b.WriteString(d.overflow(first, d.LastID, rest, last))
		b.WriteByte('\n')
	}
	for _, t := range tail {
		b.WriteString(t)
		b.WriteByte('\n')
	}
	b.WriteString(foot)
	return b.String()
}

// nextAfter is the id of the first line after id (else LastID).
func (d digest) nextAfter(id int64) int64 {
	for _, l := range d.Lines {
		if l.ID > id {
			return l.ID
		}
	}
	return d.LastID
}

func (d digest) overflow(first, last, n, after int64) string {
	return fmt.Sprintf("%d more events (#%d-#%d): get_steering_events {treeId: %d, after: %d}", n, first, last, d.Tree, after)
}

// clip cuts s to at most n bytes on a rune boundary, marking the cut.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	const mark = "…"
	cut := n - len(mark)
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut] + mark
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// oneLine flattens s to one line of at most n bytes.
func oneLine(s string, n int) string {
	return clip(strings.Join(strings.Fields(s), " "), n)
}

// noRequests is a needs_input member's summary when its harness lists no
// requests Stage can answer.
const noRequests = "no listable requests: reply with send_message, or escalate_to_user"

// eventText is e's digest line (without the trailing newline). summary is
// a needs_input event's request summary.
func eventText(e db.SteeringEvent, summary string) string {
	var p map[string]any
	_ = json.Unmarshal(e.Payload, &p)
	str := func(k string) string {
		v, _ := p[k].(string)
		return v
	}
	num := func(k string) int64 {
		v, _ := p[k].(float64)
		return int64(v)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "#%d", e.ID)
	task := int64(0)
	if e.TaskID != nil {
		task = *e.TaskID
		fmt.Fprintf(&b, " task #%d", task)
		if t := str("title"); t != "" {
			fmt.Fprintf(&b, " %q", oneLine(t, 120))
		}
	}
	fmt.Fprintf(&b, " %s", e.Kind)
	switch e.Kind {
	case db.SteerMemberNeedsInput:
		rev := num("stateRevision")
		fmt.Fprintf(&b, " (stateRevision %d): %s. Answer with respond_request {taskId: %d, requestId, expectedStateRevision: %d} or send_message {taskId: %d, text, expectedStateRevision: %d}, or escalate_to_user {taskId: %d, reason}", rev, summary, task, rev, task, rev, task)
	case db.SteerMemberDone:
		if r := str("reply"); r != "" {
			trunc := ""
			if v, _ := p["replyTruncated"].(bool); v {
				trunc = " (truncated)"
			}
			fmt.Fprintf(&b, ": reply %q%s", oneLine(r, 600), trunc)
		}
		fmt.Fprintf(&b, ". Review with get_completion_preview {taskId: %d}, then complete_task", task)
	case db.SteerMemberFailed, db.SteerMemberInterrupted:
		if msg := str("error"); msg != "" {
			fmt.Fprintf(&b, ": %s", oneLine(msg, 300))
		}
		fmt.Fprintf(&b, ". Inspect with get_task {taskId: %d}; retry with start_task {taskId: %d}", task, task)
	case db.SteerMemberCancelled:
		fmt.Fprintf(&b, ". Inspect with get_task {taskId: %d}", task)
	case db.SteerMemberReady:
		fmt.Fprintf(&b, ": its needs are accepted. Start it with start_task {taskId: %d}", task)
	case db.SteerMemberAccepted, db.SteerMemberReopened:
		fmt.Fprintf(&b, " (acceptance revision %d)", num("revision"))
	case db.SteerMemberAnswered:
		if via := str("via"); via != "" {
			fmt.Fprintf(&b, " (via %s)", via)
		}
	case db.SteerNote:
		fmt.Fprintf(&b, ": %s note #%d %q", str("category"), num("noteId"), oneLine(str("text"), 600))
		if v, _ := p["truncated"].(bool); v {
			fmt.Fprintf(&b, " (truncated; get_journal_entry {id: %d})", num("noteId"))
		}
	case db.SteerTreeStatus:
		fmt.Fprintf(&b, ": %s -> %s", str("from"), str("to"))
	case db.SteerEnded, db.SteerTreeFinished:
		if r := str("reason"); r != "" {
			fmt.Fprintf(&b, " (%s)", r)
		}
	case db.SteerStarted, db.SteerMemberAdded, db.SteerMemberRemoved:
	default:
		if len(p) > 0 {
			raw, _ := json.Marshal(p)
			fmt.Fprintf(&b, ": %s", oneLine(string(raw), 300))
		}
	}
	if e.CausedBy != nil && e.CausedBy.TaskID != nil {
		fmt.Fprintf(&b, " [by task #%d]", *e.CausedBy.TaskID)
	}
	return b.String()
}

// eventRevision is a needs_input event's stateRevision.
func eventRevision(e db.SteeringEvent) int64 {
	var p struct {
		StateRevision int64 `json:"stateRevision"`
	}
	_ = json.Unmarshal(e.Payload, &p)
	return p.StateRevision
}
