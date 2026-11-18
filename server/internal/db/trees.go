package db

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Tree mutations run inside Store.inTx (BEGIN IMMEDIATE), which serializes
// them with every other write, so need checks and updates are atomic.

// StartMode is when a tree member launches once its needs settle: auto
// (the release runner launches it) or manual (it reads ready).
type StartMode string

const (
	StartAuto   StartMode = "auto"
	StartManual StartMode = "manual"
)

// StartModes are the valid start modes.
var StartModes = []string{string(StartAuto), string(StartManual)}

func (m StartMode) Valid() bool { return slices.Contains(StartModes, string(m)) }

// TreeStatus is a tree's lifecycle (docs/api.md "Done, archive and delete"):
// open, done (marked done by hand) or archived. A tree that is not open
// releases nothing, rejects structural changes and its members leave the
// Dashboard; activity on a member reopens it.
type TreeStatus string

const (
	TreeOpen     TreeStatus = "open"
	TreeDone     TreeStatus = "done"
	TreeArchived TreeStatus = "archived"
)

// TreeStatuses are the valid tree statuses.
var TreeStatuses = []string{string(TreeOpen), string(TreeDone), string(TreeArchived)}

func (s TreeStatus) Valid() bool { return slices.Contains(TreeStatuses, string(s)) }

// Readiness of a backlog tree member: blocked (a need is not accepted) or
// ready (every need accepted, no start time, and manual or no needs).
type Readiness string

const (
	ReadinessBlocked Readiness = "blocked"
	ReadinessReady   Readiness = "ready"
)

// SQL over taskSelect's aliases (t = task, tr = its tree).
const (
	effectiveModeSQL  = `COALESCE(t.start_mode, tr.start_mode)`
	hasNeedsSQL       = `EXISTS (SELECT 1 FROM task_needs n WHERE n.task_id = t.id)`
	unacceptedNeedSQL = `EXISTS (SELECT 1 FROM task_needs n JOIN tasks d ON d.id = n.need_id WHERE n.task_id = t.id AND d.acceptance_id IS NULL)`
	readinessSQL      = `CASE WHEN t.tree_id IS NULL OR t.state <> 'backlog' OR t.adopted=1 OR t.acceptance_id IS NOT NULL OR tr.status <> 'open' THEN NULL
 WHEN ` + unacceptedNeedSQL + ` THEN 'blocked'
 WHEN t.start_at IS NULL AND (` + effectiveModeSQL + ` = 'manual' OR NOT ` + hasNeedsSQL + `) THEN 'ready'
 END`
	// releasedSQL: a backlog task with needs, all accepted, and no start time
	// (auto launches it; manual reads ready and notifies).
	releasedSQL = `t.state = 'backlog' AND t.acceptance_id IS NULL AND t.start_at IS NULL AND t.adopted = 0 AND tr.status = 'open' AND ` + hasNeedsSQL + ` AND NOT ` + unacceptedNeedSQL
	// unstartedSQL: task t has no notes or execution history (deletable).
	unstartedSQL = `(t.launch_attempt = 0 AND t.t3_thread_id IS NULL AND t.adopted = 0 AND t.recorded_work = 0)`
)

