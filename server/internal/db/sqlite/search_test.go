package sqlite

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db"
)

func migratedStore(t *testing.T) (*DB, *db.Store) {
	t.Helper()
	ctx := context.Background()
	pool, err := Open(ctx, filepath.Join(t.TempDir(), DBFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool, db.NewStore(pool)
}

// The FTS tables follow every write to the searched columns (triggers), so
// FTS5's integrity check against the content tables passes after inserts,
// edits and deletes.
func TestSearchIndexFollowsWrites(t *testing.T) {
	ctx := context.Background()
	pool, s := migratedStore(t)
	var triggers []string
	rows, err := pool.Query(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name LIKE '%\_fts\_%' ESCAPE '\' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		triggers = append(triggers, name)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"journal_fts_delete", "journal_fts_insert", "journal_fts_update", "tasks_fts_delete", "tasks_fts_insert", "tasks_fts_update"}; !slices.Equal(triggers, want) {
		t.Fatalf("triggers %v, want %v", triggers, want)
	}
	p, err := s.InsertProject(ctx, db.Project{Title: "p", Bindings: []db.ProjectBinding{{ConnectionID: 1, ExternalProjectID: "p", Workspace: "/w/p"}}})
	if err != nil {
		t.Fatal(err)
	}
	newTask := func(title string) db.Task {
		task, err := s.CreateTask(ctx, db.NewTask{ProjectID: p.ID, Title: title, Prompt: "prompt " + title, RuntimeMode: "full-access", InteractionMode: "default", Workspace: db.Workspace{Type: "root"}})
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	a, b := newTask("alpha"), newTask("beta")
	if _, err := s.UpdateTask(ctx, a.ID, db.TaskPatch{Title: db.Optional[string]{Set: true, V: "alpha renamed"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendNote(ctx, db.JournalOwner{TaskID: &a.ID}, db.AppendNoteInput{Text: "a note", RequestID: "n"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTask(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"tasks_fts", "journal_fts"} {
		// rank 1: also compare the index with the external content table.
		if _, err := pool.Exec(`INSERT INTO ` + table + `(` + table + `, rank) VALUES ('integrity-check', 1)`); err != nil {
			t.Fatalf("%s integrity: %v", table, err)
		}
	}
}

// Queries of 3 or more characters use the trigram index; shorter ones scan.
func TestSearchUsesTrigramIndex(t *testing.T) {
	pool, _ := migratedStore(t)
	plan := func(cond func(*where, string) string, table, q string) string {
		var w where
		c := cond(&w, q)
		rows, err := pool.Query(`EXPLAIN QUERY PLAN SELECT 1 FROM `+table+` WHERE `+c, w.args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			out = append(out, detail)
		}
		return strings.Join(out, "; ")
	}
	for _, c := range []struct {
		cond        func(*where, string) string
		table, fts  string
		long, short string
	}{
		{searchTasks, "tasks t", "tasks_fts", "needle", "ne"},
		{searchJournal, "journal_entries", "journal_fts", "needle", "ne"},
	} {
		if got := plan(c.cond, c.table, c.long); !strings.Contains(got, "VIRTUAL TABLE INDEX") || !strings.Contains(got, c.fts) {
			t.Errorf("%s long query plan: %s", c.table, got)
		}
		if got := plan(c.cond, c.table, c.short); strings.Contains(got, c.fts) {
			t.Errorf("%s short query plan uses the index: %s", c.table, got)
		}
	}
}
