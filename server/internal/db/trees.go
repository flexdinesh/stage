package db

import (
	"context"
	"errors"
	"fmt"
	"github.com/flexdinesh/stage/server/internal/db/store"
	"slices"
	"strings"
	"time"
)

// Tree mutations run inside one write unit of work (BEGIN IMMEDIATE on
// SQLite), which serializes
// them with every other write, so need checks and updates are atomic.

// StartMode is when a tree member launches once its needs settle.
type StartMode = store.StartMode

const (
	StartAuto   = store.StartAuto
	StartManual = store.StartManual
)

// StartModes are the valid start modes.
var StartModes = store.StartModes

// TreeStatus is a tree's lifecycle.
type TreeStatus = store.TreeStatus

const (
	TreeOpen     = store.TreeOpen
	TreeDone     = store.TreeDone
	TreeArchived = store.TreeArchived
)

// TreeStatuses are the valid tree statuses.
var TreeStatuses = store.TreeStatuses

// Readiness of a backlog tree member.
type Readiness = store.Readiness

const (
	ReadinessBlocked = store.ReadinessBlocked
	ReadinessReady   = store.ReadinessReady
)

type (
	// Tree is a DAG of tasks (docs/api.md "Trees").
	Tree = store.Tree
)

// TreeEdge: TaskID needs NeedID.
type TreeEdge struct {
	TaskID int64 `json:"taskId"`
	NeedID int64 `json:"needId"`
}

// treeEdges lists the needs of every member of trees.
func treeEdges(ctx context.Context, tx store.Tx, trees []int64) ([]TreeEdge, error) {
	rows, err := tx.Trees().Edges(ctx, trees)
	if err != nil {
		return nil, err
	}
	out := make([]TreeEdge, len(rows))
	for i, e := range rows {
		out[i] = TreeEdge{TaskID: e.TaskID, NeedID: e.NeedID}
	}
	return out, nil
}

// TreeDetail is a tree with its members (by id), edges and levels (task ids
// per level, roots first; TreeLevels).
type TreeDetail struct {
	Tree
	TreeTimes
	Tasks  []Task     `json:"tasks"`
	Edges  []TreeEdge `json:"edges"`
	Levels [][]int64  `json:"levels"`
	// Events: the members' newest task events (treeEventLimit), newest first.
	Events []TaskEvent `json:"events"`
}

// TreeTimes: StartedAt is the first member launch (nil before any);
// LastActivityAt includes tree, member, session and journal activity.
// ListTrees orders by it.
type TreeTimes struct {
	StartedAt      *time.Time `json:"startedAt"`
	LastActivityAt time.Time  `json:"lastActivityAt"`
}

// treeEventLimit caps TreeDetail.Events.
const treeEventLimit = 50

func treeTimes(t Tree, members []Task) TreeTimes {
	tt := TreeTimes{LastActivityAt: t.CreatedAt}
	if t.JournalActivityAt.After(tt.LastActivityAt) {
		tt.LastActivityAt = t.JournalActivityAt
	}
	later := func(v *time.Time) {
		if v != nil && v.After(tt.LastActivityAt) {
			tt.LastActivityAt = *v
		}
	}
	for _, m := range members {
		if m.LaunchedAt != nil && (tt.StartedAt == nil || m.LaunchedAt.Before(*tt.StartedAt)) {
			tt.StartedAt = m.LaunchedAt
		}
		later(&m.UpdatedAt)
		later(m.SessionUpdatedAt)
	}
	return tt
}

// TreeCounts counts a tree's members by state; Blocked and Ready split
// backlog by readiness.
type TreeCounts struct {
	Total       int `json:"total"`
	Backlog     int `json:"backlog"`
	Blocked     int `json:"blocked"`
	Ready       int `json:"ready"`
	Launching   int `json:"launching"`
	Running     int `json:"running"`
	NeedsInput  int `json:"needsInput"`
	Done        int `json:"done"`
	Completed   int `json:"completed"`
	Failed      int `json:"failed"`
	Interrupted int `json:"interrupted"`
	Cancelled   int `json:"cancelled"`
}

