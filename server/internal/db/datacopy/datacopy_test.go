package datacopy

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// The coordinator against in-memory fakes: the rules that need no engine.
// The round trip on real engines is roundtrip_postgres_test.go.

var testTable = store.CopyTable{
	Name:     "items",
	Identity: "id",
	Columns: []store.CopyColumn{
		{Name: "id", Kind: store.CopyInt},
		{Name: "doc", Kind: store.CopyJSON},
		{Name: "at", Kind: store.CopyTime},
	},
}

type fakeSource struct {
	version int64
	tables  map[string][]string
	seqs    map[string]int64
	rows    map[string][]store.CopyRow
}

func (s *fakeSource) Version(context.Context) (int64, error) { return s.version, nil }
func (s *fakeSource) Tables(context.Context) (map[string][]string, error) {
	return s.tables, nil
}
func (s *fakeSource) Sequences(context.Context) (map[string]int64, error) { return s.seqs, nil }
func (s *fakeSource) Rows(_ context.Context, t store.CopyTable, fn func(store.CopyRow) error) error {
	for _, r := range s.rows[t.Name] {
		if err := fn(r); err != nil {
			return err
		}
	}
	return nil
}

type fakeMigrator struct {
	max int64
	ups *int
}

func (m fakeMigrator) Current(context.Context) (int64, error) { return 0, nil }
func (m fakeMigrator) Max() int64                             { return m.max }
func (m fakeMigrator) Up(context.Context) error               { *m.ups++; return nil }

// fakeTarget keeps committed rows; a load writes to a copy, kept on commit.
type fakeTarget struct {
	head      int64
	state     store.CopyTargetState
	schema    []store.CopyTable
	committed map[string][]store.CopyRow
	identity  map[string]int64
	ups       int
	// readBack alters rows read back inside the load (a lossy engine).
	readBack func(store.CopyRow) store.CopyRow
}

func (t *fakeTarget) Migrator() store.Migrator { return fakeMigrator{t.head, &t.ups} }
func (t *fakeTarget) State(context.Context) (store.CopyTargetState, error) {
	return t.state, nil
}
func (t *fakeTarget) Schema(context.Context) ([]store.CopyTable, error) { return t.schema, nil }
func (t *fakeTarget) Load(ctx context.Context, fn func(store.CopyLoad) error) error {
	l := &fakeLoad{t: t, rows: map[string][]store.CopyRow{}, identity: map[string]int64{}}
	if err := fn(l); err != nil {
		return err
	}
	t.committed, t.identity = l.rows, l.identity
	return nil
}

type fakeLoad struct {
	t        *fakeTarget
	rows     map[string][]store.CopyRow
	identity map[string]int64
}

