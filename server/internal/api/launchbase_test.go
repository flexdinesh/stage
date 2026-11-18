package api_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/t3"
)

type launchBaseRec struct {
	Attempt         int     `json:"attempt"`
	BaseRef         string  `json:"baseRef"`
	StartFromOrigin bool    `json:"startFromOrigin"`
	Source          string  `json:"source"`
	SourceTaskID    *int64  `json:"sourceTaskId"`
	SourceThreadID  *string `json:"sourceThreadId"`
	SourceTitle     string  `json:"sourceTitle"`
	Reason          *string `json:"reason"`
}

type baseTask struct {
	task
	Workspace struct {
		Type     string  `json:"type"`
		BaseRef  string  `json:"baseRef"`
		Branch   *string `json:"branch"`
		Base     string  `json:"base"`
		BaseTask *int64  `json:"baseTaskId"`
	} `json:"workspace"`
	CurrentLaunchBase *launchBaseRec `json:"currentLaunchBase"`
}

func (e *env) baseTask(id int64) baseTask {
	e.t.Helper()
	var out baseTask
	e.must("GET", fmt.Sprintf("/api/tasks/%d", id), nil, 200, &out)
	return out
}

// predecessorWS is a worktree from a need's branch, origin/main the fallback.
func predecessorWS() map[string]any {
	return map[string]any{"type": "worktree", "baseRef": "origin/main", "base": "predecessor"}
}

// lastWorkspace is the workspace strategy of the newest launch.
func (e *env) lastWorkspace() t3.WorkspaceStrategy {
	e.t.Helper()
	if len(e.fake.Launched) == 0 || e.fake.Launched[len(e.fake.Launched)-1].Workspace == nil {
		e.t.Fatal("no launch with a workspace")
	}
	return *e.fake.Launched[len(e.fake.Launched)-1].Workspace
}

// inWorktree gives thread a worktree on branch.
func (e *env) inWorktree(thread, branch string) {
	_ = e.fake.SetThread(thread, func(d *t3.ThreadDetail) {
		d.Branch, d.WorktreePath = branch, "/w/wt-"+branch
	})
}

func assertFallback(t *testing.T, got baseTask, ws t3.WorkspaceStrategy, reason string) {
	t.Helper()
	b := got.CurrentLaunchBase
	if got.State != "running" || b == nil || b.Source != "fallback" || b.BaseRef != "origin/main" || !b.StartFromOrigin ||
		b.Reason == nil || !strings.Contains(*b.Reason, reason) {
		t.Fatalf("want fallback %q: %+v / %+v", reason, got, b)
	}
	if ws.BaseRef != "origin/main" || ws.StartFromOrigin == nil || !*ws.StartFromOrigin {
		t.Fatalf("launch must use the fallback base: %+v", ws)
	}
}

func TestPredecessorBaseStacksOnBranch(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/stack")
	a := e.task(p.ID, "Build API", map[string]any{"start": true})
	b := e.task(p.ID, "Use API", map[string]any{"startMode": "auto", "needs": []int64{a.ID}, "workspace": predecessorWS(), "handoff": false})
	if got := e.baseTask(b.ID); got.Workspace.Base != "predecessor" || got.Workspace.BaseRef != "origin/main" || got.CurrentLaunchBase != nil {
		t.Fatalf("saved workspace %+v", got)
	}
	e.inWorktree(*a.T3ThreadID, "feat/api")
	e.acceptThread(*a.T3ThreadID)

	got := e.baseTask(b.ID)
	lb := got.CurrentLaunchBase
	if got.State != "running" || lb == nil || lb.Source != "predecessor" || lb.BaseRef != "feat/api" || lb.StartFromOrigin ||
		lb.Attempt != 1 || lb.SourceTaskID == nil || *lb.SourceTaskID != a.ID || lb.SourceThreadID == nil || *lb.SourceThreadID != *a.T3ThreadID {
		t.Fatalf("launch base %+v / %+v", got, lb)
	}
	ws := e.lastWorkspace()
	if ws.Type != t3.WorkspaceWorktree || ws.BaseRef != "feat/api" || ws.StartFromOrigin == nil || *ws.StartFromOrigin || ws.Branch != "" {
		t.Fatalf("launch workspace %+v", ws)
	}
	if !e.hasEvent(b.ID, fmt.Sprintf("base from #%d's branch feat/api", a.ID)) {
		t.Fatal("missing base event")
	}
}

