//go:build postgres

package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"slices"
	"testing"
)

// migrationStep is one upgrade scenario for a Postgres migration: seed runs
// on a database at the previous version (nil: nothing to seed; the baseline
// starts empty), check right after the migration.
type migrationStep struct {
	name        string
	seed, check func(t *testing.T, pool *sql.DB)
}

// stepwiseMigrations holds every Postgres migration's upgrade scenarios,
// from the baseline (#403) on. A new migration adds its entry next to the
// SQLite one (docs/storage.md "Adding a migration").
var stepwiseMigrations = map[int64][]migrationStep{}

// TestStepwiseMigrations: for every Postgres version v from the baseline, a
// fresh database at the previous version, seeded, migrated to v and checked.
func TestStepwiseMigrations(t *testing.T) {
	versions := Versions()
	for _, v := range slices.Sorted(maps.Keys(stepwiseMigrations)) {
		if !slices.Contains(versions, v) {
			t.Errorf("step-wise entry for unknown migration %d", v)
		}
	}
	if len(versions) == 0 {
		t.Skip("no Postgres baseline yet (#403): step-wise runs from the baseline once it exists")
	}
	ctx := context.Background()
	for i, v := range versions {
		steps := stepwiseMigrations[v]
		if len(steps) == 0 {
			t.Errorf("migration %d has no step-wise entry: add one to stepwiseMigrations (docs/storage.md \"Adding a migration\")", v)
			continue
		}
		var prev int64
		if i > 0 {
			prev = versions[i-1]
		}
		for _, st := range steps {
			t.Run(fmt.Sprintf("%04d/%s", v, st.name), func(t *testing.T) {
				t.Parallel()
				pool := freshDB(t)
				if prev > 0 {
					if err := MigrateTo(ctx, pool, prev); err != nil {
						t.Fatal(err)
					}
				}
				if st.seed != nil {
					st.seed(t, pool)
				}
				if err := MigrateTo(ctx, pool, v); err != nil {
					t.Fatal(err)
				}
				st.check(t, pool)
			})
		}
	}
}
