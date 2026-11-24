//go:build postgres

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

// Options configure Open.
type Options struct {
	// ApplicationName is the session's application_name ("stage
	// <instanceId>"), reported to a second owner (docs/storage.md "Single
	// owner").
	ApplicationName string
	// Logger receives the backup warning and ownership loss (default
	// slog.Default()).
	Logger *slog.Logger
	// MaxConns caps the pool (0: pgxpool's default).
	MaxConns int32
	// OwnershipPing is how often a held ownership lease checks its
	// connection (0: ownershipPing).
	OwnershipPing time.Duration
}

// DB is an open Postgres database and the Postgres store.Engine over it.
type DB struct {
	pool *pgxpool.Pool
	// sqlDB is database/sql over the pool, for goose only.
	sqlDB *sql.DB
	url   string
	opts  Options
	log   *slog.Logger
}

var _ store.Engine = (*DB)(nil)

// Open connects to the database url names (postgres:// or postgresql://).
// It creates nothing and migrates nothing.
func Open(ctx context.Context, url string, opts Options) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("open database: invalid url") // never echo the URL: it may hold credentials
	}
	if opts.ApplicationName != "" {
		cfg.ConnConfig.RuntimeParams["application_name"] = opts.ApplicationName
	}
	if opts.MaxConns > 0 {
		cfg.MaxConns = opts.MaxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &DB{pool: pool, sqlDB: stdlib.OpenDBFromPool(pool), url: url, opts: opts, log: log}, nil
}

// Close closes the pool.
func (d *DB) Close() error {
	err := d.sqlDB.Close()
	d.pool.Close()
	return err
}

// Pool is the pgx pool (tests query through it).
func (d *DB) Pool() *pgxpool.Pool { return d.pool }

// writeAttempts is how many times Write runs fn when the transaction fails
// with a serialization failure or a deadlock (docs/storage.md "Postgres").
const writeAttempts = 3

// Read runs fn in a read-only READ COMMITTED transaction: each statement
// sees the latest commit.
func (d *DB) Read(ctx context.Context, fn func(store.Tx) error) error {
	return d.inTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly}, fn)
}

// Write runs fn in a READ COMMITTED transaction, committing when fn returns
// nil and rolling back on error or panic. Use cases that read, check and
// write take row locks through the repositories' Lock methods. A
// serialization failure (40001) or deadlock (40P01) reruns fn, up to
// writeAttempts runs; fn has no effects outside the transaction.
func (d *DB) Write(ctx context.Context, fn func(store.Tx) error) error {
	var err error
	for attempt := 1; ; attempt++ {
		err = d.inTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, fn)
		if !retryable(err) || attempt == writeAttempts {
			return err
		}
		backoff := time.Duration(attempt) * (5*time.Millisecond + rand.N(20*time.Millisecond))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
}

func (d *DB) inTx(ctx context.Context, txo pgx.TxOptions, fn func(store.Tx) error) (err error) {
	tx, err := d.pool.BeginTx(ctx, txo)
	if err != nil {
		return contextErr(ctx, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if err := fn(newTx(&runner{tx})); err != nil {
		return contextErr(ctx, err)
	}
	committed = true
	return contextErr(ctx, tx.Commit(ctx))
}

// contextErr returns the context's error when the statement was cancelled
// because ctx ended (57014 or a pgx context error), else err.
func contextErr(ctx context.Context, err error) error {
	if err == nil || ctx.Err() == nil {
		return err
	}
	if pgCode(err) == "57014" || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ctx.Err()
	}
	return err
}

func retryable(err error) bool {
	switch pgCode(err) {
	case "40001", "40P01":
		return true
	}
	return false
}

func pgCode(err error) string {
	var e *pgconn.PgError
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func (d *DB) Errors() store.ErrorClassifier { return classifier{} }

type classifier struct{}

func (classifier) IsUniqueViolation(err error) bool     { return isUniqueViolation(err) }
func (classifier) IsForeignKeyViolation(err error) bool { return isForeignKeyViolation(err) }

func isUniqueViolation(err error) bool     { return pgCode(err) == "23505" }
func isForeignKeyViolation(err error) bool { return pgCode(err) == "23503" }

// notFound maps a missing row to store.ErrNotFound.
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	return err
}

// Migrator is goose's postgres dialect under its session locker over the
// embedded and Go migrations.
func (d *DB) Migrator() store.Migrator { return NewMigrator(d.sqlDB) }

// Backup takes none: Postgres backups are the operator's. It logs a warning
// before migrating (docs/storage.md "Backups").
func (d *DB) Backup() store.Backup { return backup{d} }

type backup struct{ d *DB }

func (b backup) Backup(_ context.Context, version int64, _ time.Time) (string, error) {
	b.d.log.Warn("migrating the Postgres schema without a backup: Postgres backups are operator-owned (pg_dump, snapshots, point-in-time recovery)",
		"schema", version, "migrating_to", MaxVersion())
	return "", nil
}

// where builds a dynamic WHERE clause with numbered placeholders.
type where struct {
	conds []string
	args  []any
}

func (w *where) arg(v any) string {
	w.args = append(w.args, v)
	return "$" + strconv.Itoa(len(w.args))
}

func (w *where) add(cond string) { w.conds = append(w.conds, cond) }

func (w *where) sql() string {
	if len(w.conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(w.conds, " AND ")
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
