package sync

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// predecessor is a launching task's settled predecessor, looked up and read
// from T3 at most once per launch (shared by the handoff and the
// predecessor base).
type predecessor struct {
	s    *Service
	task db.Task

	srcDone bool
	src     *db.HandoffSource
	srcErr  error

	readDone bool
	detail   t3.ThreadDetail
	readErr  error
}

func (s *Service) predecessorOf(task db.Task) *predecessor {
	return &predecessor{s: s, task: task}
}

// source is the settled predecessor; nil when the task is not linked, the
// predecessor has not passed, or the predecessor task has no thread.
func (p *predecessor) source(ctx context.Context) (*db.HandoffSource, error) {
	if !p.srcDone {
		p.srcDone = true
		if p.task.AfterTaskID != nil || p.task.AfterThreadID != nil {
			p.src, p.srcErr = p.s.store.HandoffSourceFor(ctx, p.task)
		}
	}
	return p.src, p.srcErr
}

// read is the live t3_thread_read of src (bounded by handoffReadTimeout).
func (p *predecessor) read(ctx context.Context, src db.HandoffSource) (t3.ThreadDetail, error) {
	if !p.readDone {
		p.readDone = true
		ctx, cancel := context.WithTimeout(ctx, handoffReadTimeout)
		defer cancel()
		p.detail, p.readErr = p.s.t3.ReadThread(ctx, src.ThreadID)
		if p.readErr != nil {
			p.readErr = fmt.Errorf("read %s: %w", src.Label(), p.readErr)
		}
	}
	return p.detail, p.readErr
}

// ResolveBase decides a predecessor-base launch from the settled predecessor
// src (read as d): its branch when usable, else the fallback reason
// (docs/api.md "Predecessor base"). t3ProjectID is the task's T3 project.
func ResolveBase(t3ProjectID string, src db.HandoffSource, d t3.ThreadDetail) (branch, reason string) {
	label := src.Label()
	branch = strings.TrimSpace(d.Branch)
	switch {
	case d.ProjectID != "" && d.ProjectID != t3ProjectID:
		return "", label + " ran in another project"
	case strings.TrimSpace(d.WorktreePath) == "":
		return "", label + " ran at project root"
	case branch == "":
		return "", label + " has no branch"
	case allMerged(d.PullRequests):
		return "", label + "'s pull requests are merged"
	}
	return branch, ""
}

// allMerged: at least one pull request and every one merged.
func allMerged(prs []t3.PullRequest) bool {
	if len(prs) == 0 {
		return false
	}
	for _, pr := range prs {
		if !strings.EqualFold(pr.State, "merged") {
			return false
		}
	}
	return true
}

// prepareBase resolves the worktree base of a task with workspace.base
// "predecessor" and records it: the predecessor's local branch
// (startFromOrigin false), or the task's own baseRef with a reason. An
// attempt that already recorded one (a reused clientRequestId) reuses it.
// fallback is the strategy to retry with when T3 rejects the predecessor
// branch (nil when the base already is the fallback).
func (s *Service) prepareBase(ctx context.Context, task db.Task, p *predecessor) (ws, fallback *t3.WorkspaceStrategy) {
	own := WorkspaceStrategy(task.Workspace)
	prev, err := s.store.GetLaunchBase(ctx, task.ID, task.LaunchAttempt)
	if err != nil {
		s.log.Warn("read launch base", "task", task.ID, "err", err)
	}
	if prev != nil {
		if prev.Source == db.LaunchBaseFallback {
			return own, nil
		}
		return baseStrategy(task.Workspace, prev.BaseRef), own
	}
	b := db.LaunchBase{Attempt: task.LaunchAttempt}
	branch, reason := s.resolveBase(ctx, task, p, &b)
	if branch == "" {
		s.recordBase(ctx, task, fallbackBase(b, own, reason))
		return own, nil
	}
	b.BaseRef, b.Source = branch, db.LaunchBasePredecessor
	s.recordBase(ctx, task, b)
	return baseStrategy(task.Workspace, branch), own
}

// resolveBase fills b's source fields and returns the predecessor's branch,
// or "" and the fallback reason.
func (s *Service) resolveBase(ctx context.Context, task db.Task, p *predecessor, b *db.LaunchBase) (string, string) {
	if task.AfterTaskID == nil && task.AfterThreadID == nil {
		return "", "not linked"
	}
	src, err := p.source(ctx)
	if err != nil {
		return "", "find predecessor: " + err.Error()
	}
	if src == nil {
		return "", "predecessor has not settled or has no thread"
	}
	b.SourceTaskID, b.SourceThreadID, b.SourceTitle = src.TaskID, &src.ThreadID, src.Title
	d, err := p.read(ctx, *src)
	if err != nil {
		return "", err.Error()
	}
	if b.SourceTitle == "" {
		b.SourceTitle = d.Title
	}
	return ResolveBase(task.T3ProjectID, *src, d)
}

// rejectBase records attempt's fallback after T3 rejected the predecessor
// branch (deleted, or not in the task's project) before creating a thread.
func (s *Service) rejectBase(ctx context.Context, task db.Task, own *t3.WorkspaceStrategy) {
	prev, err := s.store.GetLaunchBase(ctx, task.ID, task.LaunchAttempt)
	b := db.LaunchBase{Attempt: task.LaunchAttempt}
	branch := ""
	if err == nil && prev != nil {
		b, branch = *prev, prev.BaseRef
	}
	s.recordBase(ctx, task, fallbackBase(b, own, fmt.Sprintf("branch %s not found in project", branch)))
}

func fallbackBase(b db.LaunchBase, own *t3.WorkspaceStrategy, reason string) db.LaunchBase {
	b.Source, b.Reason = db.LaunchBaseFallback, &reason
	b.BaseRef = own.BaseRef
	b.StartFromOrigin = own.StartFromOrigin != nil && *own.StartFromOrigin
	return b
}

func (s *Service) recordBase(ctx context.Context, task db.Task, b db.LaunchBase) {
	if b.Source == db.LaunchBaseFallback {
		s.log.Info("predecessor base fell back", "task", task.ID, "base", b.BaseRef, "reason", deref(b.Reason))
	}
	if err := s.store.RecordLaunchBase(ctx, task.ID, b); err != nil {
		s.log.Warn("record launch base", "task", task.ID, "err", err)
	}
}

// baseStrategy is a worktree from the predecessor's local branch (shared
// refs: the predecessor's worktree branch resolves in the project repo).
func baseStrategy(w db.Workspace, branch string) *t3.WorkspaceStrategy {
	fromOrigin := false
	ws := &t3.WorkspaceStrategy{Type: t3.WorkspaceWorktree, BaseRef: branch, StartFromOrigin: &fromOrigin}
	if w.Branch != nil {
		ws.Branch = *w.Branch
	}
	return ws
}

// BaseRefRejected: T3 refused the launch because baseRef does not resolve in
// the project (checkWorktreeBase; no thread was created).
func BaseRefRejected(err error, baseRef string) bool {
	var te *t3.ToolError
	if !errors.As(err, &te) || te.Code != "invalid_request" {
		return false
	}
	return strings.Contains(te.Message, `Base ref "`+baseRef+`"`)
}
