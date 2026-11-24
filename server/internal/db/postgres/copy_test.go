//go:build postgres

package postgres

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

func TestFKOrder(t *testing.T) {
	got, err := fkOrder([]string{"c", "b", "a", "d"}, map[string][]string{
		"c": {"b", "a"}, "b": {"a"}, "d": {"d"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "d", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
	if _, err := fkOrder([]string{"a", "b"}, map[string][]string{"a": {"b"}, "b": {"a"}}); err == nil || !strings.Contains(err.Error(), "cycle among a, b") {
		t.Fatalf("cycle: %v", err)
	}
}

// A freshly migrated database (the baseline's seeds only) counts as
// empty; any other row occupies it. A database without a schema reports
// version 0 and its tables.
func TestCopyState(t *testing.T) {
	ctx := context.Background()
	d := openEngine(t, freshURL(t), Options{})
	st, err := d.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st != (store.CopyTargetState{}) {
		t.Fatalf("empty database: %+v", st)
	}
	if err := d.Migrator().Up(ctx); err != nil {
		t.Fatal(err)
	}
	st, err = d.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != MaxVersion() || st.Tables == 0 || st.Occupied != "" {
		t.Fatalf("migrated database: %+v", st)
	}
	if _, err := d.pool.Exec(ctx, `UPDATE machines SET label = 'renamed' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if st, _ = d.State(ctx); st.Occupied != "" {
		t.Fatalf("a changed seed row occupies: %+v", st)
	}
	if _, err := d.pool.Exec(ctx, `INSERT INTO snippets (name, body) VALUES ('a', 'b')`); err != nil {
		t.Fatal(err)
	}
	if st, _ = d.State(ctx); st.Occupied != "snippets" {
		t.Fatalf("occupied %q, want snippets", st.Occupied)
	}
}

// Schema lists every table once with known kinds, parents before
// children, and every baseline seed table.
func TestCopySchema(t *testing.T) {
	ctx := context.Background()
	d := openEngine(t, freshURL(t), Options{})
	if err := d.Migrator().Up(ctx); err != nil {
		t.Fatal(err)
	}
	schema, err := d.Schema(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	for i, tb := range schema {
		if _, dup := pos[tb.Name]; dup {
			t.Fatalf("table %s listed twice", tb.Name)
		}
		pos[tb.Name] = i
		if len(tb.Columns) == 0 {
			t.Errorf("table %s has no columns", tb.Name)
		}
	}
	if _, ok := pos["goose_db_version"]; ok {
		t.Fatal("goose's table listed")
	}
	for name := range baselineSeeds {
		if _, ok := pos[name]; !ok {
			t.Errorf("seed table %s missing", name)
		}
	}
	parents, err := d.foreignKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for child, ps := range parents {
		for _, p := range ps {
			if p != child && pos[p] >= pos[child] {
				t.Errorf("%s comes before its parent %s", child, p)
			}
		}
	}
	i := slices.IndexFunc(schema, func(tb store.CopyTable) bool { return tb.Name == "task_acceptances" })
	if i < 0 {
		t.Fatal("task_acceptances missing")
	}
	kinds := map[string]store.CopyKind{}
	for _, c := range schema[i].Columns {
		kinds[c.Name] = c.Kind
	}
	if kinds["evidence"] != store.CopyText || kinds["accepted_at"] != store.CopyTime || kinds["id"] != store.CopyInt || schema[i].Identity != "id" {
		t.Fatalf("task_acceptances: %+v", schema[i])
	}
}