// Tree is a DAG of tasks (docs/api.md "Trees").
type Tree struct {
	ID        int64      `json:"id"`
	Title     string     `json:"title"`
	StartMode StartMode  `json:"startMode"`
	Status    TreeStatus `json:"status"`
	// ClosedAt: when it was marked done or archived (nil while open).
	ClosedAt  *time.Time `json:"closedAt"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	// Deletable: no recorded work, including from members that left.
	Deletable         bool      `json:"deletable"`
	JournalActivityAt time.Time `json:"-"`
}

// TreeEdge: TaskID needs NeedID.
type TreeEdge struct {
	TaskID int64 `json:"taskId"`
	NeedID int64 `json:"needId"`
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
// LastActivityAt includes tree, member, thread and journal activity.
// ListTrees orders by it.
type TreeTimes struct {
	StartedAt      *time.Time `json:"startedAt"`
	LastActivityAt time.Time  `json:"lastActivityAt"`
}

// treeEventLimit caps TreeDetail.Events.
const treeEventLimit = 50

// treeActivitySQL is TreeTimes.LastActivityAt for tree tr in SQL.
const treeActivitySQL = `max(tr.created_at, tr.updated_at, COALESCE((SELECT max(j.created_at) FROM journal_entries j WHERE j.tree_id = tr.id OR (j.kind = 'note' AND j.task_id IN (SELECT id FROM tasks WHERE tree_id = tr.id))), ''), COALESCE((SELECT max(max(m.updated_at, COALESCE(th.updated_at, '')))
 FROM tasks m LEFT JOIN threads th ON th.session_id = m.session_id WHERE m.tree_id = tr.id), ''))`

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
		later(m.ThreadUpdatedAt)
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
	Settled     int `json:"settled"`
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
	// SettledAt: every member settled; the latest settle (nil otherwise).
	SettledAt *time.Time `json:"settledAt"`
}

// TreeView picks trees by lifecycle: active (open with a member not
// settled), done (marked done, or open with every member settled),
// archived, or all.
type TreeView string

const (
	TreeViewActive   TreeView = "active"
	TreeViewDone     TreeView = "done"
	TreeViewArchived TreeView = "archived"
	TreeViewAll      TreeView = "all"
)

// TreeViews are the valid tree views.
var TreeViews = []string{string(TreeViewActive), string(TreeViewDone), string(TreeViewArchived), string(TreeViewAll)}

// TreeFilter selects trees: View ("" = active); with active, open trees
// completed at or after SettledFrom are listed too.
type TreeFilter struct {
	View        TreeView
	SettledFrom *time.Time
	ProjectID   *int64 // a member is in this project
}

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

const treeColumns = `tr.id, tr.title, tr.start_mode, tr.status, tr.closed_at, tr.created_at, tr.updated_at,
  (tr.recorded_work = 0 AND NOT EXISTS (SELECT 1 FROM tasks t WHERE t.tree_id = tr.id AND NOT ` + unstartedSQL + `)), ` + treeActivitySQL

func scanTree(row rowScanner) (Tree, error) {
	var t Tree
	err := row.Scan(&t.ID, &t.Title, &t.StartMode, &t.Status, &t.ClosedAt, &t.CreatedAt, &t.UpdatedAt, &t.Deletable, &t.JournalActivityAt)
	return t, notFound(err)
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
		mode = StartManual
	}
	var id int64
	err := s.inTx(ctx, func(tx *Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO task_trees (title, start_mode) VALUES ($1, $2) RETURNING id`, in.Title, mode).Scan(&id); err != nil {
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
	tree, err := scanTree(s.pool.QueryRow(ctx, `SELECT `+treeColumns+` FROM task_trees tr WHERE tr.id = $1`, id))
	if err != nil {
		return TreeDetail{}, fmt.Errorf("tree: %w", err)
	}
	rows, err := s.pool.Query(ctx, taskSelect+` WHERE t.tree_id = $1 ORDER BY t.id`, id)
	tasks, err := collectTasks(rows, err)
	if err != nil {
		return TreeDetail{}, err
	}
	edges, err := s.treeEdges(ctx, []int64{id})
	if err != nil {
		return TreeDetail{}, err
	}
	erows, err := s.pool.Query(ctx, `
SELECT e.id, e.task_id, e.from_state, e.to_state, e.detail, e.at FROM task_events e JOIN tasks t ON t.id = e.task_id
WHERE t.tree_id = $1 ORDER BY e.at DESC, e.id DESC LIMIT $2`, id, treeEventLimit)
	if err != nil {
		return TreeDetail{}, err
	}
	events, err := collectRows(erows, scanTaskEvent)
	if err != nil {
		return TreeDetail{}, err
	}
	return TreeDetail{Tree: tree, TreeTimes: treeTimes(tree, tasks), Tasks: tasks, Edges: edges,
		Levels: TreeLevels(idsOf(tasks), edges), Events: events}, nil
}

func idsOf(tasks []Task) []int64 {
	out := make([]int64, len(tasks))
	for i, t := range tasks {
		out[i] = t.ID
	}
	return out
}

// treeEdges lists the needs of every member of trees.
func (s *Store) treeEdges(ctx context.Context, trees []int64) ([]TreeEdge, error) {
	rows, err := s.pool.Query(ctx, `
SELECT n.task_id, n.need_id FROM task_needs n JOIN tasks t ON t.id = n.task_id
WHERE t.tree_id IN (SELECT value FROM json_each($1)) ORDER BY n.task_id, n.need_id`, jsonArray(trees))
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(r rowScanner) (TreeEdge, error) {
		var e TreeEdge
		err := r.Scan(&e.TaskID, &e.NeedID)
		return e, err
	})
}

