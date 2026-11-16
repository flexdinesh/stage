package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
)

// BackupsDir is the backups directory inside the data directory.
const BackupsDir = "backups"

// KeepBackups is how many stage-schema*.db backups Backup leaves.
const KeepBackups = 3

// backupTime is the UTC timestamp in a backup's name.
const backupTime = "20060102T150405Z"

// ErrSchemaTooNew matches a *SchemaTooNewError.
var ErrSchemaTooNew = errors.New("database schema is newer than this Stage")

// SchemaTooNewError: the database was migrated by a newer Stage.
type SchemaTooNewError struct{ Current, Known int64 }

func (e *SchemaTooNewError) Error() string {
	return fmt.Sprintf("database schema %d is newer than this Stage (knows %d): install a newer Stage or restore a backup (docs/install.md#recovery)", e.Current, e.Known)
}

func (e *SchemaTooNewError) Is(target error) bool { return target == ErrSchemaTooNew }

// MaxVersion is the highest embedded migration version.
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
	return highest
}

// CurrentVersion is the highest applied version in goose_db_version, 0 for
// a database without it (fresh). Versions this binary does not embed count.
func CurrentVersion(ctx context.Context, pool *sql.DB) (int64, error) {
	var n int
	if err := pool.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, goose.DefaultTablename,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("schema version: %w", err)
	}
	if n == 0 {
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

// SchemaVersion is CurrentVersion of the store's database.
func (s *Store) SchemaVersion(ctx context.Context) (int64, error) {
	return CurrentVersion(ctx, s.pool.db)
}

// Backup copies the database (VACUUM INTO) to
// dir/stage-schema<version>-<UTC time>.db, mode 0600 in a 0700 dir. A
// failed copy leaves no file behind. It returns the backup's path.
func Backup(ctx context.Context, pool *sql.DB, dir string, version int64, now time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("stage-schema%d-%s.db", version, now.UTC().Format(backupTime)))
	tmp := path + ".tmp"
	// VACUUM INTO accepts an existing empty file, which keeps mode 0600.
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	_ = f.Close()
	if err := vacuumInto(ctx, pool, tmp); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("backup %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("backup: %w", err)
	}
	return path, nil
}

// vacuumInto writes a consistent copy of the database to path and syncs it.
func vacuumInto(ctx context.Context, pool *sql.DB, path string) error {
	if _, err := pool.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// PruneBackups deletes all but the newest keep stage-schema*.db backups in
// dir (newest by the timestamp in the name). Other files are left alone.
func PruneBackups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("prune backups: %w", err)
	}
	type backup struct{ name, stamp string }
	var backups []backup
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || !strings.HasPrefix(name, "stage-schema") || !strings.HasSuffix(name, ".db") {
			continue
		}
		i := strings.LastIndexByte(name, '-')
		stamp := strings.TrimSuffix(name[i+1:], ".db")
		if _, err := time.Parse(backupTime, stamp); err != nil {
			continue
		}
		backups = append(backups, backup{name, stamp})
	}
	slices.SortFunc(backups, func(a, b backup) int {
		if c := strings.Compare(b.stamp, a.stamp); c != 0 {
			return c
		}
		return strings.Compare(b.name, a.name)
	})
	for _, b := range backups[min(keep, len(backups)):] {
		if err := os.Remove(filepath.Join(dir, b.name)); err != nil {
			return fmt.Errorf("prune backups: %w", err)
		}
	}
	return nil
}
