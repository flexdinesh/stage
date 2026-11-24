package sqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/db/store"
)

// BackupsDir is the backups directory inside the data directory (beside
// the database file).
const BackupsDir = "backups"

// KeepBackups is how many stage-schema*.db backups Backup leaves.
const KeepBackups = 3

// backupTime is the UTC timestamp in a backup's name.
const backupTime = "20060102T150405Z"

// Backup copies the database into BackupsDir beside the database file
// (store.Backup).
func (d *DB) Backup() store.Backup { return backup{d} }

type backup struct{ d *DB }

func (b backup) Backup(ctx context.Context, version int64, now time.Time) (string, error) {
	return Backup(ctx, b.d, filepath.Join(filepath.Dir(b.d.path), BackupsDir), version, now)
}

// Backup copies the database (VACUUM INTO) to
// dir/stage-schema<version>-<UTC time>.db, mode 0600 in a 0700 dir. A
// failed copy leaves no file behind. It returns the backup's path.
func Backup(ctx context.Context, d *DB, dir string, version int64, now time.Time) (string, error) {
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
	if err := vacuumInto(ctx, d, tmp); err != nil {
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
func vacuumInto(ctx context.Context, d *DB, path string) error {
	if _, err := d.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
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