// ListTrees returns trees matching f with their roll-up, most recent member
// activity first.
func (s *Store) ListTrees(ctx context.Context, f TreeFilter) ([]TreeSummary, error) {
	var w where
	if f.ProjectID != nil {
		w.add("EXISTS (SELECT 1 FROM tasks m WHERE m.tree_id = tr.id AND m.project_id = " + w.arg(*f.ProjectID) + ")")
	}
	unsettled := "EXISTS (SELECT 1 FROM tasks m WHERE m.tree_id = tr.id AND m.acceptance_id IS NULL)"
	switch f.View {
	case TreeViewAll:
	case TreeViewDone:
		w.add("(tr.status = 'done' OR (tr.status = 'open' AND NOT " + unsettled + "))")
	case TreeViewArchived:
		w.add("tr.status = 'archived'")
	default:
		active := unsettled
		if f.SettledFrom != nil {
			active = "(" + active + " OR (SELECT max((SELECT accepted_at FROM task_acceptances a WHERE a.id=m.acceptance_id)) FROM tasks m WHERE m.tree_id = tr.id) >= " + w.arg(*f.SettledFrom) + ")"
		}
		w.add("tr.status = 'open' AND " + active)
	}
	rows, err := s.pool.Query(ctx, `SELECT `+treeColumns+` FROM task_trees tr`+w.sql()+`
ORDER BY `+treeActivitySQL+` DESC, tr.id DESC`, w.args...)
	if err != nil {
		return nil, err
	}
	trees, err := collectRows(rows, scanTree)
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
	trows, err := s.pool.Query(ctx, taskSelect+` WHERE t.tree_id IN (SELECT value FROM json_each($1)) ORDER BY t.id`, jsonArray(ids))
	members, err := collectTasks(trows, err)
	if err != nil {
		return nil, err
	}
	edges, err := s.treeEdges(ctx, ids)
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
			c.Settled++
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
		case m.State == StateDone || m.State == StateSettled:
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
	if c.Total > 0 && c.Settled == c.Total {
		sum.SettledAt = latest
	}
	sum.Levels = len(TreeLevels(idsOf(members), edges))
	return sum
}

// UpdateTree applies a validated patch. Start mode changes only while the
// tree is open (Conflict).
func (s *Store) UpdateTree(ctx context.Context, id int64, p TreePatch) (Tree, error) {
	err := s.inTx(ctx, func(tx *Tx) error {
		if err := treeExists(ctx, tx, id); err != nil {
			return err
		}
		if p.StartMode.Set {
			if err := treeOpen(ctx, tx, id); err != nil {
				return err
			}
		}
		var w where
		w.arg(id)
		sets := []string{"updated_at = " + nowSQL}
		if p.Title.Set {
			sets = append(sets, "title = "+w.arg(p.Title.V))
		}
		if p.StartMode.Set {
			sets = append(sets, "start_mode = "+w.arg(p.StartMode.V))
		}
		_, err := tx.Exec(ctx, `UPDATE task_trees SET `+strings.Join(sets, ", ")+` WHERE id = $1`, w.args...)
		return err
	})
	if err != nil {
		return Tree{}, err
	}
	t, err := scanTree(s.pool.QueryRow(ctx, `SELECT `+treeColumns+` FROM task_trees tr WHERE tr.id = $1`, id))
	if err != nil {
		return Tree{}, fmt.Errorf("tree: %w", err)
	}
	return t, nil
}

// SetTreeStatus marks tree id done, archives it, or reopens it (open). Done
// and archived need no member launching, running or needing input
// (Conflict naming it); members keep their states.
func (s *Store) SetTreeStatus(ctx context.Context, id int64, status TreeStatus) (TreeDetail, error) {
	err := s.inTx(ctx, func(tx *Tx) error {
		if err := treeExists(ctx, tx, id); err != nil {
			return err
		}
		if status != TreeOpen {
			if err := noActiveTask(ctx, tx, `t.tree_id = $1`, id); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `UPDATE task_trees SET status = $2,
  closed_at = CASE WHEN $2 = 'open' THEN NULL WHEN status = $2 THEN closed_at ELSE `+nowSQL+` END,
  updated_at = `+nowSQL+` WHERE id = $1`, id, status)
		return err
	})
	if err != nil {
		return TreeDetail{}, err
	}
	return s.GetTree(ctx, id)
}