// TreeSummary is a tree list row with its member roll-up.
type TreeSummary struct {
	Tree
	TreeTimes
	Counts   TreeCounts `json:"counts"`
	Levels   int        `json:"levels"`
	Projects []string   `json:"projects"` // member project titles, sorted
	// CompletedAt: latest acceptance when every member is completed (nil otherwise).
	CompletedAt *time.Time `json:"completedAt"`
}

// TreeView picks trees by lifecycle.
type TreeView = store.TreeView

const (
	TreeViewActive   = store.TreeViewActive
	TreeViewDone     = store.TreeViewDone
	TreeViewArchived = store.TreeViewArchived
	TreeViewAll      = store.TreeViewAll
)

// TreeViews are the valid tree views.
var TreeViews = store.TreeViews

// TreeFilter selects trees.
type TreeFilter = store.TreeFilter

// TreeMember is a task joining a tree with its needs (nil = keep current).
type TreeMember struct {
	TaskID int64   `json:"taskId"`
	Needs  []int64 `json:"needs"`
}

// NewTree is a validated create input.
type NewTree struct {
	Title     string
	StartMode StartMode
	Tasks     []TreeMember
}

// TreePatch is a partial tree update.
type TreePatch struct {
	Title     Optional[string]    `json:"title"`
	StartMode Optional[StartMode] `json:"startMode"`
	// Status is applied by SetTreeStatus (UpdateTree ignores it).
	Status Optional[TreeStatus] `json:"status"`
}

// TreeLevels groups ids into levels: a task's level is the longest need path
// to a root (a task without needs in ids is level 0). Edges to tasks
// outside ids are ignored; each level is sorted by id.
func TreeLevels(ids []int64, edges []TreeEdge) [][]int64 {
	in := make(map[int64]bool, len(ids))
	for _, id := range ids {
		in[id] = true
	}
	needs := map[int64][]int64{}
	for _, e := range edges {
		if in[e.TaskID] && in[e.NeedID] {
			needs[e.TaskID] = append(needs[e.TaskID], e.NeedID)
		}
	}
	level := map[int64]int{}
	visiting := map[int64]bool{}
	var depth func(id int64) int
	depth = func(id int64) int {
		if l, ok := level[id]; ok {
			return l
		}
		if visiting[id] {
			return 0 // a cycle (never stored): break it
		}
		visiting[id] = true
		l := 0
		for _, n := range needs[id] {
			l = max(l, depth(n)+1)
		}
		visiting[id] = false
		level[id] = l
		return l
	}
	out := [][]int64{}
	sorted := slices.Clone(ids)
	slices.Sort(sorted)
	for _, id := range slices.Compact(sorted) {
		l := depth(id)
		for len(out) <= l {
			out = append(out, []int64{})
		}
		out[l] = append(out[l], id)
	}
	return out
}

