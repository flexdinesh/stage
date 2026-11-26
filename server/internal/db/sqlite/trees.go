package sqlite

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

type trees struct{ *runner }

func (t *tx) Trees() store.Trees { return trees{t.r} }

// treeActivitySQL is TreeTimes.LastActivityAt for tree tr in SQL.
const treeActivitySQL = `max(tr.created_at, tr.updated_at, COALESCE((SELECT max(j.created_at) FROM journal_entries j WHERE j.tree_id = tr.id OR (j.kind = 'note' AND j.task_id IN (SELECT id FROM tasks WHERE tree_id = tr.id))), ''), COALESCE((SELECT max(max(m.updated_at, COALESCE(strftime('%Y-%m-%dT%H:%M:%fZ', json_extract(o.observation,'$.updatedAt')), '')))
 FROM tasks m LEFT JOIN execution_observations o ON o.session_id = m.session_id WHERE m.tree_id = tr.id), ''))`

const treeColumns = `tr.id, tr.title, tr.start_mode, tr.status, tr.closed_at, tr.created_at, tr.created_by_user_id, tr.source_machine_id, tr.updated_at,
  (tr.recorded_work = 0 AND NOT EXISTS (SELECT 1 FROM tasks t WHERE t.tree_id = tr.id AND NOT ` + unstartedSQL + `)), ` + treeActivitySQL

func scanTree(row rowScanner) (store.Tree, error) {
	var t store.Tree
	err := row.Scan(&t.ID, &t.Title, &t.StartMode, &t.Status, &t.ClosedAt, &t.CreatedAt, &t.CreatedBy, &t.SourceMachineID, &t.UpdatedAt, &t.Deletable, &t.JournalActivityAt)
	return t, notFound(err)
}

func (r trees) Insert(ctx context.Context, title string, mode store.StartMode, createdBy int64, sourceMachine *int64) (int64, error) {
	var id int64
	err := r.QueryRow(ctx, `INSERT INTO task_trees (title, start_mode, created_by_user_id, source_machine_id) VALUES ($1, $2, $3, $4) RETURNING id`, title, mode, createdBy, sourceMachine).Scan(&id)
	return id, err
}

func (r trees) Get(ctx context.Context, id int64) (store.Tree, error) {
	return scanTree(r.QueryRow(ctx, `SELECT `+treeColumns+` FROM task_trees tr WHERE tr.id = $1`, id))
}

func (r trees) List(ctx context.Context, f store.TreeFilter) ([]store.Tree, error) {
	var w where
	if f.ProjectID != nil {
		w.add("EXISTS (SELECT 1 FROM tasks m WHERE m.tree_id = tr.id AND m.project_id = " + w.arg(*f.ProjectID) + ")")
	}
	unaccepted := "EXISTS (SELECT 1 FROM tasks m WHERE m.tree_id = tr.id AND m.acceptance_id IS NULL)"
	switch f.View {
	case store.TreeViewAll:
	case store.TreeViewDone:
		w.add("(tr.status = 'done' OR (tr.status = 'open' AND NOT " + unaccepted + "))")
	case store.TreeViewArchived:
		w.add("tr.status = 'archived'")
	default:
		active := unaccepted
		if f.CompletedFrom != nil {
			active = "(" + active + " OR (SELECT max((SELECT accepted_at FROM task_acceptances a WHERE a.id=m.acceptance_id)) FROM tasks m WHERE m.tree_id = tr.id) >= " + w.arg(*f.CompletedFrom) + ")"
		}
		w.add("tr.status = 'open' AND " + active)
	}
	rows, err := r.Query(ctx, `SELECT `+treeColumns+` FROM task_trees tr`+w.sql()+`
ORDER BY `+treeActivitySQL+` DESC, tr.id DESC`, w.args...)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, scanTree)
}

