package db

import (
	"context"
	"errors"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db/dbtest"
	"github.com/flexdinesh/stage/server/internal/db/sqlite"
	"github.com/flexdinesh/stage/server/internal/db/store"
)

// derivedState fingerprints every row a derived write or its cause touches.
func derivedState(t *testing.T, pool *sqlite.DB) string {
	t.Helper()
	var s string
	err := pool.QueryRow(`SELECT
 ifnull((SELECT group_concat(id||kind||event_type||ifnull(tree_id,'')||text, ';') FROM journal_entries), '')||'|'||
 (SELECT count(*) FROM task_events)||'|'||
 ifnull((SELECT group_concat(task_id||'-'||need_id) FROM task_needs), '')||'|'||
 ifnull((SELECT group_concat(id||':'||ifnull(tree_id,'')||':'||recorded_work||':'||updated_at) FROM tasks), '')||'|'||
 ifnull((SELECT group_concat(id||':'||title||start_mode||status||recorded_work||membership_revision||updated_at||ifnull(closed_at,'')) FROM task_trees), '')||'|'||
 (SELECT count(*) FROM task_acceptances)||'|'||(SELECT count(*) FROM task_handoffs)||'|'||(SELECT count(*) FROM session_identities)`).Scan(&s)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func journalCount(t *testing.T, pool *sqlite.DB) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(`SELECT count(*) FROM journal_entries`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// derivedFixture is tree T (a; b needs a; c needs b), an empty open tree E,
// a standalone task d and a launched standalone task l.
type derivedFixture struct {
	t3, project, tree, empty, a, b, c, d, l int64
}

func newDerivedFixture(t *testing.T, s *Store, ctx context.Context) derivedFixture {
	t.Helper()
	var f derivedFixture
	f.t3 = s.ActiveID(LocalMachine, "t3")
	f.project = projectWithBinding(t, s, f.t3, "/w/a")
	task := func(title string) int64 {
		t.Helper()
		x, err := s.CreateTask(ctx, NewTask{ConnectionID: dbtest.T3Connection, ProjectID: f.project, Title: title, Prompt: "p"})
		if err != nil {
			t.Fatal(err)
		}
		return x.ID
	}
	f.a, f.b, f.c, f.d = task("a"), task("b"), task("c"), task("d")
	tr, err := s.CreateTree(ctx, NewTree{Title: "T", Tasks: []TreeMember{{TaskID: f.a}, {TaskID: f.b, Needs: []int64{f.a}}, {TaskID: f.c, Needs: []int64{f.b}}}})
	if err != nil {
		t.Fatal(err)
	}
	f.tree = tr.ID
	if err := s.e.Write(ctx, func(tx store.Tx) error {
		f.empty, err = insertTree(ctx, tx, "E", StartAuto)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	f.l = launchedTask(t, s, ctx, f.project, "thr-l")
	return f
}

var errRollback = errors.New("roll back")

// Each derived write commits exactly its rows with its cause and rolls
// back with it.
func TestDerivedWritesOnceAndRollBack(t *testing.T) {
	for _, tc := range []struct {
		name string
		// rows: journal rows the write adds (events, or the note itself).
		rows int
		run  func(ctx context.Context, tx store.Tx, f derivedFixture) error
	}{
		{"task event", 1, func(ctx context.Context, tx store.Tx, f derivedFixture) error {
			from := StateBacklog
			return addTaskEvent(ctx, tx, f.a, &from, StateLaunching, "go")
		}},
		{"note", 1, func(ctx context.Context, tx store.Tx, f derivedFixture) error {
			_, err := insertNote(ctx, tx, JournalOwner{TaskID: &f.a}, AppendNoteInput{Text: "n", RequestID: "r"}, nil)
			return err
		}},
		{"tree created", 1, func(ctx context.Context, tx store.Tx, f derivedFixture) error {
			_, err := insertTree(ctx, tx, "new", StartManual)
			return err
		}},
		{"tree config", 1, func(ctx context.Context, tx store.Tx, f derivedFixture) error {
			return updateTree(ctx, tx, f.tree, Optional[string]{Set: true, V: "renamed"}, Optional[StartMode]{})
		}},
		{"tree status", 1, func(ctx context.Context, tx store.Tx, f derivedFixture) error {
			return setTreeStatus(ctx, tx, f.tree, TreeDone)
		}},
		{"archive dissolved", 1, func(ctx context.Context, tx store.Tx, f derivedFixture) error {
			return archiveDissolved(ctx, tx, f.empty)
		}},
		{"membership", 1, func(ctx context.Context, tx store.Tx, f derivedFixture) error {
			return setMembership(ctx, tx, f.d, &f.tree)
		}},
		{"need added", 1, func(ctx context.Context, tx store.Tx, f derivedFixture) error {
			return addNeed(ctx, tx, f.c, f.a)
		}},
		{"needs kept", 1, func(ctx context.Context, tx store.Tx, f derivedFixture) error {
			return keepNeeds(ctx, tx, f.b, nil)
		}},
		{"needs inherited", 1, func(ctx context.Context, tx store.Tx, f derivedFixture) error {
			return inheritNeeds(ctx, tx, f.b)
		}},
		{"needs dropped", 2, func(ctx context.Context, tx store.Tx, f derivedFixture) error {
			return dropNeeds(ctx, tx, f.b)
		}},
		// 2 needs removed, 3 members moved, the emptied tree archived.
		{"members detached", 6, func(ctx context.Context, tx store.Tx, f derivedFixture) error {
			return detachMembers(ctx, tx, f.tree)
		}},
		{"acceptance", 0, func(ctx context.Context, tx store.Tx, f derivedFixture) error {
			_, err := insertAcceptance(ctx, tx, f.l, "user", AcceptedEvidence{}, "")
			return err
		}},
		{"handoff", 0, func(ctx context.Context, tx store.Tx, f derivedFixture) error {
			return recordHandoff(ctx, tx, f.l, Handoff{Attempt: 1, Status: HandoffSent, SourceTaskID: &f.a, Text: "x"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, ctx, pool := connectionStore(t)
			f := newDerivedFixture(t, s, ctx)
			before, rows := derivedState(t, pool), journalCount(t, pool)
			err := s.e.Write(ctx, func(tx store.Tx) error {
				if err := tc.run(ctx, tx, f); err != nil {
					return err
				}
				return errRollback
			})
			if !errors.Is(err, errRollback) {
				t.Fatalf("write: %v", err)
			}
			if after := derivedState(t, pool); after != before {
				t.Fatalf("rollback left rows:\nbefore %s\nafter  %s", before, after)
			}
			if err := s.e.Write(ctx, func(tx store.Tx) error { return tc.run(ctx, tx, f) }); err != nil {
				t.Fatal(err)
			}
			if got := journalCount(t, pool) - rows; got != tc.rows {
				t.Fatalf("journal rows written %d, want %d", got, tc.rows)
			}
			if derivedState(t, pool) == before {
				t.Fatal("committed write changed nothing")
			}
		})
	}
}

// A handoff can omit its source session when none was recorded.
func TestHandoffWithoutSourceSession(t *testing.T) {
	s, pool, moved := movedT3(t)
	ctx := t.Context()
	project := projectWithBinding(t, s, moved, "/w/a")
	mustExec(t, pool, `INSERT INTO tasks(id,project_id,title,prompt,state,launch_attempt,connection_id) VALUES(7,$1,'dst','p','launching',1,$2)`, project, moved)
	src := int64(7)
	if err := s.RecordHandoff(ctx, 7, Handoff{Attempt: 1, Status: HandoffSent, SourceTaskID: &src, Text: "x"}); err != nil {
		t.Fatal(err)
	}
	if got := handoffSession(t, pool, 7); got != "-" {
		t.Fatalf("handoff session %s, want none", got)
	}
}