func TestPredecessorBaseKeepsDependentBranch(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/named")
	a := e.task(p.ID, "a", map[string]any{"start": true})
	ws := predecessorWS()
	ws["branch"] = "feat/next"
	b := e.task(p.ID, "b", map[string]any{"startMode": "auto", "needs": []int64{a.ID}, "workspace": ws})
	e.inWorktree(*a.T3ThreadID, "feat/a")
	e.acceptThread(*a.T3ThreadID)
	if got := e.lastWorkspace(); got.BaseRef != "feat/a" || got.Branch != "feat/next" {
		t.Fatalf("dependent branch must stay its own: %+v", got)
	}
	if e.getTask(b.ID).State != "running" {
		t.Fatal("not running")
	}
}

func TestPredecessorBaseFromAdoptedSession(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/base-session")
	s := runningSession(t, e, p.T3ProjectID, "Planner")
	b := e.task(p.ID, "b", map[string]any{"startMode": "auto", "needsSessionId": s, "workspace": predecessorWS()})
	planner := b.Needs[0]
	e.inWorktree(s, "plan/x")
	e.acceptThread(s)
	got := e.baseTask(b.ID)
	if lb := got.CurrentLaunchBase; lb == nil || lb.Source != "predecessor" || lb.BaseRef != "plan/x" ||
		lb.SourceTaskID == nil || *lb.SourceTaskID != planner || lb.SourceTitle != "Planner" {
		t.Fatalf("session base %+v", lb)
	}
	if !e.hasEvent(b.ID, fmt.Sprintf("base from #%d's branch plan/x", planner)) {
		t.Fatal("missing event")
	}
}

// With several needs, workspace.baseTaskId picks the need to stack on.
func TestPredecessorBasePicksBaseTaskID(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/pick")
	a := e.task(p.ID, "a", map[string]any{"start": true})
	b := e.task(p.ID, "b", map[string]any{"start": true})
	ws := predecessorWS()
	ws["baseTaskId"] = b.ID
	c := e.task(p.ID, "c", map[string]any{"startMode": "auto", "needs": []int64{a.ID, b.ID}, "workspace": ws, "handoff": false})
	if got := e.baseTask(c.ID); got.Workspace.BaseTask == nil || *got.Workspace.BaseTask != b.ID {
		t.Fatalf("saved baseTaskId %+v", got.Workspace)
	}
	e.inWorktree(*a.T3ThreadID, "feat/a")
	e.inWorktree(*b.T3ThreadID, "feat/b")
	e.acceptThread(*a.T3ThreadID)
	e.acceptThread(*b.T3ThreadID)
	got := e.baseTask(c.ID)
	if lb := got.CurrentLaunchBase; got.State != "running" || lb == nil || lb.Source != "predecessor" || lb.BaseRef != "feat/b" ||
		lb.SourceTaskID == nil || *lb.SourceTaskID != b.ID {
		t.Fatalf("base from baseTaskId %+v / %+v", got, lb)
	}
	if e.lastWorkspace().BaseRef != "feat/b" {
		t.Fatalf("launch workspace %+v", e.lastWorkspace())
	}
}

func TestPredecessorBaseAmbiguousNeedsFallBack(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/ambiguous")
	a := e.task(p.ID, "a", map[string]any{"start": true})
	b := e.task(p.ID, "b", map[string]any{"start": true})
	c := e.task(p.ID, "c", map[string]any{"startMode": "auto", "needs": []int64{a.ID, b.ID}, "workspace": predecessorWS(), "handoff": false})
	e.inWorktree(*a.T3ThreadID, "feat/a")
	e.inWorktree(*b.T3ThreadID, "feat/b")
	e.acceptThread(*a.T3ThreadID)
	e.acceptThread(*b.T3ThreadID)
	assertFallback(t, e.baseTask(c.ID), e.lastWorkspace(), "the task has 2 needs and no workspace.baseTaskId")
}

