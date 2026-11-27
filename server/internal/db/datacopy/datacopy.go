// Package datacopy copies a Stage database between engines through the
// store copy ports: `stage db copy` from SQLite into Postgres
// (docs/storage.md "Data copy and cutover"). It holds no SQL and imports no
// driver; the adapters read, write and convert.
package datacopy

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// RefusedError: the source or target is not in a state the copy accepts.
// No rows were written (a fresh target may have been migrated: the schema
// comparison needs its schema).
type RefusedError struct{ Reason string }

func (e *RefusedError) Error() string { return "refusing to copy: " + e.Reason }

func refused(format string, args ...any) error {
	return &RefusedError{Reason: fmt.Sprintf(format, args...)}
}

// Mismatch is one table whose target rows differ from the source's.
type Mismatch struct {
	Table                          string
	SourceRows, TargetRows         int64
	SourceChecksum, TargetChecksum string
}

// MismatchError: verification failed; the load was rolled back.
type MismatchError struct{ Tables []Mismatch }

func (e *MismatchError) Error() string {
	parts := make([]string, len(e.Tables))
	for i, m := range e.Tables {
		parts[i] = fmt.Sprintf("%s (source %d rows %s, target %d rows %s)",
			m.Table, m.SourceRows, short(m.SourceChecksum), m.TargetRows, short(m.TargetChecksum))
	}
	return "verification failed, target rolled back: " + strings.Join(parts, "; ")
}

// TableReport is one verified table.
type TableReport struct {
	Name     string
	Rows     int64
	Checksum string // hex sha256 (see checksum.go)
}

// Report is a finished, verified copy.
type Report struct {
	Version int64
	// Migrated: the target was fresh and the copy migrated it.
	Migrated bool
	Tables   []TableReport
}

// batchRows and batchBytes bound one Insert.
const (
	batchRows  = 1000
	batchBytes = 16 << 20
)

// Run copies src into dst:
//
//  1. src must be at dst's head (the binary's schema head);
//  2. dst must be fresh (no tables: Run migrates it to the head) or at the
//     head with only the baseline's seeded rows;
//  3. both must hold the same tables and columns;
//  4. in one dst transaction: clear the seeded rows, copy durable tables in
//     foreign-key order, restart identities past the source's highest
//     issued ID, then read every table back and compare row counts and
//     checksums with the planned rows. Auth sessions and grants are empty
//     after import. Any error or mismatch rolls it back.
func Run(ctx context.Context, src store.CopySource, dst store.CopyTarget) (Report, error) {
	var rep Report
	head := dst.Migrator().Max()
	v, err := src.Version(ctx)
	if err != nil {
		return rep, err
	}
	switch {
	case v < head:
		return rep, refused("source schema %d is older than this binary's %d: start this stage version once on the SQLite database (it migrates it, with a backup), stop it, then copy", v, head)
	case v > head:
		return rep, refused("source schema %d is newer than this binary's %d: use a stage version at schema %d", v, head, v)
	}
	rep.Version = v
	st, err := dst.State(ctx)
	if err != nil {
		return rep, err
	}
	switch {
	case st.Version == 0 && st.Tables > 0:
		return rep, refused("target database has %d tables but no stage schema: copy into an empty database", st.Tables)
	case st.Version == 0:
		if err := dst.Migrator().Up(ctx); err != nil {
			return rep, fmt.Errorf("migrate target: %w", err)
		}
		rep.Migrated = true
	case st.Version != head:
		return rep, refused("target schema %d is not %d: copy into an empty database", st.Version, head)
	case st.Occupied != "":
		return rep, refused("target database holds data (table %s): copy into an empty database", st.Occupied)
	}
	schema, err := dst.Schema(ctx)
	if err != nil {
		return rep, err
	}
	srcTables, err := src.Tables(ctx)
	if err != nil {
		return rep, err
	}
	if diffs := compareTables(schema, srcTables); len(diffs) > 0 {
		return rep, refused("source and target schemas differ: %s", strings.Join(diffs, "; "))
	}
	seqs, err := src.Sequences(ctx)
	if err != nil {
		return rep, err
	}
	err = dst.Load(ctx, func(l store.CopyLoad) error {
		rep.Tables = rep.Tables[:0]
		if err := l.Clear(ctx, schema); err != nil {
			return err
		}
		sums := make([]tableSum, len(schema))
		for i, t := range schema {
			s, err := copyTable(ctx, src, l, t)
			if err != nil {
				return err
			}
			if t.Identity != "" {
				if err := l.RestartIdentity(ctx, t, max(s.maxID, seqs[t.Name])); err != nil {
					return err
				}
			}
			sums[i] = s
		}
		var mismatches []Mismatch
		for i, t := range schema {
			got := newSum(t)
			if err := l.Rows(ctx, t, got.add); err != nil {
				return err
			}
			want, have := sums[i].checksum(), got.checksum()
			if got.rows != sums[i].rows || have != want {
				mismatches = append(mismatches, Mismatch{Table: t.Name,
					SourceRows: sums[i].rows, TargetRows: got.rows, SourceChecksum: want, TargetChecksum: have})
				continue
			}
			rep.Tables = append(rep.Tables, TableReport{Name: t.Name, Rows: got.rows, Checksum: have})
		}
		if len(mismatches) > 0 {
			return &MismatchError{Tables: mismatches}
		}
		return nil
	})
	if err != nil {
		rep.Tables = nil
		return rep, err
	}
	return rep, nil
}