// noActiveTask: Conflict naming the first task matching cond (over tasks t,
// arg $1) that is launching, running or needing input.
func noActiveTask(ctx context.Context, tx *Tx, cond string, arg any) error {
	var id int64
	var state TaskState
	err := tx.QueryRow(ctx, `SELECT t.id, t.state FROM tasks t WHERE `+cond+`
  AND t.state IN ('launching', 'running', 'needs_input') ORDER BY t.id LIMIT 1`, arg).Scan(&id, &state)
	if errors.Is(notFound(err), ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return Conflict("#%d is %s: stop it first", id, strings.ReplaceAll(string(state), "_", " "))
}

// treeOpen: Conflict unless tree id is open (structural changes need an open
// tree). A missing tree is not checked here.
func treeOpen(ctx context.Context, q queryRower, id int64) error {
	var status TreeStatus
	if err := q.QueryRow(ctx, `SELECT status FROM task_trees WHERE id = $1`, id).Scan(&status); err != nil {
		if errors.Is(notFound(err), ErrNotFound) {
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
	return s.inTx(ctx, func(tx *Tx) error {
		if err := treeExists(ctx, tx, id); err != nil {
			return err
		}
		var protected bool
		if err := tx.QueryRow(ctx, `SELECT recorded_work FROM task_trees WHERE id = $1`, id).Scan(&protected); err != nil {
			return err
		}
		if protected {
			return Conflict("tree %d has recorded work: archive it instead", id)
		}
		var started *int64
		if err := tx.QueryRow(ctx, `SELECT min(t.id) FROM tasks t WHERE t.tree_id = $1 AND NOT `+unstartedSQL, id).Scan(&started); err != nil {
			return err
		}
		if started != nil {
			return Conflict("#%d has started: archive the tree instead", *started)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM tasks WHERE tree_id = $1`, id); err != nil {
			return err
		}
		if err := clearStaleBases(ctx, tx); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM task_trees WHERE id = $1`, id)
		return err
	})
}

// DissolveTree detaches members and archives the tree, preserving its journal.
func (s *Store) DissolveTree(ctx context.Context, id int64) error {
	return s.inTx(ctx, func(tx *Tx) error {
		if err := treeExists(ctx, tx, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM task_needs WHERE task_id IN (SELECT id FROM tasks WHERE tree_id = $1)`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE tasks SET tree_id = NULL, updated_at = `+nowSQL+` WHERE tree_id = $1`, id); err != nil {
			return err
		}
		if err := clearStaleBases(ctx, tx); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE task_trees SET status = 'archived', closed_at = COALESCE(closed_at, `+nowSQL+`), updated_at = `+nowSQL+` WHERE id = $1`, id)
		return err
	})
}

// AddToTree puts task taskID in tree treeID (a member of another tree moves:
// it is spliced out there first) and, when needs is non-nil, sets its needs.
func (s *Store) AddToTree(ctx context.Context, treeID, taskID int64, needs []int64) (TreeDetail, error) {
	err := s.inTx(ctx, func(tx *Tx) error {
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
	return s.inTx(ctx, func(tx *Tx) error {
		var tree *int64
		if err := tx.QueryRow(ctx, `SELECT tree_id FROM tasks WHERE id = $1`, taskID).Scan(&tree); err != nil {
			return fmt.Errorf("task: %w", notFound(err))
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
	err := s.inTx(ctx, func(tx *Tx) error {
		var tree *int64
		if err := tx.QueryRow(ctx, `SELECT tree_id FROM tasks WHERE id = $1`, id).Scan(&tree); err != nil {
			return notFound(err)
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
	if err := treeExists(ctx, s.pool, treeID); err != nil {
		return nil, err
	}
	if err := treeOpen(ctx, s.pool, treeID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT t.id FROM tasks t JOIN task_trees tr ON tr.id = t.tree_id
WHERE t.tree_id = $1 AND (`+readinessSQL+`) = 'ready' ORDER BY t.id`, treeID)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, rowTo[int64])
}

// LaunchableTasks: auto members whose needs all accepted (the release runner
// launches them), by id.
func (s *Store) LaunchableTasks(ctx context.Context) ([]int64, error) {
	return s.LaunchableForConnection(ctx, 0)
}
func (s *Store) LaunchableForConnection(ctx context.Context, connection int64) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT t.id FROM tasks t JOIN task_trees tr ON tr.id = t.tree_id
WHERE `+releasedSQL+` AND `+effectiveModeSQL+` = 'auto' AND ($1=0 OR t.connection_id=$1) ORDER BY t.id`, connection)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, rowTo[int64])
}

// MarkReady records readiness episodes of manual members with needs: clears
// ready_at where the task is no longer ready, sets it (once per episode,
// with a task event) where it became ready, and returns those task ids (the
// release runner notifies them).
func (s *Store) MarkReady(ctx context.Context) ([]int64, error) {
	eligible := `SELECT t.id FROM tasks t JOIN task_trees tr ON tr.id = t.tree_id
WHERE ` + releasedSQL + ` AND ` + effectiveModeSQL + ` = 'manual'`
	var ids []int64
	err := s.inTx(ctx, func(tx *Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE tasks SET ready_at = NULL WHERE ready_at IS NOT NULL AND id NOT IN (`+eligible+`)`); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `UPDATE tasks SET ready_at = `+nowSQL+` WHERE ready_at IS NULL AND id IN (`+eligible+`) RETURNING id`)
		if err != nil {
			return err
		}
		if ids, err = collectRows(rows, rowTo[int64]); err != nil {
			return err
		}
		slices.Sort(ids)
		for _, id := range ids {
			if _, err := tx.Exec(ctx,
				`INSERT INTO task_events (task_id, from_state, to_state, detail) VALUES ($1, 'backlog', 'backlog', 'ready: needs completed')`, id); err != nil {
				return err
			}
		}
		return nil
	})
	return ids, err
}

