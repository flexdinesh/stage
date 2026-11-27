//go:build postgres

package db

import (
	"database/sql"
	"testing"

	"github.com/flexdinesh/stage/server/internal/db/postgres"
	"github.com/flexdinesh/stage/server/internal/db/postgres/pgtest"
)

func TestLegacyProjectUpgradePostgres(t *testing.T) {
	url := pgtest.DatabaseURL(t, pgtest.CreateDatabase(t, pgtest.Locale))
	pool, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	if err := postgres.MigrateTo(t.Context(), pool, 46); err != nil {
		t.Fatal(err)
	}
	engine, err := postgres.Open(t.Context(), url, postgres.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close() })
	checkLegacyProjectUpgrade(t, pool, engine)
}