// copyTable streams t's source rows into the target in batches, summing
// them as they pass.
func copyTable(ctx context.Context, src store.CopySource, l store.CopyLoad, t store.CopyTable) (tableSum, error) {
	sum := newSum(t)
	if t.Name == "auth_sessions" || t.Name == "auth_grants" {
		return sum, nil
	}
	sessionColumn := -1
	if t.Name == "push_subscriptions" {
		for i, col := range t.Columns {
			if col.Name == "auth_session_id" {
				sessionColumn = i
				break
			}
		}
	}
	var batch []store.CopyRow
	size := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := l.Insert(ctx, t, batch); err != nil {
			return err
		}
		batch, size = batch[:0], 0
		return nil
	}
	err := src.Rows(ctx, t, func(row store.CopyRow) error {
		// A copied device cannot use a session the restore deliberately
		// invalidates. Personal subscriptions have no session to invalidate.
		if sessionColumn >= 0 && row[sessionColumn] != nil {
			return nil
		}
		if err := sum.add(row); err != nil {
			return err
		}
		batch = append(batch, row)
		size += rowSize(row)
		if len(batch) >= batchRows || size >= batchBytes {
			return flush()
		}
		return nil
	})
	if err != nil {
		return sum, err
	}
	return sum, flush()
}

func rowSize(row store.CopyRow) int {
	n := 0
	for _, v := range row {
		switch v := v.(type) {
		case string:
			n += len(v)
		case []byte:
			n += len(v)
		default:
			n += 8
		}
	}
	return n
}

// compareTables lists tables or columns present on one side only. Column
// order may differ: rows are read and written by name.
func compareTables(schema []store.CopyTable, src map[string][]string) []string {
	var diffs []string
	seen := map[string]bool{}
	for _, t := range schema {
		seen[t.Name] = true
		cols, ok := src[t.Name]
		if !ok {
			diffs = append(diffs, "table "+t.Name+" missing from the source")
			continue
		}
		have := map[string]bool{}
		for _, c := range cols {
			have[c] = true
		}
		want := map[string]bool{}
		for _, c := range t.Columns {
			want[c.Name] = true
			if !have[c.Name] {
				diffs = append(diffs, "column "+t.Name+"."+c.Name+" missing from the source")
			}
		}
		for _, c := range cols {
			if !want[c] {
				diffs = append(diffs, "column "+t.Name+"."+c+" missing from the target")
			}
		}
	}
	var extra []string
	for name := range src {
		if !seen[name] {
			extra = append(extra, "table "+name+" missing from the target")
		}
	}
	slices.Sort(extra)
	return append(diffs, extra...)
}

func short(sum string) string {
	if len(sum) > 16 {
		return sum[:16]
	}
	return sum
}
