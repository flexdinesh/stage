//go:build postgres

package postgres

import (
	"context"
	"strings"
	"testing"
)

// The search predicates use the pg_trgm GIN indexes (with sequential scans
// off, so the plan shows whether an index can serve them at all).
func TestSearchUsesTrigramIndexes(t *testing.T) {
	ctx := context.Background()
	pool := freshDB(t)
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SET enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		cond    func(*where, string) string
		table   string
		indexes []string
	}{
		{searchTasks, "tasks t", []string{"tasks_title_trgm", "tasks_prompt_trgm"}},
		{searchJournal, "journal_entries", []string{"journal_entries_text_trgm"}},
	} {
		var w where
		rows, err := conn.QueryContext(ctx, `EXPLAIN SELECT 1 FROM `+c.table+` WHERE `+c.cond(&w, "needle"), w.args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, line)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		got := strings.Join(plan, "\n")
		for _, index := range c.indexes {
			if !strings.Contains(got, "Bitmap Index Scan on "+index) {
				t.Errorf("%s: plan does not use %s:\n%s", c.table, index, got)
			}
		}
	}
}
