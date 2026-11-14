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
	// handoffReplyMaxChars caps the predecessor's final reply in the block
	// (T3 UTF-16 units).
	handoffReplyMaxChars = 4000
	// handoffReadTimeout bounds the predecessor read; a slower T3 launches
	// the task without the block.
	handoffReadTimeout = 20 * time.Second
)

// ComposeHandoff is the block appended to a linked follower's launch prompt:
// the settled predecessor (src, read as d), its branch/worktree and PRs, and
// its final reply (nil: none found), marked when truncated.
func ComposeHandoff(src db.HandoffSource, d t3.ThreadDetail, reply *t3.Reply) string {
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
	fmt.Fprintf(&b, "This task runs after %q (%s)", title, kind)
	if src.SettledAt != nil {
		fmt.Fprintf(&b, ", settled %s", src.SettledAt.UTC().Format(time.RFC3339))
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
			if pr.State != "" {
				line += " (" + pr.State + ")"
			}
			b.WriteString(line + " " + pr.URL + "\n")
		}
	}
	if reply == nil || strings.TrimSpace(reply.Text) == "" {
		b.WriteString("Final reply: none found.\n")
	} else {
		b.WriteString("Final reply:\n")
		b.WriteString(strings.TrimRight(reply.Text, "\n") + "\n")
		if reply.Truncated {
			fmt.Fprintf(&b, "[truncated at %d chars; full reply via t3_thread_read %s]\n", handoffReplyMaxChars, src.ThreadID)
		}
	}
	b.WriteString("</handoff>")
	return b.String()
}

// prepareHandoff returns the block to append to task's launch prompt ("" for
// none): the task has handoff on and a settled predecessor. An attempt that
// already sent one (a reused clientRequestId) resends the stored text. A
// failed read is recorded as skipped and never blocks the launch.
func (s *Service) prepareHandoff(ctx context.Context, task db.Task, p *predecessor) string {
	if !task.Handoff || (task.AfterTaskID == nil && task.AfterThreadID == nil) {
		return ""
	}
	src, err := p.source(ctx)
	if err != nil {
		s.skipHandoff(ctx, task, db.HandoffSource{ThreadID: deref(task.AfterThreadID), TaskID: task.AfterTaskID}, err)
		return ""
	}
	if src == nil {
		return ""
	}
	prev, err := s.store.GetHandoff(ctx, task.ID, task.LaunchAttempt)
	if err == nil && prev != nil && prev.Status == db.HandoffSent {
		return prev.Text
	}
	text, err := s.readHandoff(ctx, *src, p)
	if err != nil {
		s.skipHandoff(ctx, task, *src, err)
		return ""
	}
	h := db.Handoff{
		Attempt: task.LaunchAttempt, Status: db.HandoffSent, SourceTaskID: src.TaskID,
		SourceThreadID: src.ThreadID, SourceTitle: src.Title, Text: text,
	}
	if err := s.store.RecordHandoff(ctx, task.ID, h); err != nil {
		s.log.Warn("record handoff", "task", task.ID, "err", err)
	}
	return text
}

// readHandoff reads src's thread (shared with the predecessor base) and
// final reply from T3 and composes the block.
func (s *Service) readHandoff(ctx context.Context, src db.HandoffSource, p *predecessor) (string, error) {
	d, err := p.read(ctx, src)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, handoffReadTimeout)
	defer cancel()
	r, err := s.t3.LatestReply(ctx, t3.LatestReplyInput{ThreadID: src.ThreadID, MaxChars: handoffReplyMaxChars, ItemCount: d.ItemCount})
	switch {
	case errors.Is(err, t3.ErrNoReply):
		return ComposeHandoff(src, d, nil), nil
	case err != nil:
		return "", fmt.Errorf("read final reply of %s: %w", src.Label(), err)
	}
	return ComposeHandoff(src, d, &r), nil
}

func (s *Service) skipHandoff(ctx context.Context, task db.Task, src db.HandoffSource, cause error) {
	s.log.Warn("handoff skipped; launching without it", "task", task.ID, "err", cause)
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
