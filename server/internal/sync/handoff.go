package sync

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
)

const (
	// handoffBudgetChars is the reply budget shared by a launch's needs; each
	// need's final reply is capped at replyCap(n) (Unicode characters).
	handoffBudgetChars = 8000
	// handoffReplyMaxChars and handoffReplyMinChars bound one need's cap.
	handoffReplyMaxChars = 4000
	handoffReplyMinChars = 1000
	// handoffReadTimeout bounds the optional predecessor workspace read.
	handoffReadTimeout = 20 * time.Second
)

// replyCap is each need's final-reply cap when n needs hand off.
func replyCap(n int) int {
	return min(handoffReplyMaxChars, max(handoffReplyMinChars, handoffBudgetChars/max(n, 1)))
}

func (s *Service) prepareHandoff(ctx context.Context, task db.Task) (string, error) {
	if !task.Handoff {
		return "", nil
	}
	sources, err := s.store.AcceptedLaunchInputs(ctx, task.ID, task.LaunchAttempt)
	if err != nil {
		return "", err
	}
	previous, err := s.store.GetHandoffs(ctx, task.ID, task.LaunchAttempt)
	if err != nil {
		return "", err
	}
	sent := map[int64]string{}
	var blocks []string
	for _, h := range previous {
		if h.Status == db.HandoffSent && h.SourceTaskID != nil {
			sent[*h.SourceTaskID] = h.Text
			blocks = append(blocks, h.Text)
		}
	}
	for _, source := range sources {
		if _, ok := sent[source.TaskID]; ok {
			continue
		}
		block := ComposeAcceptedHandoff(source, replyCap(len(sources)))
		thread := ""
		if source.Evidence.ThreadID != nil {
			thread = *source.Evidence.ThreadID
		}
		h := db.Handoff{Session: source.Evidence.Session, Attempt: task.LaunchAttempt, Status: db.HandoffSent, SourceTaskID: &source.TaskID, SourceThreadID: thread, SourceTitle: source.Evidence.Title, Text: block}
		if err := s.store.RecordHandoff(ctx, task.ID, h); err != nil {
			return "", err
		}
		blocks = append(blocks, block)
	}
	return strings.Join(blocks, "\n\n"), nil
}

// ComposeAcceptedHandoff uses only the immutable Stage result, including when
// the source harness is offline or the task has since been reopened.
func ComposeAcceptedHandoff(a db.Acceptance, maxChars int) string {
	e := a.Evidence
	var b strings.Builder
	fmt.Fprintf(&b, "<handoff from=\"stage\">\nThis task needs %q (task #%d), completed %s.\nAccepted result #%d (%s), captured %s.\n", e.Title, e.TaskID, a.AcceptedAt.UTC().Format(time.RFC3339), a.ID, a.Provenance, a.CapturedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "Task context: #%d; journal through entry #%d (read_journal / get_journal_entry).\n", e.TaskID, e.JournalThroughID)
	if e.TreeID != nil {
		fmt.Fprintf(&b, "Tree at acceptance: #%d.\n", *e.TreeID)
	}
	if e.Session != nil {
		fmt.Fprintf(&b, "Harness session: %s %s (connection %d)\n", e.Session.ExternalSessionID, deref(e.ThreadLink), e.Session.ConnectionID)
	} else if e.ThreadID != nil {
		fmt.Fprintf(&b, "Harness session: %s %s\n", *e.ThreadID, deref(e.ThreadLink))
	}
	if e.Branch != nil {
		fmt.Fprintf(&b, "Branch: %s\n", *e.Branch)
	}
	if e.WorktreePath != nil {
		fmt.Fprintf(&b, "Worktree: %s\n", *e.WorktreePath)
	}
	for _, link := range e.Artifacts {
		if link.URL != "" {
			fmt.Fprintf(&b, "Artifact: %s\n", link.URL)
		}
	}
	if e.Reply == nil || strings.TrimSpace(*e.Reply) == "" {
		b.WriteString("Final reply: missing at acceptance.\n")
	} else {
		if e.ReplyRunID == nil || e.RunID == nil || *e.ReplyRunID != *e.RunID {
			b.WriteString("Reply provenance: earlier run or unverified run; not a verified current-run final reply.\n")
		}
		text := []rune(*e.Reply)
		truncated := e.ReplyTruncated
		if len(text) > maxChars {
			text = text[:maxChars]
			truncated = true
		}
		b.WriteString("Cached final-reply excerpt:\n" + string(text) + "\n")
		if truncated {
			b.WriteString("[Excerpt truncated; accepted snapshot retained in Stage.]\n")
		}
	}
	if a.Note != "" {
		b.WriteString("User acceptance note:\n" + a.Note + "\n")
	}
	b.WriteString("</handoff>")
	return b.String()
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
