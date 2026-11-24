// Package store holds the storage ports below db.Store: the unit of work,
// one repository per aggregate, the capability ports, and the Go types they
// exchange (docs/storage.md "Ports"). It imports no driver and no adapter;
// port signatures use Go types only, never an engine encoding.
package store

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound: the row does not exist. db.ErrNotFound is this value.
var ErrNotFound = errors.New("not found")

// Engine is one storage engine (SQLite today; Postgres in Docker builds).
type Engine interface {
	UnitOfWork
	Migrator() Migrator
	Backup() Backup
	Ownership() Ownership
	Errors() ErrorClassifier
}

// UnitOfWork runs fn with a transaction handle. Read never takes the write
// lock; Write runs fn in one write transaction, committing when fn returns
// nil and rolling back on error or panic. Nothing writes outside a Write.
type UnitOfWork interface {
	Read(ctx context.Context, fn func(Tx) error) error
	Write(ctx context.Context, fn func(Tx) error) error
}

// Tx is a transaction handle: every repository is reached through it, so
// every repository call runs in its unit of work.
type Tx interface {
	Snippets() Snippets
}

// Migrator applies the engine's embedded migrations (same version numbers
// on every engine).
type Migrator interface {
	// Current is the highest applied version (0 for a fresh database);
	// versions this binary does not embed count.
	Current(ctx context.Context) (int64, error)
	// Max is the highest embedded version.
	Max() int64
	// Up applies every pending migration.
	Up(ctx context.Context) error
}

// Backup copies the database before migrating from version.
type Backup interface {
	// Backup returns where the copy went ("" when the engine takes none).
	Backup(ctx context.Context, version int64, now time.Time) (string, error)
}

// Ownership makes this process the database's single owner
// (docs/storage.md "Single owner").
type Ownership interface {
	// Acquire takes ownership; release gives it up.
	Acquire(ctx context.Context) (release func() error, err error)
}

// ErrorClassifier maps engine errors to the conditions rules decide on
// (docs/storage.md "Errors").
type ErrorClassifier interface {
	IsUniqueViolation(err error) bool
	IsForeignKeyViolation(err error) bool
}
