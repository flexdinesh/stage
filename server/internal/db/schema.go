package db

import (
	"context"
	"errors"
	"fmt"
)

// ErrSchemaTooNew matches a *SchemaTooNewError.
var ErrSchemaTooNew = errors.New("database schema is newer than this Stage")

// SchemaTooNewError: the database was migrated by a newer Stage.
type SchemaTooNewError struct{ Current, Known int64 }

func (e *SchemaTooNewError) Error() string {
	return fmt.Sprintf("database schema %d is newer than this Stage (knows %d): install a newer Stage or restore a backup (docs/install.md#recovery)", e.Current, e.Known)
}

func (e *SchemaTooNewError) Is(target error) bool { return target == ErrSchemaTooNew }

// SchemaVersion is the store's applied schema version (store.Migrator).
func (s *Store) SchemaVersion(ctx context.Context) (int64, error) {
	return s.e.Migrator().Current(ctx)
}
