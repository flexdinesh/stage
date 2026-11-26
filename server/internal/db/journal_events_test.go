package db_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
)

func mustTree(t *testing.T, s *db.Store, ctx context.Context, title string, mode db.StartMode, members ...db.TreeMember) db.TreeDetail {
	t.Helper()
	tree, err := s.CreateTree(ctx, db.NewTree{Title: title, StartMode: mode, Tasks: members})
	if err != nil {
		t.Fatalf("create tree %q: %v", title, err)
	}
	return tree
}

func setState(t *testing.T, s *db.Store, ctx context.Context, id int64, to db.TaskState) {
	t.Helper()
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: id, To: to}); err != nil {
		t.Fatalf("task %d to %s: %v", id, to, err)
	}
}

// launch launches task id under a legacy thread id and moves it to end.
func launch(t *testing.T, s *db.Store, ctx context.Context, id int64, thread string, end db.TaskState) {
	t.Helper()
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: id, To: db.StateLaunching, BeginAttempt: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionTask(ctx, db.Transition{TaskID: id, To: db.StateRunning, Session: &db.SessionRef{ConnectionID: s.ActiveID(db.LocalMachine, "t3"), ExternalSessionID: thread}}); err != nil {
		t.Fatal(err)
	}
	setState(t, s, ctx, id, end)
}

