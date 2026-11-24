package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// goRunner runs one Go migration inside goose's migration transaction.
type goRunner func(ctx context.Context, tx *sql.Tx) error

// goMigrations holds the SQLite runner of every backfill.Steps version: the
// shared logic in internal/db/backfill over this adapter's SQL. Empty until
// the first backfill (docs/storage.md "Adding a migration").
var goMigrations = map[int64]goRunner{}

// newProvider is the goose provider over the embedded migrations and the Go
// migrations.
func newProvider(pool *sql.DB) (*goose.Provider, error) {
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, err
	}
	return providerFor(pool, fsys, goMigrations)
}

// providerFor is the goose sqlite3 provider over fsys's .sql migrations plus
// runners registered as Go migrations at their versions.
func providerFor(pool *sql.DB, fsys fs.FS, runners map[int64]goRunner) (*goose.Provider, error) {
	gos := make([]*goose.Migration, 0, len(runners))
	for v, run := range runners {
		gos = append(gos, goose.NewGoMigration(v, &goose.GoFunc{RunTx: run}, nil))
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, pool, fsys, goose.WithGoMigrations(gos...))
	if err != nil {
		return nil, fmt.Errorf("goose provider: %w", err)
	}
	return provider, nil
}

// Migrate applies all pending goose migrations.
func Migrate(ctx context.Context, d *DB) error {
	provider, err := newProvider(d.DB)
	if err != nil {
		return err
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// MigrateTo applies the embedded migrations up to and including version.
func MigrateTo(ctx context.Context, d *DB, version int64) error {
	provider, err := newProvider(d.DB)
	if err != nil {
		return err
	}
	if _, err := provider.UpTo(ctx, version); err != nil {
		return fmt.Errorf("migrate to %d: %w", version, err)
	}
	return nil
}

// MaxVersion is the highest embedded migration version, .sql or Go.
func MaxVersion() int64 {
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		panic(err)
	}
	var highest int64
	for _, n := range names {
		v, err := goose.NumericComponent(n)
		if err != nil {
			panic(fmt.Sprintf("migration %s: %v", n, err))
		}
		highest = max(highest, v)
	}
	for v := range goMigrations {
		highest = max(highest, v)
	}
	return highest
}

// CurrentVersion is the highest applied version in goose_db_version, 0 for
// a database without it (fresh). Versions this binary does not embed count.
func CurrentVersion(ctx context.Context, d *DB) (int64, error) {
	var n int
	if err := d.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, goose.DefaultTablename,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("schema version: %w", err)
	}
	if n == 0 {
		return 0, nil
	}
	var v int64
	if err := d.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version_id), 0) FROM `+goose.DefaultTablename+` WHERE is_applied`,
	).Scan(&v); err != nil {
		return 0, fmt.Errorf("schema version: %w", err)
	}
	return v, nil
}

// Migrator is goose's sqlite3 dialect over the embedded and Go migrations.
func (d *DB) Migrator() store.Migrator { return migrator{d} }

type migrator struct{ d *DB }

func (m migrator) Current(ctx context.Context) (int64, error) { return CurrentVersion(ctx, m.d) }
func (m migrator) Max() int64                                 { return MaxVersion() }
func (m migrator) Up(ctx context.Context) error               { return Migrate(ctx, m.d) }