func TestPredecessorBaseFallbacks(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(e *env, thread string)
		reason string
	}{
		{"root", func(e *env, thread string) {
			_ = e.fake.SetThread(thread, func(d *t3.ThreadDetail) { d.Branch, d.WorktreePath = "main", "" })
		}, "ran at project root"},
		{"no branch", func(e *env, thread string) {
			_ = e.fake.SetThread(thread, func(d *t3.ThreadDetail) { d.Branch, d.WorktreePath = "", "/w/wt" })
		}, "has no branch"},
		{"merged", func(e *env, thread string) {
			e.inWorktree(thread, "feat/done")
			_ = e.fake.SetThread(thread, func(d *t3.ThreadDetail) {
				d.PullRequests = []t3.PullRequest{{Repository: "me/app", Number: 1, URL: "u1", State: "merged"}}
			})
		}, "pull requests are merged"},
		{"read failure", func(e *env, thread string) {
			e.fake.RemoveThread(thread)
		}, "read #"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			p := e.project("/w/fb")
			a := e.task(p.ID, "a", map[string]any{"start": true})
			b := e.task(p.ID, "b", map[string]any{"startMode": "auto", "needs": []int64{a.ID}, "workspace": predecessorWS(), "handoff": false})
			_ = e.fake.SetStatus(*a.T3ThreadID, t3.StatusCompleted)
			_ = e.fake.Settle(*a.T3ThreadID)
			e.poll()
			e.complete(a.ID)
			c.setup(e, *a.T3ThreadID)
			_ = e.runner.Reconcile(t.Context())
			assertFallback(t, e.baseTask(b.ID), e.lastWorkspace(), c.reason)
			if !e.hasEvent(b.ID, "base fallback origin/main: ") {
				t.Fatal("missing fallback event")
			}
		})
	}
}

func TestPredecessorBaseMixedPRsStillStacks(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/mixed")
	a := e.task(p.ID, "a", map[string]any{"start": true})
	b := e.task(p.ID, "b", map[string]any{"startMode": "auto", "needs": []int64{a.ID}, "workspace": predecessorWS()})
	e.inWorktree(*a.T3ThreadID, "feat/a")
	_ = e.fake.SetThread(*a.T3ThreadID, func(d *t3.ThreadDetail) {
		d.PullRequests = []t3.PullRequest{{Number: 1, State: "merged"}, {Number: 2, State: "open"}}
	})
	e.acceptThread(*a.T3ThreadID)
	if lb := e.baseTask(b.ID).CurrentLaunchBase; lb == nil || lb.Source != "predecessor" || lb.BaseRef != "feat/a" {
		t.Fatalf("an open PR keeps the stack: %+v", lb)
	}
}

func TestPredecessorBaseOtherProjectFallsBack(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/here")
	other := e.project("/w/there")
	a := e.task(other.ID, "a", map[string]any{"start": true})
	b := e.task(p.ID, "b", map[string]any{"startMode": "auto", "needs": []int64{a.ID}, "workspace": predecessorWS()})
	e.inWorktree(*a.T3ThreadID, "feat/there")
	e.acceptThread(*a.T3ThreadID)
	assertFallback(t, e.baseTask(b.ID), e.lastWorkspace(), "ran in another project")
}

func TestPredecessorBaseRejectedBranchRetriesFallback(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/deleted")
	a := e.task(p.ID, "a", map[string]any{"start": true})
	b := e.task(p.ID, "b", map[string]any{"startMode": "auto", "needs": []int64{a.ID}, "workspace": predecessorWS()})
	e.inWorktree(*a.T3ThreadID, "feat/gone")
	e.fake.MissRef("feat/gone")
	before := len(e.t3.launchRequestIDs())
	e.acceptThread(*a.T3ThreadID)
	got := e.baseTask(b.ID)
	assertFallback(t, got, e.lastWorkspace(), "branch feat/gone not found in project")
	ids := e.t3.launchRequestIDs()[before:]
	if len(ids) != 2 || ids[0] != ids[1] {
		t.Fatalf("rejection must retry once with the same clientRequestId: %v", ids)
	}
	if lb := got.CurrentLaunchBase; lb.SourceTaskID == nil || *lb.SourceTaskID != a.ID {
		t.Fatalf("fallback keeps the source: %+v", lb)
	}
	if !e.hasEvent(b.ID, "base from #") || !e.hasEvent(b.ID, "base fallback origin/main: branch feat/gone") {
		t.Fatal("events must show the attempt and the fallback")
	}
}