// UnsettledNeeds: task id's needs that are not accepted, by id.
func (s *Store) UnsettledNeeds(ctx context.Context, id int64) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT n.need_id FROM task_needs n JOIN tasks d ON d.id = n.need_id
WHERE n.task_id = $1 AND d.acceptance_id IS NULL ORDER BY n.need_id`, id)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, rowTo[int64])
}

func treeExists(ctx context.Context, q queryRower, id int64) error {
	var one int
	if err := q.QueryRow(ctx, `SELECT 1 FROM task_trees WHERE id = $1`, id).Scan(&one); err != nil {
		return fmt.Errorf("tree: %w", notFound(err))
	}
	return nil
}

// placeNeeds sets id's needs within tree (nil: id is outside a tree; it
// joins the needs' tree, resolveTree).
func placeNeeds(ctx context.Context, tx *Tx, id int64, tree *int64, needs []int64) error {
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
func resolveTree(ctx context.Context, tx *Tx, tree *int64, needs []int64) (int64, error) {
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
	rows, err := tx.Query(ctx, `SELECT id, tree_id, title FROM tasks WHERE id IN (SELECT value FROM json_each($1))`, jsonArray(needs))
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var id int64
		var n need
		if err := rows.Scan(&id, &n.tree, &n.title); err != nil {
			rows.Close()
			return 0, err
		}
		found[id] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
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
		if err := tx.QueryRow(ctx, `INSERT INTO task_trees (title,start_mode) VALUES ($1,'manual') RETURNING id`, found[needs[0]].title).Scan(&target); err != nil {
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
func joinTree(ctx context.Context, tx *Tx, id, tree int64) error {
	var cur *int64
	if err := tx.QueryRow(ctx, `SELECT tree_id FROM tasks WHERE id = $1`, id).Scan(&cur); err != nil {
		return fmt.Errorf("task %d: %w", id, notFound(err))
	}
	if cur != nil && *cur == tree {
		return nil
	}
	if err := treeOpen(ctx, tx, tree); err != nil {
		return err
	}
	var archived bool
	if err := tx.QueryRow(ctx, `SELECT archived_at IS NOT NULL FROM tasks WHERE id = $1`, id).Scan(&archived); err != nil {
		return err
	}
	if archived {
		return Conflict("task %d is archived: unarchive it first", id)
	}
	if cur != nil {
		return spliceOutTo(ctx, tx, id, &tree)
	}
	_, err := tx.Exec(ctx, `UPDATE tasks SET tree_id = $2, updated_at = `+nowSQL+` WHERE id = $1`, id, tree)
	return err
}

// spliceOut takes task id out of its tree: each dependent drops id and takes
// over id's needs (A <- X <- B becomes A <- B, deduplicated), id loses its
// needs and leaves the tree, stale workspace.baseTaskId values are cleared
// and an emptied tree is archived.
func spliceOut(ctx context.Context, tx *Tx, id int64) error {
	return spliceOutTo(ctx, tx, id, nil)
}

// spliceOutTo changes membership once, so a move records both tree IDs.
func spliceOutTo(ctx context.Context, tx *Tx, id int64, destination *int64) error {
	var tree *int64
	if err := tx.QueryRow(ctx, `SELECT tree_id FROM tasks WHERE id = $1`, id).Scan(&tree); err != nil {
		return fmt.Errorf("task %d: %w", id, notFound(err))
	}
	if tree != nil {
		if err := treeOpen(ctx, tx, *tree); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
INSERT OR IGNORE INTO task_needs (task_id, need_id)
SELECT d.task_id, n.need_id FROM task_needs d JOIN task_needs n ON n.task_id = d.need_id
WHERE d.need_id = $1 AND n.need_id <> d.task_id`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE tasks SET updated_at = `+nowSQL+` WHERE id IN (SELECT task_id FROM task_needs WHERE need_id = $1)`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM task_needs WHERE task_id = $1 OR need_id = $1`, id); err != nil {
		return err
	}
	if err := clearStaleBases(ctx, tx); err != nil {
		return err
	}
	if tree == nil {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE tasks SET tree_id = $2, updated_at = `+nowSQL+` WHERE id = $1`, id, destination)
	return err
}

// setNeeds replaces id's needs (docs/api.md "Tree rules"; 409 unless noted):
// needs change only in backlog (an unchanged set is a no-op in any state);
// not with a start time (400); each need exists (404), is not the task, is
// in the task's tree, and does not (transitively) need the task. A settled
// need is allowed. Clears a workspace.baseTaskId no longer among the needs.
func setNeeds(ctx context.Context, tx *Tx, id int64, needs []int64) error {
	want := slices.Clone(needs)
	slices.Sort(want)
	want = slices.Compact(want)
	var state TaskState
	var tree *int64
	var startAt *time.Time
	if err := tx.QueryRow(ctx, `SELECT state, tree_id, start_at FROM tasks WHERE id = $1`, id).Scan(&state, &tree, &startAt); err != nil {
		return fmt.Errorf("task %d: %w", id, notFound(err))
	}
	rows, err := tx.Query(ctx, `SELECT need_id FROM task_needs WHERE task_id = $1 ORDER BY need_id`, id)
	if err != nil {
		return err
	}
	cur, err := collectRows(rows, rowTo[int64])
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
		var needTree *int64
		if err := tx.QueryRow(ctx, `SELECT tree_id FROM tasks WHERE id = $1`, need).Scan(&needTree); err != nil {
			return fmt.Errorf("need %d: %w", need, notFound(err))
		}
		if tree == nil || needTree == nil || *needTree != *tree {
			return Conflict("task %d is not in this task's tree", need)
		}
		cycle, err := wouldCycle(ctx, tx, id, need)
		if err != nil {
			return err
		}
		if cycle {
			return Conflict("task %d already needs this task (needs cannot loop)", need)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM task_needs WHERE task_id = $1 AND need_id NOT IN (SELECT value FROM json_each($2))`, id, jsonArray(want)); err != nil {
		return err
	}
	for _, need := range want {
		if slices.Contains(cur, need) {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO task_needs (task_id, need_id) VALUES ($1, $2)`, id, need); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE tasks SET updated_at = `+nowSQL+` WHERE id = $1`, id); err != nil {
		return err
	}
	return clearStaleBases(ctx, tx)
}

// wouldCycle: need already (transitively) needs id, so id needing it loops.
func wouldCycle(ctx context.Context, tx *Tx, id, need int64) (bool, error) {
	var cycle bool
	err := tx.QueryRow(ctx, `
WITH RECURSIVE up(x) AS (
  SELECT need_id FROM task_needs WHERE task_id = $1
  UNION SELECT n.need_id FROM task_needs n JOIN up ON n.task_id = up.x
)
SELECT EXISTS (SELECT 1 FROM up WHERE x = $2)`, need, id).Scan(&cycle)
	return cycle, err
}

// clearStaleBases drops workspace.baseTaskId where it no longer names one of
// the task's needs (after a splice or a needs change).
func clearStaleBases(ctx context.Context, tx *Tx) error {
	_, err := tx.Exec(ctx, `
UPDATE tasks SET workspace = json_set(workspace, '$.baseTaskId', json('null'))
WHERE json_extract(workspace, '$.baseTaskId') IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM task_needs n WHERE n.task_id = tasks.id
    AND n.need_id = json_extract(tasks.workspace, '$.baseTaskId'))`)
	return err
}