func (r trees) Exists(ctx context.Context, id int64) (bool, error) {
	var one int
	err := r.QueryRow(ctx, `SELECT 1 FROM task_trees WHERE id = $1`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (r trees) Status(ctx context.Context, id int64) (store.TreeStatus, error) {
	var status store.TreeStatus
	err := r.QueryRow(ctx, `SELECT status FROM task_trees WHERE id = $1`, id).Scan(&status)
	return status, notFound(err)
}

func (r trees) Members(ctx context.Context, ids []int64) ([]store.Task, error) {
	return collectTasks(r.Query(ctx, taskSelect+` WHERE t.tree_id IN (SELECT value FROM json_each($1)) ORDER BY t.id`, jsonArray(ids)))
}

func (r trees) Edges(ctx context.Context, ids []int64) ([]store.TreeEdge, error) {
	rows, err := r.Query(ctx, `
SELECT n.task_id, n.need_id FROM task_needs n JOIN tasks t ON t.id = n.task_id
WHERE t.tree_id IN (SELECT value FROM json_each($1)) ORDER BY n.task_id, n.need_id`, jsonArray(ids))
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(r rowScanner) (store.TreeEdge, error) {
		var e store.TreeEdge
		err := r.Scan(&e.TaskID, &e.NeedID)
		return e, err
	})
}

func (r trees) Events(ctx context.Context, id int64, limit int) ([]store.TaskEvent, error) {
	rows, err := r.Query(ctx, `
SELECT e.id, e.task_id, e.from_state, e.to_state, e.detail, e.at FROM task_events e JOIN tasks t ON t.id = e.task_id
WHERE t.tree_id = $1 ORDER BY e.at DESC, e.id DESC LIMIT $2`, id, limit)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(r rowScanner) (store.TaskEvent, error) {
		var e store.TaskEvent
		err := r.Scan(&e.ID, &e.TaskID, &e.FromState, &e.ToState, &e.Detail, &e.At)
		return e, err
	})
}

func (r trees) Update(ctx context.Context, id int64, title store.Optional[string], mode store.Optional[store.StartMode]) error {
	var w where
	w.arg(id)
	sets := []string{"updated_at = " + nowSQL}
	if title.Set {
		sets = append(sets, "title = "+w.arg(title.V))
	}
	if mode.Set {
		sets = append(sets, "start_mode = "+w.arg(mode.V))
	}
	_, err := r.Exec(ctx, `UPDATE task_trees SET `+strings.Join(sets, ", ")+` WHERE id = $1`, w.args...)
	return err
}

func (r trees) SetStatus(ctx context.Context, id int64, status store.TreeStatus) error {
	_, err := r.Exec(ctx, `UPDATE task_trees SET status = $2,
  closed_at = CASE WHEN $2 = 'open' THEN NULL WHEN status = $2 THEN closed_at ELSE `+nowSQL+` END,
  updated_at = `+nowSQL+` WHERE id = $1`, id, status)
	return err
}

func (r trees) Config(ctx context.Context, id int64) (string, store.StartMode, error) {
	var title string
	var mode store.StartMode
	err := r.QueryRow(ctx, `SELECT title, start_mode FROM task_trees WHERE id = $1`, id).Scan(&title, &mode)
	return title, mode, notFound(err)
}

func (r trees) MarkRecordedWork(ctx context.Context, id int64, touched *time.Time) error {
	_, err := r.Exec(ctx, `UPDATE task_trees SET recorded_work = 1, updated_at = COALESCE($2, updated_at) WHERE id = $1`, id, touched)
	return err
}

func (r trees) BumpMembership(ctx context.Context, trees []int64, work bool) error {
	_, err := r.Exec(ctx, `UPDATE task_trees SET membership_revision = membership_revision + 1, updated_at = `+nowSQL+`,
  recorded_work = max(recorded_work, $2) WHERE id IN (SELECT value FROM json_each($1))`, jsonArray(trees), work)
	return err
}

func (r trees) ArchiveIfEmpty(ctx context.Context, id int64) (bool, error) {
	res, err := r.Exec(ctx, `UPDATE task_trees SET status = 'archived', closed_at = `+nowSQL+`
WHERE id = $1 AND NOT EXISTS (SELECT 1 FROM tasks WHERE tree_id = $1)`, id)
	return err == nil && res.RowsAffected() > 0, err
}

// edges scans need edges.
func edges(rows *rowSet, err error) ([]store.TreeEdge, error) {
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(r rowScanner) (store.TreeEdge, error) {
		var e store.TreeEdge
		err := r.Scan(&e.TaskID, &e.NeedID)
		return e, err
	})
}

