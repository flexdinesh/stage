//go:build postgres

package config

import (
	"slices"
	"testing"
)

func TestParseDatabasePostgres(t *testing.T) {
	for _, v := range []string{"postgres://u:" + secret + "@h/db", "postgresql://u:" + secret + "@h:5432/db?sslmode=disable"} {
		d, err := ParseDatabase(v)
		if err != nil || d.Engine() != EnginePostgres || d.URL.Reveal() != v {
			t.Fatalf("%q: %v, %v", v, d.Engine(), err)
		}
	}
	if !slices.Equal(Engines(), []string{EngineSQLite, EnginePostgres}) {
		t.Fatalf("engines %v", Engines())
	}
}