func (l *fakeLoad) Clear(context.Context, []store.CopyTable) error { return nil }
func (l *fakeLoad) Insert(_ context.Context, t store.CopyTable, rows []store.CopyRow) error {
	for _, r := range rows {
		l.rows[t.Name] = append(l.rows[t.Name], slices.Clone(r))
	}
	return nil
}
func (l *fakeLoad) RestartIdentity(_ context.Context, t store.CopyTable, last int64) error {
	l.identity[t.Name] = last
	return nil
}
func (l *fakeLoad) Rows(_ context.Context, t store.CopyTable, fn func(store.CopyRow) error) error {
	for _, r := range l.rows[t.Name] {
		if l.t.readBack != nil {
			r = l.t.readBack(slices.Clone(r))
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return nil
}

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t
}

func fixture() (*fakeSource, *fakeTarget) {
	src := &fakeSource{
		version: 29,
		tables:  map[string][]string{"items": {"at", "doc", "id"}},
		seqs:    map[string]int64{"items": 9},
		rows: map[string][]store.CopyRow{"items": {
			{int64(1), json.RawMessage(`{"b":1,"a":[1.0]}`), at("2026-11-01T10:00:00.123456Z")},
			{int64(4), nil, at("2026-11-01T10:00:00Z")},
		}},
	}
	dst := &fakeTarget{head: 29, state: store.CopyTargetState{Version: 29, Tables: 3}, schema: []store.CopyTable{testTable}}
	return src, dst
}

func TestRunCopiesAndVerifies(t *testing.T) {
	src, dst := fixture()
	rep, err := Run(context.Background(), src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Tables) != 1 || rep.Tables[0].Rows != 2 || len(rep.Tables[0].Checksum) != 64 {
		t.Fatalf("report %+v", rep)
	}
	if len(dst.committed["items"]) != 2 {
		t.Fatalf("committed %v", dst.committed)
	}
	// The source's high-water mark (9) beats its highest row (4).
	if dst.identity["items"] != 9 {
		t.Fatalf("identity restarted after %d, want 9", dst.identity["items"])
	}
	if rep.Migrated || dst.ups != 0 {
		t.Fatal("a seeds-only target was migrated")
	}
}

func TestRunIdentityPastHighestRow(t *testing.T) {
	src, dst := fixture()
	src.seqs = map[string]int64{}
	if _, err := Run(context.Background(), src, dst); err != nil {
		t.Fatal(err)
	}
	if dst.identity["items"] != 4 {
		t.Fatalf("identity restarted after %d, want 4", dst.identity["items"])
	}
}

func TestRunMigratesFreshTarget(t *testing.T) {
	src, dst := fixture()
	dst.state = store.CopyTargetState{}
	rep, err := Run(context.Background(), src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Migrated || dst.ups != 1 {
		t.Fatalf("migrated %v ups %d", rep.Migrated, dst.ups)
	}
}

// A read-back that differs from the source fails verification and nothing
// is committed.
func TestRunMismatchRollsBack(t *testing.T) {
	cases := map[string]func(store.CopyRow) store.CopyRow{
		"time precision lost": func(r store.CopyRow) store.CopyRow {
			if tm, ok := r[2].(time.Time); ok {
				r[2] = tm.Truncate(time.Millisecond)
			}
			return r
		},
		"json value changed": func(r store.CopyRow) store.CopyRow {
			if r[1] != nil {
				r[1] = json.RawMessage(`{"a":[1],"b":2}`)
			}
			return r
		},
	}
	for name, readBack := range cases {
		t.Run(name, func(t *testing.T) {
			src, dst := fixture()
			dst.readBack = readBack
			_, err := Run(context.Background(), src, dst)
			var mm *MismatchError
			if !errors.As(err, &mm) || len(mm.Tables) != 1 || mm.Tables[0].Table != "items" {
				t.Fatalf("got %v, want a mismatch on items", err)
			}
			if dst.committed != nil {
				t.Fatalf("committed despite the mismatch: %v", dst.committed)
			}
		})
	}
}

// jsonb reorders keys, drops whitespace and normalizes numbers: the same
// value verifies.
func TestRunJSONByValue(t *testing.T) {
	src, dst := fixture()
	dst.readBack = func(r store.CopyRow) store.CopyRow {
		if r[1] != nil {
			r[1] = json.RawMessage(`{"a": [1], "b": 1.0}`)
		}
		return r
	}
	if _, err := Run(context.Background(), src, dst); err != nil {
		t.Fatal(err)
	}
}

func TestRunRefuses(t *testing.T) {
	cases := map[string]struct {
		edit func(*fakeSource, *fakeTarget)
		want string
	}{
		"source older":         {func(s *fakeSource, _ *fakeTarget) { s.version = 28 }, "older than this binary's 29"},
		"source newer":         {func(s *fakeSource, _ *fakeTarget) { s.version = 30 }, "newer than this binary's 29"},
		"target other version": {func(_ *fakeSource, d *fakeTarget) { d.state.Version = 28 }, "target schema 28 is not 29"},
		"target occupied":      {func(_ *fakeSource, d *fakeTarget) { d.state.Occupied = "tasks" }, "holds data (table tasks)"},
		"target foreign tables": {func(_ *fakeSource, d *fakeTarget) { d.state = store.CopyTargetState{Tables: 2} },
			"no stage schema"},
		"column missing": {func(s *fakeSource, _ *fakeTarget) { s.tables["items"] = []string{"id", "doc"} },
			"column items.at missing from the source"},
		"extra column": {func(s *fakeSource, _ *fakeTarget) { s.tables["items"] = []string{"id", "doc", "at", "legacy"} },
			"column items.legacy missing from the target"},
		"extra table":   {func(s *fakeSource, _ *fakeTarget) { s.tables["old"] = []string{"id"} }, "table old missing from the target"},
		"missing table": {func(s *fakeSource, _ *fakeTarget) { delete(s.tables, "items") }, "table items missing from the source"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			src, dst := fixture()
			c.edit(src, dst)
			_, err := Run(context.Background(), src, dst)
			var refused *RefusedError
			if !errors.As(err, &refused) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want refusal %q", err, c.want)
			}
			if dst.committed != nil || dst.ups != 0 {
				t.Fatal("a refused copy wrote to the target")
			}
		})
	}
}

func TestChecksum(t *testing.T) {
	sum := func(rows ...store.CopyRow) string {
		t.Helper()
		s := newSum(testTable)
		for _, r := range rows {
			if err := s.add(r); err != nil {
				t.Fatal(err)
			}
		}
		return s.checksum()
	}
	t0 := at("2026-11-01T10:00:00.123456Z")
	a := store.CopyRow{int64(1), json.RawMessage(`{"x":1}`), t0}
	b := store.CopyRow{int64(2), nil, t0}
	if sum(a, b) != sum(b, a) {
		t.Fatal("checksum depends on row order")
	}
	if sum(a, b) == sum(a, a) || sum(a) == sum(a, a) {
		t.Fatal("checksum ignores a row")
	}
	same := store.CopyRow{int64(1), json.RawMessage(` { "x" : 1.00 } `), t0.In(time.FixedZone("x", 3600))}
	if sum(a) != sum(same) {
		t.Fatal("equal values (JSON by value, time by instant) differ")
	}
	null := store.CopyRow{int64(1), json.RawMessage(`null`), t0}
	if sum(null) == sum(store.CopyRow{int64(1), nil, t0}) {
		t.Fatal("JSON null and SQL NULL collide")
	}
	text := store.CopyTable{Name: "t", Columns: []store.CopyColumn{{Name: "a", Kind: store.CopyText}, {Name: "b", Kind: store.CopyText}}}
	s1, s2 := newSum(text), newSum(text)
	_ = s1.add(store.CopyRow{"ab", "c"})
	_ = s2.add(store.CopyRow{"a", "bc"})
	if s1.checksum() == s2.checksum() {
		t.Fatal("adjacent text values collide")
	}
	bad := newSum(testTable)
	if err := bad.add(store.CopyRow{"1", nil, t0}); err == nil {
		t.Fatal("a value of the wrong type was summed")
	}
}