// deleteNeeds removes the need edges matching cond (over task_needs, args
// from $1) and returns them, by task then need.
func (r trees) deleteNeeds(ctx context.Context, cond string, args ...any) ([]store.TreeEdge, error) {
	gone, err := edges(r.Query(ctx, `SELECT task_id, need_id FROM task_needs WHERE `+cond+` ORDER BY task_id, need_id`, args...))
	if err != nil || len(gone) == 0 {
		return gone, err
	}
	_, err = r.Exec(ctx, `DELETE FROM task_needs WHERE `+cond, args...)
	return gone, err
}

// activeTask is the first task matching cond (over tasks t, arg $1) that is
// launching, running or needing input.
func (r trees) activeTask(ctx context.Context, cond string, arg any) (int64, store.TaskState, error) {
	var id int64
	var state store.TaskState
	err := r.QueryRow(ctx, `SELECT t.id, t.state FROM tasks t WHERE `+cond+`
  AND t.state IN ('launching', 'running', 'needs_input') ORDER BY t.id LIMIT 1`, arg).Scan(&id, &state)
	return id, state, notFound(err)
}

func (r trees) ActiveMember(ctx context.Context, id int64) (int64, store.TaskState, error) {
	return r.activeTask(ctx, `t.tree_id = $1`, id)
}

func (r trees) ActiveTask(ctx context.Context, taskID int64) (int64, store.TaskState, error) {
	return r.activeTask(ctx, `t.id = $1`, taskID)
}

func (r trees) RecordedWork(ctx context.Context, id int64) (bool, error) {
	var protected bool
	err := r.QueryRow(ctx, `SELECT recorded_work FROM task_trees WHERE id = $1`, id).Scan(&protected)
	return protected, err
}

func (r trees) FirstStarted(ctx context.Context, id int64) (*int64, error) {
	var started *int64
	err := r.QueryRow(ctx, `SELECT min(t.id) FROM tasks t WHERE t.tree_id = $1 AND NOT `+unstartedSQL, id).Scan(&started)
	return started, err
}

func (r trees) DeleteMembers(ctx context.Context, id int64) error {
	_, err := r.Exec(ctx, `DELETE FROM tasks WHERE tree_id = $1`, id)
	return err
}

func (r trees) Delete(ctx context.Context, id int64) error {
	_, err := r.Exec(ctx, `DELETE FROM task_trees WHERE id = $1`, id)
	return err
}

