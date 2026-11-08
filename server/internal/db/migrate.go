// Package db owns the Postgres schema and data access.
package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrate applies all pending goose migrations.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	return MigrateTo(ctx, pool, 0)
}

// MigrateTo applies pending migrations up to version (0 = latest).
func MigrateTo(ctx context.Context, pool *pgxpool.Pool, version int64) error {
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, fsys)
	if err != nil {
		return fmt.Errorf("goose provider: %w", err)
	}
	if version == 0 {
		_, err = provider.Up(ctx)
	} else {
		_, err = provider.UpTo(ctx, version)
	}
	if err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}
