package db

import (
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db/backfill"
	"github.com/pressly/goose/v3"
)

// Engine migration dirs, relative to this package (go test runs here).
const (
	sqliteMigrationsDir   = "sqlite/migrations"
	postgresMigrationsDir = "postgres/migrations"
)

// sqlMigrations maps each .sql migration version in dir to its name (the
// file name after the version, without .sql). A repeated version fails.
func sqlMigrations(t *testing.T, dir string) map[int64]string {
	t.Helper()
	names, err := fs.Glob(os.DirFS(dir), "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	out := map[int64]string{}
	for _, n := range names {
		v, err := goose.NumericComponent(n)
		if err != nil {
			t.Fatalf("%s/%s: %v", dir, n, err)
		}
		if prev, ok := out[v]; ok {
			t.Fatalf("%s: version %d twice (%s, %s)", dir, v, prev, n)
		}
		_, name, _ := strings.Cut(strings.TrimSuffix(n, ".sql"), "_")
		out[v] = name
	}
	return out
}

// engineVersions are an engine dir's .sql versions plus the shared backfill
// steps (registered as Go migrations on every engine), ascending.
func engineVersions(t *testing.T, dir string) []int64 {
	t.Helper()
	vs := slices.Collect(maps.Keys(sqlMigrations(t, dir)))
	for _, s := range backfill.Steps {
		vs = append(vs, s.Version)
	}
	slices.Sort(vs)
	return vs
}

// TestMigrationVersionParity: both engines know the same versions with the
// same names from the Postgres baseline (its lowest version) up; versions
// below it are SQLite-only history (docs/storage.md "Migrations").
func TestMigrationVersionParity(t *testing.T) {
	lite := sqlMigrations(t, sqliteMigrationsDir)
	pg := sqlMigrations(t, postgresMigrationsDir)

	seen := map[int64]bool{}
	for i, s := range backfill.Steps {
		if s.Name == "" || seen[s.Version] || (i > 0 && s.Version < backfill.Steps[i-1].Version) {
			t.Fatalf("backfill.Steps must be named, unique and ascending: %+v", backfill.Steps)
		}
		seen[s.Version] = true
		if _, ok := lite[s.Version]; ok {
			t.Errorf("backfill %d %s also has a .sql file in %s", s.Version, s.Name, sqliteMigrationsDir)
		}
		if _, ok := pg[s.Version]; ok {
			t.Errorf("backfill %d %s also has a .sql file in %s", s.Version, s.Name, postgresMigrationsDir)
		}
	}

	if len(pg) == 0 {
		// Until the baseline (#403) a Postgres runner would run a backfill on
		// an empty schema, so no backfill may exist yet.
		if len(backfill.Steps) > 0 {
			t.Fatalf("backfill steps %+v before the Postgres baseline: add the baseline (#403) first", backfill.Steps)
		}
		t.Logf("no Postgres baseline yet (#403): %s has no migrations; versions are compared from the baseline once it exists", postgresMigrationsDir)
		return
	}

	baseline := slices.Min(slices.Collect(maps.Keys(pg)))
	if _, ok := lite[baseline]; !ok {
		t.Fatalf("Postgres baseline %d is not a SQLite version", baseline)
	}
	for _, s := range backfill.Steps {
		if s.Version <= baseline {
			t.Errorf("backfill %d %s is not above the Postgres baseline %d", s.Version, s.Name, baseline)
		}
	}
	for v, name := range lite {
		if v < baseline {
			continue
		}
		pgName, ok := pg[v]
		switch {
		case !ok:
			t.Errorf("migration %d %s is in %s but not %s", v, name, sqliteMigrationsDir, postgresMigrationsDir)
		case v > baseline && pgName != name:
			t.Errorf("migration %d is %s in %s but %s in %s", v, name, sqliteMigrationsDir, pgName, postgresMigrationsDir)
		}
	}
	for v, name := range pg {
		if _, ok := lite[v]; !ok {
			t.Errorf("migration %d %s is in %s but not %s", v, name, postgresMigrationsDir, sqliteMigrationsDir)
		}
	}
}
