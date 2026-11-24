// Package backfill holds the shared logic of Go data migrations (backfills),
// written once for every engine (docs/storage.md "Migrations").
//
// A backfill is one Step: a version and a name, registered as a goose Go
// migration at that version by every engine adapter. Its logic is a function
// here that takes a small port interface, also declared here, listing the
// queries it needs; each adapter implements that port with its own SQL and
// runs the function inside the migration's transaction. A Step's version has
// no .sql file in any engine dir (TestMigrationVersionParity).
//
// This package holds no SQL and imports no driver or adapter.
package backfill

// Step is one Go data migration, registered at Version on every engine.
type Step struct {
	Version int64
	Name    string
}

// Steps lists every backfill in version order. Each adapter registers a
// runner for exactly these versions (its migrator tests check it).
var Steps = []Step{}