// CreateTree creates a tree with its members (each joins, leaving any other
// tree by splice), then sets each member's needs.
func (s *Store) CreateTree(ctx context.Context, in NewTree) (TreeDetail, error) {
	if len(in.Tasks) == 0 {
		return TreeDetail{}, Invalid("tasks: list at least one task")
	}
	seen := map[int64]bool{}
	for _, m := range in.Tasks {
		if seen[m.TaskID] {
			return TreeDetail{}, Invalid("tasks: task %d is listed twice", m.TaskID)
		}
		seen[m.TaskID] = true
	}
	mode := in.StartMode
	if mode == "" {
		mode = StartAuto
	}
	var id int64
	err := s.e.Write(ctx, func(tx store.Tx) error {
		var tasks []int64
		for _, m := range in.Tasks {
			tasks = append(append(tasks, m.TaskID), m.Needs...)
		}
		if err := lockTreeWork(ctx, tx, nil, tasks); err != nil {
			return err
		}
		var err error
		if id, err = insertTree(ctx, tx, in.Title, mode); err != nil {
			return err
		}
		for _, m := range in.Tasks {
			if err := joinTree(ctx, tx, m.TaskID, id); err != nil {
				return err
			}
		}
		for _, m := range in.Tasks {
			if m.Needs == nil {
				continue
			}
			if err := placeNeeds(ctx, tx, m.TaskID, &id, m.Needs); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return TreeDetail{}, err
	}
	return s.GetTree(ctx, id)
}

// GetTree returns a tree with its members, edges and levels.
func (s *Store) GetTree(ctx context.Context, id int64) (TreeDetail, error) {
	return read(ctx, s, func(tx store.Tx) (TreeDetail, error) {
		tr := tx.Trees()
		tree, err := tr.Get(ctx, id)
		if err != nil {
			return TreeDetail{}, fmt.Errorf("tree: %w", err)
		}
		tasks, err := tr.Members(ctx, []int64{id})
		if err != nil {
			return TreeDetail{}, err
		}
		edges, err := treeEdges(ctx, tx, []int64{id})
		if err != nil {
			return TreeDetail{}, err
		}
		events, err := tr.Events(ctx, id, treeEventLimit)
		if err != nil {
			return TreeDetail{}, err
		}
		return TreeDetail{Tree: tree, TreeTimes: treeTimes(tree, tasks), Tasks: tasks, Edges: edges,
			Levels: TreeLevels(idsOf(tasks), edges), Events: events}, nil
	})
}

func idsOf(tasks []Task) []int64 {
	out := make([]int64, len(tasks))
	for i, t := range tasks {
		out[i] = t.ID
	}
	return out
}

// ListTrees returns trees matching f with their roll-up, most recent member
// activity first.
func (s *Store) ListTrees(ctx context.Context, f TreeFilter) ([]TreeSummary, error) {
	return read(ctx, s, func(tx store.Tx) ([]TreeSummary, error) {
		tr := tx.Trees()
		trees, err := tr.List(ctx, f)
		if err != nil {
			return nil, err
		}
		out := make([]TreeSummary, len(trees))
		if len(trees) == 0 {
			return out, nil
		}
		ids := make([]int64, len(trees))
		for i, t := range trees {
			ids[i] = t.ID
		}
		members, err := tr.Members(ctx, ids)
		if err != nil {
			return nil, err
		}
		edges, err := treeEdges(ctx, tx, ids)
		if err != nil {
			return nil, err
		}
		byTree := map[int64][]Task{}
		for _, m := range members {
			byTree[*m.TreeID] = append(byTree[*m.TreeID], m)
		}
		for i, t := range trees {
			out[i] = summarize(t, byTree[t.ID], edges)
		}
		return out, nil
	})
}

// summarize rolls up a tree's members.
func summarize(t Tree, members []Task, edges []TreeEdge) TreeSummary {
	sum := TreeSummary{Tree: t, TreeTimes: treeTimes(t, members), Projects: []string{}}
	c := &sum.Counts
	var latest *time.Time
	for _, m := range members {
		c.Total++
		switch {
		case m.AcceptanceID != nil:
			c.Completed++
			if m.CompletedAt != nil && (latest == nil || m.CompletedAt.After(*latest)) {
				latest = m.CompletedAt
			}
		case m.State == StateBacklog:
			c.Backlog++
			if m.Readiness != nil && *m.Readiness == ReadinessBlocked {
				c.Blocked++
			}
			if m.Readiness != nil && *m.Readiness == ReadinessReady {
				c.Ready++
			}
		case m.State == StateLaunching:
			c.Launching++
		case m.State == StateRunning:
			c.Running++
		case m.State == StateNeedsInput:
			c.NeedsInput++
		case m.State == StateDone:
			c.Done++
		case m.State == StateFailed:
			c.Failed++
		case m.State == StateInterrupted:
			c.Interrupted++
		case m.State == StateCancelled:
			c.Cancelled++
		}
		if !slices.Contains(sum.Projects, m.ProjectTitle) {
			sum.Projects = append(sum.Projects, m.ProjectTitle)
		}
	}
	slices.Sort(sum.Projects)
	if c.Total > 0 && c.Completed == c.Total {
		sum.CompletedAt = latest
	}
	sum.Levels = len(TreeLevels(idsOf(members), edges))
	return sum
}

// UpdateTree applies a validated patch. Start mode changes only while the
// tree is open (Conflict).
func (s *Store) UpdateTree(ctx context.Context, id int64, p TreePatch) (Tree, error) {
	err := s.e.Write(ctx, func(tx store.Tx) error {
		if err := lockTreeWork(ctx, tx, []int64{id}, nil); err != nil {
			return err
		}
		if err := treeExists(ctx, tx, id); err != nil {
			return err
		}
		if p.StartMode.Set {
			if err := treeOpen(ctx, tx, id); err != nil {
				return err
			}
		}
		return updateTree(ctx, tx, id, p.Title, p.StartMode)
	})
	if err != nil {
		return Tree{}, err
	}
	t, err := read(ctx, s, func(tx store.Tx) (Tree, error) { return tx.Trees().Get(ctx, id) })
	if err != nil {
		return Tree{}, fmt.Errorf("tree: %w", err)
	}
	return t, nil
}

// SetTreeStatus marks tree id done, archives it, or reopens it (open). Done
// and archived need no member launching, running or needing input
// (Conflict naming it); members keep their states.
func (s *Store) SetTreeStatus(ctx context.Context, id int64, status TreeStatus) (TreeDetail, error) {
	err := s.e.Write(ctx, func(tx store.Tx) error {
		if err := lockTreeWork(ctx, tx, []int64{id}, nil); err != nil {
			return err
		}
		if err := treeExists(ctx, tx, id); err != nil {
			return err
		}
		if status != TreeOpen {
			if err := noActiveTask(tx.Trees().ActiveMember(ctx, id)); err != nil {
				return err
			}
		}
		return setTreeStatus(ctx, tx, id, status)
	})
	if err != nil {
		return TreeDetail{}, err
	}
	return s.GetTree(ctx, id)
}

// noActiveTask: Conflict naming the active task found (ErrNotFound: none).
func noActiveTask(id int64, state TaskState, err error) error {
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return Conflict("#%d is %s: stop it first", id, strings.ReplaceAll(string(state), "_", " "))
}

// treeOpen: Conflict unless tree id is open (structural changes need an open
// tree). A missing tree is not checked here.
func treeOpen(ctx context.Context, tx store.Tx, id int64) error {
	status, err := tx.Trees().Status(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	switch status {
	case TreeDone:
		return Conflict("tree %d is done: reopen it first", id)
	case TreeArchived:
		return Conflict("tree %d is archived: unarchive it first", id)
	}
	return nil
}

// DeleteTree deletes a tree and its members only if neither the tree nor
// any member has notes or execution history, including former members
// recorded since journal rollout. Otherwise archive it.
func (s *Store) DeleteTree(ctx context.Context, id int64) error {
	return s.e.Write(ctx, func(tx store.Tx) error {
		tr := tx.Trees()
		if err := lockTreeWork(ctx, tx, []int64{id}, nil); err != nil {
			return err
		}
		if err := treeExists(ctx, tx, id); err != nil {
			return err
		}
		protected, err := tr.RecordedWork(ctx, id)
		if err != nil {
			return err
		}
		if protected {
			return Conflict("tree %d has recorded work: archive it instead", id)
		}
		started, err := tr.FirstStarted(ctx, id)
		if err != nil {
			return err
		}
		if started != nil {
			return Conflict("#%d has started: archive the tree instead", *started)
		}
		if err := tr.DeleteMembers(ctx, id); err != nil {
			return err
		}
		return tr.Delete(ctx, id)
	})
}

// DissolveTree detaches members and archives the tree, preserving its journal.
func (s *Store) DissolveTree(ctx context.Context, id int64) error {
	return s.e.Write(ctx, func(tx store.Tx) error {
		if err := lockTreeWork(ctx, tx, []int64{id}, nil); err != nil {
			return err
		}
		if err := treeExists(ctx, tx, id); err != nil {
			return err
		}
		if err := detachMembers(ctx, tx, id); err != nil {
			return err
		}
		return archiveDissolved(ctx, tx, id)
	})
}

// AddToTree puts task taskID in tree treeID (a member of another tree moves:
// it is spliced out there first) and, when needs is non-nil, sets its needs.
func (s *Store) AddToTree(ctx context.Context, treeID, taskID int64, needs []int64) (TreeDetail, error) {
	err := s.e.Write(ctx, func(tx store.Tx) error {
		if err := lockTreeWork(ctx, tx, []int64{treeID}, append([]int64{taskID}, needs...)); err != nil {
			return err
		}
		if err := treeExists(ctx, tx, treeID); err != nil {
			return err
		}
		if err := joinTree(ctx, tx, taskID, treeID); err != nil {
			return err
		}
		if needs != nil {
			return placeNeeds(ctx, tx, taskID, &treeID, needs)
		}
		return nil
	})
	if err != nil {
		return TreeDetail{}, err
	}
	return s.GetTree(ctx, treeID)
}

// RemoveFromTree splices task taskID out of tree treeID: its dependents take
// over its needs; it stays as a standalone task. The emptied tree is archived.
func (s *Store) RemoveFromTree(ctx context.Context, treeID, taskID int64) error {
	return s.e.Write(ctx, func(tx store.Tx) error {
		if err := lockTreeWork(ctx, tx, []int64{treeID}, []int64{taskID}); err != nil {
			return err
		}
		tree, err := tx.Tasks().TreeID(ctx, taskID)
		if err != nil {
			return fmt.Errorf("task: %w", err)
		}
		if tree == nil || *tree != treeID {
			return fmt.Errorf("task %d in tree %d: %w", taskID, treeID, ErrNotFound)
		}
		return spliceOut(ctx, tx, taskID)
	})
}

// SetNeeds replaces task id's needs (setNeeds rules). A task outside a tree
// joins its needs' tree (or a new one, resolveTree).
func (s *Store) SetNeeds(ctx context.Context, id int64, needs []int64) (Task, error) {
	err := s.e.Write(ctx, func(tx store.Tx) error {
		if err := lockTreeWork(ctx, tx, nil, append([]int64{id}, needs...)); err != nil {
			return err
		}
		tree, err := tx.Tasks().TreeID(ctx, id)
		if err != nil {
			return err
		}
		return placeNeeds(ctx, tx, id, tree, needs)
	})
	if err != nil {
		return Task{}, err
	}
	return s.GetTask(ctx, id)
}

// StartableInTree: the tree's ready members (Start ready), by id.
func (s *Store) StartableInTree(ctx context.Context, treeID int64) ([]int64, error) {
	return read(ctx, s, func(tx store.Tx) ([]int64, error) {
		if err := treeExists(ctx, tx, treeID); err != nil {
			return nil, err
		}
		if err := treeOpen(ctx, tx, treeID); err != nil {
			return nil, err
		}
		return tx.Trees().Startable(ctx, treeID)
	})
}

// LaunchableTasks: auto members whose needs all accepted (the release runner
// launches them), by id.
func (s *Store) LaunchableTasks(ctx context.Context) ([]int64, error) {
	return s.LaunchableForConnection(ctx, 0)
}
func (s *Store) LaunchableForConnection(ctx context.Context, connection int64) ([]int64, error) {
	return read(ctx, s, func(tx store.Tx) ([]int64, error) { return tx.Trees().Launchable(ctx, connection) })
}

// MarkReady records readiness episodes of manual members with needs: clears
// ready_at where the task is no longer ready, sets it (once per episode,
// with a task event) where it became ready, and returns those task ids (the
// release runner notifies them).
func (s *Store) MarkReady(ctx context.Context) ([]int64, error) {
	var ids []int64
	err := s.e.Write(ctx, func(tx store.Tx) error {
		if err := tx.Trees().ClearReady(ctx); err != nil {
			return err
		}
		var err error
		if ids, err = tx.Trees().SetReady(ctx); err != nil {
			return err
		}
		slices.Sort(ids)
		from := StateBacklog
		for _, id := range ids {
			eventCtx, err := releaseAttribution(ctx, tx, id)
			if err != nil {
				return err
			}
			if err := addTaskEvent(eventCtx, tx, id, &from, StateBacklog, "ready: needs completed"); err != nil {
				return err
			}
		}
		return nil
	})
	return ids, err
}

func releaseAttribution(ctx context.Context, tx store.Tx, id int64) (context.Context, error) {
	cause := Cause{Kind: "dependency_release"}
	a, err := tx.Acceptances().ReleaseCause(ctx, id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return ctx, err
	}
	cause.TaskID, cause.AcceptanceID = a.TaskID, a.ID
	return AutomatedWork(ctx, "system", cause, a.Attribution), nil
}

func treeExists(ctx context.Context, tx store.Tx, id int64) error {
	ok, err := tx.Trees().Exists(ctx, id)
	if err != nil {
		return fmt.Errorf("tree: %w", err)
	}
	if !ok {
		return fmt.Errorf("tree: %w", ErrNotFound)
	}
	return nil
}

// placeNeeds sets id's needs within tree (nil: id is outside a tree; it
// joins the needs' tree, resolveTree).
func placeNeeds(ctx context.Context, tx store.Tx, id int64, tree *int64, needs []int64) error {
	if len(needs) > 0 {
		resolved, err := resolveTree(ctx, tx, tree, needs)
		if err != nil {
			return err
		}
		if err := joinTree(ctx, tx, id, resolved); err != nil {
			return err
		}
	}
	return setNeeds(ctx, tx, id, needs)
}

// resolveTree picks the tree for a task with needs (docs/api.md "Tree
// rules"): tree when set (404 when missing), else the needs' tree, else a
// new auto tree titled after the first need. Needs outside any tree are
// pulled into it; needs in another tree are a Conflict. Missing needs are
// 404.
func resolveTree(ctx context.Context, tx store.Tx, tree *int64, needs []int64) (int64, error) {
	if tree != nil {
		if err := treeExists(ctx, tx, *tree); err != nil {
			return 0, err
		}
	}
	type need struct {
		tree  *int64
		title string
	}
	found := map[int64]need{}
	infos, err := tx.Trees().NeedsInfo(ctx, needs)
	if err != nil {
		return 0, err
	}
	for _, n := range infos {
		found[n.ID] = need{tree: n.TreeID, title: n.Title}
	}
	var trees []int64
	for _, id := range needs {
		n, ok := found[id]
		if !ok {
			return 0, fmt.Errorf("need %d: %w", id, ErrNotFound)
		}
		if n.tree != nil && !slices.Contains(trees, *n.tree) {
			trees = append(trees, *n.tree)
		}
	}
	var target int64
	switch {
	case tree != nil:
		for _, t := range trees {
			if t != *tree {
				return 0, Conflict("a need is in another tree (%d): a task's needs share its tree", t)
			}
		}
		target = *tree
	case len(trees) > 1:
		return 0, Conflict("needs are in different trees (%d and %d): a task's needs share one tree", trees[0], trees[1])
	case len(trees) == 1:
		target = trees[0]
	default:
		if target, err = insertTree(ctx, tx, found[needs[0]].title, StartAuto); err != nil {
			return 0, err
		}
	}
	for _, id := range needs {
		if found[id].tree == nil {
			if err := joinTree(ctx, tx, id, target); err != nil {
				return 0, err
			}
			n := found[id]
			n.tree = &target
			found[id] = n
		}
	}
	return target, nil
}

// joinTree puts task id in tree (no-op when already there); a member of
// another tree is spliced out of it first.
func joinTree(ctx context.Context, tx store.Tx, id, tree int64) error {
	cur, err := tx.Tasks().TreeID(ctx, id)
	if err != nil {
		return fmt.Errorf("task %d: %w", id, err)
	}
	if cur != nil && *cur == tree {
		return nil
	}
	if err := treeOpen(ctx, tx, tree); err != nil {
		return err
	}
	archived, err := tx.Trees().IsArchived(ctx, id)
	if err != nil {
		return err
	}
	if archived {
		return Conflict("task %d is archived: unarchive it first", id)
	}
	if cur != nil {
		return spliceOutTo(ctx, tx, id, &tree)
	}
	return setMembership(ctx, tx, id, &tree)
}

// spliceOut takes task id out of its tree: each dependent drops id and takes
// over id's needs (A <- X <- B becomes A <- B, deduplicated), id loses its
// needs and leaves the tree, and an emptied tree is archived.
func spliceOut(ctx context.Context, tx store.Tx, id int64) error {
	return spliceOutTo(ctx, tx, id, nil)
}

// spliceOutTo changes membership once, so a move records both tree IDs.
func spliceOutTo(ctx context.Context, tx store.Tx, id int64, destination *int64) error {
	tr := tx.Trees()
	tree, err := tx.Tasks().TreeID(ctx, id)
	if err != nil {
		return fmt.Errorf("task %d: %w", id, err)
	}
	if tree != nil {
		if err := treeOpen(ctx, tx, *tree); err != nil {
			return err
		}
	}
	if err := inheritNeeds(ctx, tx, id); err != nil {
		return err
	}
	if err := tr.TouchDependents(ctx, id); err != nil {
		return err
	}
	if err := dropNeeds(ctx, tx, id); err != nil {
		return err
	}
	if tree == nil {
		return nil
	}
	return setMembership(ctx, tx, id, destination)
}

// setNeeds replaces id's needs (docs/api.md "Tree rules"; 409 unless noted):
// needs change only in backlog (an unchanged set is a no-op in any state);
// not with a start time (400); each need exists (404), is not the task, is
// in the task's tree, and does not (transitively) need the task. A completed
// need is allowed.
func setNeeds(ctx context.Context, tx store.Tx, id int64, needs []int64) error {
	tr := tx.Trees()
	want := slices.Clone(needs)
	slices.Sort(want)
	want = slices.Compact(want)
	ns, err := tr.NeedsState(ctx, id)
	if err != nil {
		return fmt.Errorf("task %d: %w", id, err)
	}
	state, tree, startAt := ns.State, ns.TreeID, ns.StartAt
	cur, err := tr.Needs(ctx, id)
	if err != nil {
		return err
	}
	if slices.Equal(cur, want) {
		return nil
	}
	if tree != nil {
		if err := treeOpen(ctx, tx, *tree); err != nil {
			return err
		}
	}
	if state != StateBacklog {
		return Conflict("task %d is %s: needs change only in backlog", id, state)
	}
	if len(want) > 0 && startAt != nil {
		return errStartAndNeeds
	}
	for _, need := range want {
		if need == id {
			return Conflict("a task cannot need itself")
		}
		needTree, err := tx.Tasks().TreeID(ctx, need)
		if err != nil {
			return fmt.Errorf("need %d: %w", need, err)
		}
		if tree == nil || needTree == nil || *needTree != *tree {
			return Conflict("task %d is not in this task's tree", need)
		}
		cycle, err := tr.WouldCycle(ctx, id, need)
		if err != nil {
			return err
		}
		if cycle {
			return Conflict("task %d already needs this task (needs cannot loop)", need)
		}
	}
	if err := keepNeeds(ctx, tx, id, want); err != nil {
		return err
	}
	for _, need := range want {
		if slices.Contains(cur, need) {
			continue
		}
		if err := addNeed(ctx, tx, id, need); err != nil {
			return err
		}
	}
	_, err = tx.Tasks().Touch(ctx, id)
	return err
}

// lockTreeWork takes the locks of a tree mutation before any read it checks
// (docs/storage.md "Transactions and locking"): the trees named and the
// trees the tasks are in, then the tasks, each in ascending id. Every
// mutation of a tree holds that tree's lock, so membership, needs and
// splices within it run one at a time; the task locks serialize changes to
// tasks outside any tree. A task that moved to another tree between the
// read and its lock gets that tree locked too (out of order: a rare
// deadlock is retried by the engine). Engines without row locks: no-op.
func lockTreeWork(ctx context.Context, tx store.Tx, trees, tasks []int64) error {
	treesOf := func() ([]int64, error) {
		var out []int64
		for _, id := range tasks {
			tree, err := tx.Tasks().TreeID(ctx, id)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if tree != nil {
				out = append(out, *tree)
			}
		}
		return out, nil
	}
	before, err := treesOf()
	if err != nil {
		return err
	}
	locked := append(slices.Clone(trees), before...)
	if err := tx.Trees().Lock(ctx, locked...); err != nil {
		return err
	}
	if err := tx.Tasks().Lock(ctx, tasks...); err != nil {
		return err
	}
	after, err := treesOf()
	if err != nil {
		return err
	}
	var moved []int64
	for _, id := range after {
		if !slices.Contains(locked, id) {
			moved = append(moved, id)
		}
	}
	return tx.Trees().Lock(ctx, moved...)
}
