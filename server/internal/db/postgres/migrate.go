//go:build postgres

package postgres

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"slices"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// migrations is the embedded dir: its .sql files are the Postgres
// migrations (README.md only until the baseline, #403).
//
//go:embed migrations
var migrations embed.FS

// goRunner runs one Go migration inside goose's migration transaction.
type goRunner func(ctx context.Context, tx *sql.Tx) error

// goMigrations holds the Postgres runner of every backfill.Steps version: the
// shared logic in internal/db/backfill over this adapter's SQL.
var goMigrations = map[int64]goRunner{}

// newProvider is the goose provider over the embedded and Go migrations.
func newProvider(pool *sql.DB) (*goose.Provider, error) {
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, err
	}
	return providerFor(pool, fsys, goMigrations)
}

// providerFor is goose's postgres provider over fsys's .sql migrations plus
// runners registered as Go migrations, run under goose's Postgres session
// locker (an advisory lock on a dedicated connection) so two processes never
// migrate at once. With no migrations it fails with goose.ErrNoMigrations.
func providerFor(pool *sql.DB, fsys fs.FS, runners map[int64]goRunner) (*goose.Provider, error) {
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("goose session locker: %w", err)
	}
	gos := make([]*goose.Migration, 0, len(runners))
	for v, run := range runners {
		gos = append(gos, goose.NewGoMigration(v, &goose.GoFunc{RunTx: run}, nil))
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, pool, fsys,
		goose.WithSessionLocker(locker), goose.WithGoMigrations(gos...))
	if err != nil {
		return nil, fmt.Errorf("goose provider: %w", err)
	}
	return provider, nil
}

// Migrate applies all pending migrations.
func Migrate(ctx context.Context, pool *sql.DB) error {
	provider, err := newProvider(pool)
	if err != nil {
		return err
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// MigrateTo applies the migrations up to and including version.
func MigrateTo(ctx context.Context, pool *sql.DB, version int64) error {
	provider, err := newProvider(pool)
	if err != nil {
		return err
	}
	if _, err := provider.UpTo(ctx, version); err != nil {
		return fmt.Errorf("migrate to %d: %w", version, err)
	}
	return nil
}

// Versions are the embedded migration versions, .sql and Go, ascending
// (none before the baseline).
func Versions() []int64 {
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		panic(err)
	}
	return versionsIn(fsys, goMigrations)
}

// MaxVersion is the highest embedded migration version, 0 before the
// baseline.
func MaxVersion() int64 {
	vs := Versions()
	if len(vs) == 0 {
		return 0
	}
	return vs[len(vs)-1]
}

// versionsIn lists fsys's .sql versions and the runners' versions,
// ascending.
func versionsIn(fsys fs.FS, runners map[int64]goRunner) []int64 {
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		panic(err)
	}
	seen := map[int64]bool{}
	for _, n := range names {
		v, err := goose.NumericComponent(n)
		if err != nil {
			panic(fmt.Sprintf("migration %s: %v", n, err))
		}
		seen[v] = true
	}
	for v := range runners {
		seen[v] = true
	}
	vs := make([]int64, 0, len(seen))
	for v := range seen {
		vs = append(vs, v)
	}
	slices.Sort(vs)
	return vs
}

// CurrentVersion is the highest applied version in goose_db_version, 0 for
// a database without it (fresh). Versions this binary does not embed count.
func CurrentVersion(ctx context.Context, pool *sql.DB) (int64, error) {
	var exists bool
	if err := pool.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, goose.DefaultTablename).Scan(&exists); err != nil {
		return 0, fmt.Errorf("schema version: %w", err)
	}
	if !exists {
		return 0, nil
	}
	var v int64
	if err := pool.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version_id), 0) FROM `+goose.DefaultTablename+` WHERE is_applied`,
	).Scan(&v); err != nil {
		return 0, fmt.Errorf("schema version: %w", err)
	}
	return v, nil
}

// NewMigrator is goose's postgres dialect, under its session locker, over
// the embedded and Go migrations.
func NewMigrator(pool *sql.DB) store.Migrator { return migrator{pool} }

type migrator struct{ pool *sql.DB }

func (m migrator) Current(ctx context.Context) (int64, error) { return CurrentVersion(ctx, m.pool) }
func (m migrator) Max() int64                                 { return MaxVersion() }
func (m migrator) Up(ctx context.Context) error               { return Migrate(ctx, m.pool) }
