package store

import "context"

// Data copy ports: `stage db copy` reads a SQLite database through a
// CopySource and writes a Postgres one through a CopyTarget; the
// driver-free internal/db/datacopy runs it (docs/storage.md "Data copy and
// cutover").

// CopyKind is a column's value class in a data copy: the Go type its values
// travel as between engines. The target's column types decide it.
type CopyKind int

const (
	CopyInt   CopyKind = iota + 1 // int64
	CopyBool                      // bool
	CopyReal                      // float64
	CopyText                      // string (evidence JSON too: bytes kept)
	CopyTime                      // time.Time, UTC, microseconds
	CopyJSON                      // json.RawMessage (queried JSON)
	CopyBytes                     // []byte
)

func (k CopyKind) String() string {
	switch k {
	case CopyInt:
		return "int"
	case CopyBool:
		return "bool"
	case CopyReal:
		return "real"
	case CopyText:
		return "text"
	case CopyTime:
		return "time"
	case CopyJSON:
		return "json"
	case CopyBytes:
		return "bytes"
	}
	return "unknown"
}

// CopyColumn is one column of a copied table.
type CopyColumn struct {
	Name string
	Kind CopyKind
}

// CopyTable is one copied table: its columns in the target's order and its
// identity column ("" when it has none).
type CopyTable struct {
	Name     string
	Columns  []CopyColumn
	Identity string
}

// CopyRow is one row in its table's column order: each value nil or its
// column kind's Go type.
type CopyRow = []any

// CopySource reads the database a copy comes from, without writing to it.
type CopySource interface {
	// Version is the source's schema version.
	Version(ctx context.Context) (int64, error)
	// Tables are the source's Stage tables (engine-internal tables left
	// out) and their column names.
	Tables(ctx context.Context) (map[string][]string, error)
	// Sequences are the highest IDs ever issued per table (SQLite's
	// AUTOINCREMENT high-water marks), which may exceed the highest left.
	Sequences(ctx context.Context) (map[string]int64, error)
	// Rows calls fn with each row of t, converted to t's column kinds. A
	// value that does not convert fails, naming the table and column.
	Rows(ctx context.Context, t CopyTable, fn func(CopyRow) error) error
}

// CopyTargetState describes the target before a copy.
type CopyTargetState struct {
	// Version is the target's schema version (0: no schema).
	Version int64
	// Tables counts the tables in the target's schema.
	Tables int
	// Occupied names the first table holding rows beyond the baseline's
	// seeded rows ("" when none).
	Occupied string
}

// CopyTarget is the database a copy goes to.
type CopyTarget interface {
	Migrator() Migrator
	// State reads the target's version and contents.
	State(ctx context.Context) (CopyTargetState, error)
	// Schema is every Stage table, parents before children (foreign-key
	// order).
	Schema(ctx context.Context) ([]CopyTable, error)
	// Load runs fn in one transaction, committed when fn returns nil and
	// rolled back otherwise.
	Load(ctx context.Context, fn func(CopyLoad) error) error
}

// CopyLoad writes and reads the target inside Load's transaction.
type CopyLoad interface {
	// Clear empties tables (the baseline's seeded rows).
	Clear(ctx context.Context, tables []CopyTable) error
	// Insert adds rows to t, keeping their IDs.
	Insert(ctx context.Context, t CopyTable, rows []CopyRow) error
	// RestartIdentity makes t's next generated ID last+1.
	RestartIdentity(ctx context.Context, t CopyTable, last int64) error
	// Rows calls fn with each row of t as the transaction sees it.
	Rows(ctx context.Context, t CopyTable, fn func(CopyRow) error) error
}
