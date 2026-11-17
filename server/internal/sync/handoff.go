package sync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
)

const (
	// handoffBudgetChars is the reply budget shared by a launch's needs; each
	// need's final reply is capped at replyCap(n) (T3 UTF-16 units).
	handoffBudgetChars = 8000
	// handoffReplyMaxChars and handoffReplyMinChars bound one need's cap.
	handoffReplyMaxChars = 4000
	handoffReplyMinChars = 1000
	// handoffReadTimeout bounds one need's read; a slower T3 launches the
	// task without that need's block.
	handoffReadTimeout = 20 * time.Second
)

// replyCap is each need's final-reply cap when n needs hand off.
func replyCap(n int) int {
	return min(handoffReplyMaxChars, max(handoffReplyMinChars, handoffBudgetChars/max(n, 1)))
}

// ComposeHandoff is one need's block appended to a launch prompt: the need
// (src, read as d), its branch/worktree and PRs, and its final reply (nil:
// none found), marked when truncated at maxChars.
func ComposeHandoff(src db.HandoffSource, d t3.ThreadDetail, reply *t3.Reply, maxChars int) string {
	var b strings.Builder
	title := src.Title
	if title == "" {
		title = d.Title
	}
	kind := "session"
	if src.TaskID != nil {
		kind = fmt.Sprintf("task #%d", *src.TaskID)
	}
	b.WriteString("<handoff from=\"stage\">\n")
	fmt.Fprintf(&b, "This task needs %q (%s)", title, kind)
	if src.SettledAt != nil {
		fmt.Fprintf(&b, ", settled %s", src.SettledAt.UTC().Format(time.RFC3339))
	} else {
		b.WriteString(", finished, not settled yet")
	}
	b.WriteString(".\n")
	fmt.Fprintf(&b, "- T3 thread: %s", src.ThreadID)
	if d.Link != "" {
		b.WriteString(" " + d.Link)
	}
	b.WriteString(" (read more with t3_thread_read)\n")
	var ws []string
	if v := strings.TrimSpace(d.Branch); v != "" {
		ws = append(ws, "Branch: "+v)
	}
	if v := strings.TrimSpace(d.WorktreePath); v != "" {
		ws = append(ws, "Worktree: "+v)
	}
	if len(ws) > 0 {
		b.WriteString("- " + strings.Join(ws, " · ") + "\n")
	}
	if len(d.PullRequests) > 0 {
		b.WriteString("- Pull requests:\n")
		for _, pr := range d.PullRequests {
			name := pr.Repository
			if pr.Number > 0 {
				name = fmt.Sprintf("%s#%d", pr.Repository, pr.Number)
			}
			line := "  - " + strings.TrimSpace(name)
			b.WriteString(line + " " + pr.URL + "\n")
		}
	}
	if reply == nil || strings.TrimSpace(reply.Text) == "" {
		b.WriteString("Final reply: none found.\n")
	} else {
		b.WriteString("Final reply:\n")
		b.WriteString(strings.TrimRight(reply.Text, "\n") + "\n")
		if reply.Truncated {
			fmt.Fprintf(&b, "[truncated at %d chars; full reply via t3_thread_read %s]\n", maxChars, src.ThreadID)
		}
	}
	b.WriteString("</handoff>")
	return b.String()
}

// prepareHandoff returns the blocks to append to task's launch prompt (""
// for none): the task has handoff on and finished needs (sources), one
// block per need, by id. A need this attempt already sent (a reused
// clientRequestId) resends the stored text. A failed read is recorded as
// skipped for that need and never blocks the launch.
func (s *Service) prepareHandoff(ctx context.Context, task db.Task, deps *depSources) string {
	if !task.Handoff || len(task.Needs) == 0 {
		return ""
	}
	srcs, err := deps.sources(ctx)
	if err != nil {
		s.log.Warn("find handoff sources; launching without them", "task", task.ID, "err", err)
		return ""
	}
	if len(srcs) == 0 {
		return ""
	}
	prev, err := s.store.GetHandoffs(ctx, task.ID, task.LaunchAttempt)
	if err != nil {
		s.log.Warn("read handoffs", "task", task.ID, "err", err)
	}
	sent := map[int64]string{}
	for _, h := range prev {
		if h.Status == db.HandoffSent && h.SourceTaskID != nil {
			sent[*h.SourceTaskID] = h.Text
		}
	}
	limit := replyCap(len(srcs))
	var blocks []string
	for _, src := range srcs {
		if text, ok := sent[*src.TaskID]; ok {
			blocks = append(blocks, text)
			continue
		}
		text, err := s.readHandoff(ctx, src, deps, limit)
		if err != nil {
			s.skipHandoff(ctx, task, src, err)
			continue
		}
		h := db.Handoff{
			Attempt: task.LaunchAttempt, Status: db.HandoffSent, SourceTaskID: src.TaskID,
			SourceThreadID: src.ThreadID, SourceTitle: src.Title, Text: text,
		}
		if err := s.store.RecordHandoff(ctx, task.ID, h); err != nil {
			s.log.Warn("record handoff", "task", task.ID, "err", err)
		}
		blocks = append(blocks, text)
	}
	return strings.Join(blocks, "\n\n")
}

// readHandoff reads src's thread (shared with the predecessor base) and
// final reply (capped at maxChars) from T3 and composes its block.
func (s *Service) readHandoff(ctx context.Context, src db.HandoffSource, deps *depSources, maxChars int) (string, error) {
	d, err := deps.read(ctx, src)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, handoffReadTimeout)
	defer cancel()
	r, err := s.t3.LatestReply(ctx, t3.LatestReplyInput{ThreadID: src.ThreadID, MaxChars: maxChars, ItemCount: d.ItemCount})
	switch {
	case errors.Is(err, t3.ErrNoReply):
		return ComposeHandoff(src, d, nil, maxChars), nil
	case err != nil:
		return "", fmt.Errorf("read final reply of %s: %w", src.Label(), err)
	}
	return ComposeHandoff(src, d, &r, maxChars), nil
}

func (s *Service) skipHandoff(ctx context.Context, task db.Task, src db.HandoffSource, cause error) {
	s.log.Warn("handoff skipped; launching without it", "task", task.ID, "source", src.Label(), "err", cause)
	reason := cause.Error()
	h := db.Handoff{
		Attempt: task.LaunchAttempt, Status: db.HandoffSkipped, SourceTaskID: src.TaskID,
		SourceThreadID: src.ThreadID, SourceTitle: src.Title, Reason: &reason,
	}
	if err := s.store.RecordHandoff(ctx, task.ID, h); err != nil {
		s.log.Warn("record handoff", "task", task.ID, "err", err)
	}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
