//go:build !postgres

package config

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// TestParseDatabaseNoPostgres: without the postgres tag a Postgres URL is
// refused with ErrNoPostgres (exit 78), never echoing the URL.
func TestParseDatabaseNoPostgres(t *testing.T) {
	for _, v := range []string{"postgres://u:" + secret + "@h/db", "postgresql://u:" + secret + "@h:5432/db?sslmode=disable"} {
		_, err := ParseDatabase(v)
		if !errors.Is(err, ErrNoPostgres) {
			t.Fatalf("%q: %v, want ErrNoPostgres", v, err)
		}
		if strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "Docker image") {
			t.Fatalf("%q: error %v", v, err)
		}
		if _, err := LoadFrom(func(k string) string {
			return map[string]string{"HOME": t.TempDir(), DatabaseEnv: v}[k]
		}, nil); !errors.Is(err, ErrNoPostgres) {
			t.Fatalf("LoadFrom %q: %v", v, err)
		}
	}
	if !slices.Equal(Engines(), []string{EngineSQLite}) {
		t.Fatalf("engines %v", Engines())
	}
}