func (r trees) MemberIDs(ctx context.Context, id int64) ([]int64, error) {
	rows, err := r.Query(ctx, `SELECT id FROM tasks WHERE tree_id = $1 ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, rowTo[int64])
}

func (r trees) DropMemberNeeds(ctx context.Context, id int64) ([]store.TreeEdge, error) {
	return r.deleteNeeds(ctx, `task_id IN (SELECT id FROM tasks WHERE tree_id = $1)`, id)
}

func (r trees) ArchiveDissolved(ctx context.Context, id int64) error {
	_, err := r.Exec(ctx, `UPDATE task_trees SET status = 'archived', closed_at = COALESCE(closed_at, `+nowSQL+`), updated_at = `+nowSQL+` WHERE id = $1`, id)
	return err
}

func (r trees) Startable(ctx context.Context, id int64) ([]int64, error) {
	rows, err := r.Query(ctx, `SELECT t.id FROM tasks t JOIN task_trees tr ON tr.id = t.tree_id
WHERE t.tree_id = $1 AND (`+readinessSQL+`) = 'ready' ORDER BY t.id`, id)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, rowTo[int64])
}

func (r trees) Launchable(ctx context.Context, connection int64) ([]int64, error) {
	rows, err := r.Query(ctx, `SELECT t.id FROM tasks t JOIN task_trees tr ON tr.id = t.tree_id
WHERE `+releasedSQL+` AND `+effectiveModeSQL+` = 'auto' AND ($1=0 OR t.connection_id=$1) ORDER BY t.id`, connection)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, rowTo[int64])
}

// readyEligible: released manual members.
const readyEligible = `SELECT t.id FROM tasks t JOIN task_trees tr ON tr.id = t.tree_id
WHERE ` + releasedSQL + ` AND ` + effectiveModeSQL + ` = 'manual'`

func (r trees) ClearReady(ctx context.Context) error {
	_, err := r.Exec(ctx, `UPDATE tasks SET ready_at = NULL WHERE ready_at IS NOT NULL AND id NOT IN (`+readyEligible+`)`)
	return err
}

func (r trees) SetReady(ctx context.Context) ([]int64, error) {
	rows, err := r.Query(ctx, `UPDATE tasks SET ready_at = `+nowSQL+` WHERE ready_at IS NULL AND id IN (`+readyEligible+`) RETURNING id`)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, rowTo[int64])
}

func (r trees) NeedsInfo(ctx context.Context, ids []int64) ([]store.NeedInfo, error) {
	rows, err := r.Query(ctx, `SELECT id, tree_id, title FROM tasks WHERE id IN (SELECT value FROM json_each($1))`, jsonArray(ids))
	if err != nil {
		return nil, err
	}
	return collectRows(rows, func(r rowScanner) (store.NeedInfo, error) {
		var n store.NeedInfo
		err := r.Scan(&n.ID, &n.TreeID, &n.Title)
		return n, err
	})
}

func (r trees) IsArchived(ctx context.Context, taskID int64) (bool, error) {
	var archived bool
	err := r.QueryRow(ctx, `SELECT archived_at IS NOT NULL FROM tasks WHERE id = $1`, taskID).Scan(&archived)
	return archived, err
}

func (r trees) SetMembership(ctx context.Context, taskID int64, tree *int64) error {
	_, err := r.Exec(ctx, `UPDATE tasks SET tree_id = $2, updated_at = `+nowSQL+` WHERE id = $1`, taskID, tree)
	return err
}

func (r trees) InheritNeeds(ctx context.Context, taskID int64) ([]store.TreeEdge, error) {
	added, err := edges(r.Query(ctx, `
INSERT OR IGNORE INTO task_needs (task_id, need_id)
SELECT d.task_id, n.need_id FROM task_needs d JOIN task_needs n ON n.task_id = d.need_id
WHERE d.need_id = $1 AND n.need_id <> d.task_id
RETURNING task_id, need_id`, taskID))
	slices.SortFunc(added, func(a, b store.TreeEdge) int {
		return cmp.Or(cmp.Compare(a.TaskID, b.TaskID), cmp.Compare(a.NeedID, b.NeedID))
	})
	return added, err
}

func (r trees) TouchDependents(ctx context.Context, taskID int64) error {
	_, err := r.Exec(ctx, `UPDATE tasks SET updated_at = `+nowSQL+` WHERE id IN (SELECT task_id FROM task_needs WHERE need_id = $1)`, taskID)
	return err
}

func (r trees) DropNeeds(ctx context.Context, taskID int64) ([]store.TreeEdge, error) {
	return r.deleteNeeds(ctx, `task_id = $1 OR need_id = $1`, taskID)
}

func (r trees) NeedsState(ctx context.Context, taskID int64) (store.NeedsState, error) {
	var n store.NeedsState
	err := r.QueryRow(ctx, `SELECT state, tree_id, start_at FROM tasks WHERE id = $1`, taskID).Scan(&n.State, &n.TreeID, &n.StartAt)
	return n, notFound(err)
}

func (r trees) Needs(ctx context.Context, taskID int64) ([]int64, error) {
	rows, err := r.Query(ctx, `SELECT need_id FROM task_needs WHERE task_id = $1 ORDER BY need_id`, taskID)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, rowTo[int64])
}

func (r trees) WouldCycle(ctx context.Context, taskID, need int64) (bool, error) {
	var cycle bool
	err := r.QueryRow(ctx, `
WITH RECURSIVE up(x) AS (
  SELECT need_id FROM task_needs WHERE task_id = $1
  UNION SELECT n.need_id FROM task_needs n JOIN up ON n.task_id = up.x
)
SELECT EXISTS (SELECT 1 FROM up WHERE x = $2)`, need, taskID).Scan(&cycle)
	return cycle, err
}

func (r trees) KeepNeeds(ctx context.Context, taskID int64, keep []int64) ([]store.TreeEdge, error) {
	return r.deleteNeeds(ctx, `task_id = $1 AND need_id NOT IN (SELECT value FROM json_each($2))`, taskID, jsonArray(keep))
}

func (r trees) AddNeed(ctx context.Context, taskID, need int64) error {
	_, err := r.Exec(ctx, `INSERT INTO task_needs (task_id, need_id) VALUES ($1, $2)`, taskID, need)
	return err
}

// Lock is a no-op: BEGIN IMMEDIATE already serializes writes.
func (trees) Lock(context.Context, ...int64) error { return nil }
