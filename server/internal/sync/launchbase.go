package sync

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/t3"
)

// depSources are a launching task's finished needs (handoff sources), looked
// up once and each read from T3 at most once per launch (shared by the
// handoff and the predecessor base).
type depSources struct {
	s    *Service
	task db.Task

	srcDone bool
	srcs    []db.HandoffSource
	srcErr  error

	reads map[string]threadRead // by thread id
}

type threadRead struct {
	detail t3.ThreadDetail
	err    error
}

func (s *Service) depSourcesOf(task db.Task) *depSources {
	return &depSources{s: s, task: task, reads: map[string]threadRead{}}
}

// sources are the task's needs in settled or done with a thread, by id
// (db.HandoffSources); nil without needs.
func (d *depSources) sources(ctx context.Context) ([]db.HandoffSource, error) {
	if !d.srcDone {
		d.srcDone = true
		if len(d.task.Needs) > 0 {
			d.srcs, d.srcErr = d.s.store.HandoffSources(ctx, d.task.ID)
		}
	}
	return d.srcs, d.srcErr
}

// read is the live t3_thread_read of src (bounded by handoffReadTimeout).
func (d *depSources) read(ctx context.Context, src db.HandoffSource) (t3.ThreadDetail, error) {
	if r, ok := d.reads[src.ThreadID]; ok {
		return r.detail, r.err
	}
	ctx, cancel := context.WithTimeout(ctx, handoffReadTimeout)
	defer cancel()
	detail, err := d.s.t3.ReadThread(ctx, src.ThreadID)
	if err != nil {
		err = fmt.Errorf("read %s: %w", src.Label(), err)
	}
	d.reads[src.ThreadID] = threadRead{detail, err}
	return detail, err
}

// baseSource picks the need whose branch a predecessor-base worktree starts
// from (docs/api.md "Predecessor base"): workspace.baseTaskId when it is a
// need, else the sole need. nil and the fallback reason otherwise, or when
// that need has not finished or has no thread.
func (d *depSources) baseSource(ctx context.Context) (*db.HandoffSource, string) {
	needs := d.task.Needs
	var pick int64
	switch base := d.task.Workspace.BaseTaskID; {
	case len(needs) == 0:
		return nil, "the task has no needs"
	case base != nil && slices.Contains(needs, *base):
		pick = *base
	case len(needs) == 1:
		pick = needs[0]
	default:
		return nil, fmt.Sprintf("the task has %d needs and no workspace.baseTaskId to pick one", len(needs))
	}
	srcs, err := d.sources(ctx)
	if err != nil {
		return nil, "find needs: " + err.Error()
	}
	for i := range srcs {
		if srcs[i].TaskID != nil && *srcs[i].TaskID == pick {
			return &srcs[i], ""
		}
	}
	return nil, fmt.Sprintf("#%d has not finished or has no thread", pick)
}

// ResolveBase decides a predecessor-base launch from the chosen need src
// (read as d): its branch when usable, else the fallback reason
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
// "predecessor" and records it: the chosen need's local branch
// (startFromOrigin false), or the task's own baseRef with a reason. An
// attempt that already recorded one (a reused clientRequestId) reuses it.
// fallback is the strategy to retry with when T3 rejects the predecessor
// branch (nil when the base already is the fallback).
func (s *Service) prepareBase(ctx context.Context, task db.Task, p *depSources) (ws, fallback *t3.WorkspaceStrategy) {
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

// resolveBase fills b's source fields and returns the chosen need's branch,
// or "" and the fallback reason.
func (s *Service) resolveBase(ctx context.Context, task db.Task, p *depSources, b *db.LaunchBase) (string, string) {
	src, reason := p.baseSource(ctx)
	if src == nil {
		return "", reason
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