func TestPredecessorBaseReusedForSameAttempt(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/base-reuse")
	a := e.task(p.ID, "a", map[string]any{"start": true})
	b := e.task(p.ID, "b", map[string]any{"startMode": "auto", "needs": []int64{a.ID}, "workspace": predecessorWS()})
	e.inWorktree(*a.T3ThreadID, "feat/one")
	e.t3.lostLaunches.Store(2)
	e.acceptThread(*a.T3ThreadID)
	first := e.baseTask(b.ID)
	if first.State != "failed" || first.CurrentLaunchBase == nil || first.CurrentLaunchBase.BaseRef != "feat/one" {
		t.Fatalf("lost launch %+v", first)
	}
	e.inWorktree(*a.T3ThreadID, "feat/two")
	e.must("POST", fmt.Sprintf("/api/tasks/%d/start", b.ID), nil, 200, nil)
	got := e.baseTask(b.ID)
	if got.LaunchAttempt != first.LaunchAttempt || e.lastWorkspace().BaseRef != "feat/one" || got.CurrentLaunchBase.BaseRef != "feat/one" {
		t.Fatalf("same attempt must resend the stored base: %+v / %+v", got.CurrentLaunchBase, e.lastWorkspace())
	}
}

func TestPredecessorBaseWithoutNeedsFallsBack(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/unlinked")
	b := e.task(p.ID, "b", map[string]any{"workspace": predecessorWS(), "start": true})
	assertFallback(t, e.baseTask(b.ID), e.lastWorkspace(), "the task has no needs")
}

func TestRefBaseRecordsNothing(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/ref")
	a := e.task(p.ID, "a", map[string]any{"start": true})
	b := e.task(p.ID, "b", map[string]any{"startMode": "auto", "needs": []int64{a.ID}, "workspace": map[string]any{"type": "worktree", "baseRef": "origin/main"}})
	e.inWorktree(*a.T3ThreadID, "feat/a")
	e.acceptThread(*a.T3ThreadID)
	got := e.baseTask(b.ID)
	if got.Workspace.Base != "ref" || got.CurrentLaunchBase != nil || e.lastWorkspace().BaseRef != "origin/main" {
		t.Fatalf("ref base unchanged: %+v", got)
	}
}

func TestPredecessorBaseValidation(t *testing.T) {
	e := newEnv(t)
	p := e.project("/w/valid")
	body := func(ws map[string]any) map[string]any {
		return map[string]any{"projectId": p.ID, "title": "t", "prompt": "p", "workspace": ws}
	}
	e.must("POST", "/api/tasks", body(map[string]any{"type": "worktree", "baseRef": "origin/main", "base": "sideways"}), 400, nil)
	e.must("POST", "/api/tasks", body(map[string]any{"type": "root", "base": "predecessor"}), 400, nil)
	e.must("POST", "/api/tasks", body(map[string]any{"type": "worktree", "base": "predecessor"}), 400, nil)
	sched := body(predecessorWS())
	sched["cron"], sched["tz"] = "0 9 * * *", "UTC"
	e.must("POST", "/api/schedules", sched, 400, nil)

	var out baseTask
	tk := e.task(p.ID, "t", nil)
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", tk.ID), map[string]any{"workspace": predecessorWS()}, 200, &out)
	if out.Workspace.Base != "predecessor" {
		t.Fatalf("PATCH base %+v", out.Workspace)
	}
	e.must("PATCH", fmt.Sprintf("/api/tasks/%d", tk.ID), map[string]any{"workspace": map[string]any{"type": "worktree", "baseRef": "origin/main", "base": "ref"}}, 200, &out)
	if out.Workspace.Base != "ref" {
		t.Fatalf("PATCH base ref %+v", out.Workspace)
	}
}