func journal(t *testing.T, s *db.Store, ctx context.Context, owner db.JournalOwner, q db.JournalQuery) db.JournalPage {
	t.Helper()
	page, err := s.ReadJournal(ctx, owner, q)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

// journalRows dumps every journal entry after id since, oldest first, without
// id and created_at.
func journalRows(t *testing.T, ctx context.Context, since int64) []string {
	t.Helper()
	rows, err := testDB.Pool.QueryContext(ctx, `SELECT kind||' '||event_type||' task='||ifnull(task_id,'-')||' tree='||ifnull(tree_id,'-')||
 ' src='||ifnull(source_task_id,'-')||' sess='||ifnull(source_session_id,'-')||
 ' att='||ifnull(attempt,'-')||' from='||ifnull(from_tree_id,'-')||' to='||ifnull(to_tree_id,'-')||
 ' by='||created_by_user_id||' m='||ifnull(source_machine_id,'-')||' '||text
 FROM journal_entries WHERE id > $1 ORDER BY id`, since)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func lastJournalID(t *testing.T, ctx context.Context) int64 {
	t.Helper()
	var id int64
	if err := testDB.Pool.QueryRowContext(ctx, `SELECT ifnull(max(id),0) FROM journal_entries`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// treeRow is a tree's status, recorded work, membership revision and
// whether it is closed.
func treeRow(t *testing.T, ctx context.Context, id int64) string {
	t.Helper()
	var s string
	if err := testDB.Pool.QueryRowContext(ctx, `SELECT status||' work='||recorded_work||' rev='||membership_revision||' closed='||(closed_at IS NOT NULL)
 FROM task_trees WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func recordedWork(t *testing.T, ctx context.Context, id int64) bool {
	t.Helper()
	var w bool
	if err := testDB.Pool.QueryRowContext(ctx, `SELECT recorded_work FROM tasks WHERE id = $1`, id).Scan(&w); err != nil {
		t.Fatal(err)
	}
	return w
}

func wantRows(t *testing.T, step string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("%s journal rows:\ngot:\n%s\nwant:\n%s", step, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestJournalEventRowsExact pins every journal event a cause writes: kind,
// type, owner, provenance, attribution (default user, no machine), text and
// the tree and task side effects (docs/storage.md "Journal events").
func TestJournalEventRowsExact(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "p")
	since := lastJournalID(t, ctx)
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	b := mustTask(t, s, ctx, p.ID, "b", nil)
	c := mustTask(t, s, ctx, p.ID, "c", nil)
	wantRows(t, "create tasks", journalRows(t, ctx, since), []string{
		"event task_created task=1 tree=- src=- sess=- att=0 from=- to=- by=1 m=- created",
		"event task_created task=2 tree=- src=- sess=- att=0 from=- to=- by=1 m=- created",
		"event task_created task=3 tree=- src=- sess=- att=0 from=- to=- by=1 m=- created",
	})

	since = lastJournalID(t, ctx)
	tr := mustTree(t, s, ctx, "tree", "", db.TreeMember{TaskID: a.ID}, db.TreeMember{TaskID: b.ID, Needs: []int64{a.ID}}, db.TreeMember{TaskID: c.ID, Needs: []int64{b.ID}})
	wantRows(t, "create tree", journalRows(t, ctx, since), []string{
		"event tree_created task=- tree=1 src=- sess=- att=- from=- to=- by=1 m=- tree",
		"event task_moved task=- tree=1 src=1 sess=- att=- from=- to=1 by=1 m=- a",
		"event task_moved task=- tree=1 src=2 sess=- att=- from=- to=1 by=1 m=- b",
		"event task_moved task=- tree=1 src=3 sess=- att=- from=- to=1 by=1 m=- c",
		"event needs_changed task=- tree=1 src=2 sess=- att=- from=- to=- by=1 m=- Added need #1",
		"event needs_changed task=- tree=1 src=3 sess=- att=- from=- to=- by=1 m=- Added need #2",
	})
	if got := treeRow(t, ctx, tr.ID); got != "open work=0 rev=3 closed=0" {
		t.Fatalf("tree after create: %s", got)
	}

	since = lastJournalID(t, ctx)
	if _, err := s.UpdateTree(ctx, tr.ID, db.TreePatch{Title: db.Optional[string]{Set: true, V: "renamed"}, StartMode: db.Optional[db.StartMode]{Set: true, V: db.StartManual}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateTree(ctx, tr.ID, db.TreePatch{Title: db.Optional[string]{Set: true, V: "renamed"}}); err != nil {
		t.Fatal(err)
	}
	wantRows(t, "update tree", journalRows(t, ctx, since), []string{
		"event tree_config_changed task=- tree=1 src=- sess=- att=- from=- to=- by=1 m=- Title: tree -> renamed; start mode: auto -> manual",
	})

	// A note protects its task and tree and stamps the tree's updated_at.
	n := note(t, s, ctx, db.JournalOwner{TaskID: &c.ID}, "keep", "k1")
	var touched bool
	if err := testDB.Pool.QueryRowContext(ctx, `SELECT tr.updated_at = j.created_at FROM task_trees tr, journal_entries j WHERE tr.id = $1 AND j.id = $2`, tr.ID, n.ID).Scan(&touched); err != nil {
		t.Fatal(err)
	}
	if !touched || !recordedWork(t, ctx, c.ID) || recordedWork(t, ctx, b.ID) || treeRow(t, ctx, tr.ID) != "open work=1 rev=3 closed=0" {
		t.Fatalf("note protection: touched=%v tree=%s", touched, treeRow(t, ctx, tr.ID))
	}

	// Task events carry the task's session, not its legacy thread id; the
	// API's sourceThreadId still reads the T3 session's id.
	since = lastJournalID(t, ctx)
	launch(t, s, ctx, a.ID, "thread-a", db.StateDone)
	for _, e := range journal(t, s, ctx, db.JournalOwner{TaskID: &a.ID}, db.JournalQuery{Kind: "events", Limit: 2}).Entries {
		if e.SourceThreadID == nil || *e.SourceThreadID != "thread-a" {
			t.Fatalf("event sourceThreadId %v", e.SourceThreadID)
		}
	}
	wantRows(t, "launch", journalRows(t, ctx, since), []string{
		"event task_state_changed task=1 tree=1 src=- sess=- att=1 from=- to=- by=1 m=-  (backlog -> launching)",
		"event task_state_changed task=1 tree=1 src=- sess=1 att=1 from=- to=- by=1 m=-  (launching -> running)",
		"event task_state_changed task=1 tree=1 src=- sess=1 att=1 from=- to=- by=1 m=-  (running -> done)",
	})
	var unmatched int
	if err := testDB.Pool.QueryRowContext(ctx, `SELECT count(*) FROM journal_entries j WHERE j.event_type LIKE 'task_%' AND j.event_type <> 'task_moved'
 AND NOT EXISTS (SELECT 1 FROM task_events e WHERE e.task_id = j.task_id AND e.at = j.created_at)`).Scan(&unmatched); err != nil {
		t.Fatal(err)
	}
	if unmatched != 0 || !recordedWork(t, ctx, a.ID) {
		t.Fatalf("task events must carry their event time: %d unmatched", unmatched)
	}

	since = lastJournalID(t, ctx)
	for _, st := range []db.TreeStatus{db.TreeDone, db.TreeDone, db.TreeOpen} {
		if _, err := s.SetTreeStatus(ctx, tr.ID, st); err != nil {
			t.Fatal(err)
		}
	}
	wantRows(t, "tree status", journalRows(t, ctx, since), []string{
		"event tree_status_changed task=- tree=1 src=- sess=- att=- from=- to=- by=1 m=- open -> done",
		"event tree_status_changed task=- tree=1 src=- sess=- att=- from=- to=- by=1 m=- done -> open",
	})

	// Deleting b splices it out: c inherits a, b's needs go, b leaves.
	since = lastJournalID(t, ctx)
	if err := s.DeleteTask(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	wantRows(t, "delete member", journalRows(t, ctx, since), []string{
		"event needs_changed task=- tree=1 src=3 sess=- att=- from=- to=- by=1 m=- Added need #1",
		"event needs_changed task=- tree=1 src=2 sess=- att=- from=- to=- by=1 m=- Removed need #1",
		"event needs_changed task=- tree=1 src=3 sess=- att=- from=- to=- by=1 m=- Removed need #2",
		"event task_moved task=- tree=1 src=2 sess=- att=- from=1 to=- by=1 m=- b",
	})

	// Moving c to another tree records both sides.
	d := mustTask(t, s, ctx, p.ID, "d", nil)
	tr2 := mustTree(t, s, ctx, "other", "", db.TreeMember{TaskID: d.ID})
	since = lastJournalID(t, ctx)
	if _, err := s.AddToTree(ctx, tr2.ID, c.ID, []int64{d.ID}); err != nil {
		t.Fatal(err)
	}
	wantRows(t, "move", journalRows(t, ctx, since), []string{
		"event needs_changed task=- tree=1 src=3 sess=- att=- from=- to=- by=1 m=- Removed need #1",
		"event task_moved task=- tree=1 src=3 sess=- att=- from=1 to=2 by=1 m=- c",
		"event task_moved task=- tree=2 src=3 sess=- att=- from=1 to=2 by=1 m=- c",
		"event needs_changed task=- tree=2 src=3 sess=- att=- from=- to=- by=1 m=- Added need #4",
	})
	if got := treeRow(t, ctx, tr2.ID); got != "open work=1 rev=2 closed=0" {
		t.Fatalf("destination tree: %s", got)
	}

	// Removing the last member archives the emptied tree.
	since = lastJournalID(t, ctx)
	if err := s.RemoveFromTree(ctx, tr.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	wantRows(t, "remove last", journalRows(t, ctx, since), []string{
		"event task_moved task=- tree=1 src=1 sess=- att=- from=1 to=- by=1 m=- a",
		"event tree_status_changed task=- tree=1 src=- sess=- att=- from=- to=- by=1 m=- open -> archived",
	})
	if got := treeRow(t, ctx, tr.ID); got != "archived work=1 rev=6 closed=1" {
		t.Fatalf("emptied tree: %s", got)
	}

	since = lastJournalID(t, ctx)
	if err := s.DissolveTree(ctx, tr2.ID); err != nil {
		t.Fatal(err)
	}
	wantRows(t, "dissolve", journalRows(t, ctx, since), []string{
		"event needs_changed task=- tree=2 src=3 sess=- att=- from=- to=- by=1 m=- Removed need #4",
		"event task_moved task=- tree=2 src=3 sess=- att=- from=2 to=- by=1 m=- c",
		"event task_moved task=- tree=2 src=4 sess=- att=- from=2 to=- by=1 m=- d",
		"event tree_status_changed task=- tree=2 src=- sess=- att=- from=- to=- by=1 m=- open -> archived",
	})

	// Deleting an unstarted tree writes nothing for it (the task_needs
	// cascade journaled into the deleted tree before 0026).
	e := mustTask(t, s, ctx, p.ID, "e", nil)
	f := mustTask(t, s, ctx, p.ID, "f", nil)
	tr3 := mustTree(t, s, ctx, "gone", "", db.TreeMember{TaskID: e.ID}, db.TreeMember{TaskID: f.ID, Needs: []int64{e.ID}})
	since = lastJournalID(t, ctx)
	if err := s.DeleteTree(ctx, tr3.ID); err != nil {
		t.Fatal(err)
	}
	wantRows(t, "delete tree", journalRows(t, ctx, since), nil)

	// An event without a state change is a task_event with the bare detail.
	// A task created with needs gets a tree named after its first need.
	g := mustTask(t, s, ctx, p.ID, "g", nil)
	since = lastJournalID(t, ctx)
	h := mustTask(t, s, ctx, p.ID, "h", []int64{g.ID})
	wantRows(t, "create with needs", journalRows(t, ctx, since), []string{
		"event tree_created task=- tree=4 src=- sess=- att=- from=- to=- by=1 m=- g",
		"event task_moved task=- tree=4 src=7 sess=- att=- from=- to=4 by=1 m=- g",
		"event task_moved task=- tree=4 src=8 sess=- att=- from=- to=4 by=1 m=- h",
		"event needs_changed task=- tree=4 src=8 sess=- att=- from=- to=- by=1 m=- Added need #7",
		"event task_created task=8 tree=4 src=- sess=- att=0 from=- to=- by=1 m=- created",
	})
	if _, err := s.UpdateTree(ctx, *h.TreeID, db.TreePatch{StartMode: db.Optional[db.StartMode]{Set: true, V: db.StartManual}}); err != nil {
		t.Fatal(err)
	}
	setState(t, s, ctx, g.ID, db.StateSettled)
	acceptTask(t, s, ctx, g.ID)
	since = lastJournalID(t, ctx)
	if _, err := s.MarkReady(ctx); err != nil {
		t.Fatal(err)
	}
	wantRows(t, "ready", journalRows(t, ctx, since), []string{
		"event task_event task=8 tree=4 src=- sess=- att=0 from=- to=- by=1 m=- ready: needs completed",
	})
	if recordedWork(t, ctx, h.ID) {
		t.Fatal("a backlog event must not record work")
	}

	// Every cause wrote its events exactly once: no duplicate rows.
	var dups int
	if err := testDB.Pool.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT 1 FROM journal_entries WHERE kind='event'
 GROUP BY event_type, task_id, tree_id, source_task_id, text, created_at HAVING count(*) > 1)`).Scan(&dups); err != nil {
		t.Fatal(err)
	}
	if dups != 0 {
		t.Fatalf("%d duplicated events", dups)
	}
}

// AddTaskEvent records on the timeline and journal at the task's current
// state without changing it; an unknown task is not found.
func TestAddTaskEventKeepsState(t *testing.T) {
	s, ctx := setup(t)
	p := mustProject(t, s, ctx, "p")
	a := mustTask(t, s, ctx, p.ID, "a", nil)
	launch(t, s, ctx, a.ID, "thread-a", db.StateDone)
	if err := s.AddTaskEvent(ctx, a.ID, "Follow-up sent from Stage"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTask(ctx, a.ID)
	if err != nil || got.State != db.StateDone {
		t.Fatalf("state %+v %v", got, err)
	}
	evs, err := s.TaskEvents(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := evs[len(evs)-1]
	if last.Detail != "Follow-up sent from Stage" || last.ToState != db.StateDone || last.FromState == nil || *last.FromState != db.StateDone {
		t.Fatalf("event %+v", last)
	}
	entries := journal(t, s, ctx, db.JournalOwner{TaskID: &a.ID}, db.JournalQuery{Kind: "events", Limit: 1}).Entries
	if len(entries) != 1 || !strings.Contains(entries[0].Text, "Follow-up sent from Stage") {
		t.Fatalf("journal %+v", entries)
	}
	if err := s.AddTaskEvent(ctx, 99999, "x"); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("unknown task: %v", err)
	}
}
