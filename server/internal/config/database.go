package config

import (
	"errors"
	"log/slog"
	"strings"
)

// DatabaseEnv selects the storage engine (docs/storage.md "Engines and
// selection").
const DatabaseEnv = "STAGE_DATABASE_URL"

// Engine names reported by health, status and startup logs.
const (
	EngineSQLite   = "sqlite"
	EnginePostgres = "postgres"
)

// Database is the storage engine STAGE_DATABASE_URL selects: the zero
// value is SQLite at <DataDir>/stage.db.
type Database struct {
	// URL is the Postgres URL; empty for SQLite.
	URL DatabaseURL
}

// Engine is EnginePostgres when a URL is set, else EngineSQLite.
func (d Database) Engine() string {
	if d.URL.s == "" {
		return EngineSQLite
	}
	return EnginePostgres
}

// DatabaseURL holds a database URL, which may carry credentials. It never
// prints its value (fmt, slog); Reveal returns it for the engine to open.
type DatabaseURL struct{ s string }

const redacted = "[redacted]"

// Reveal is the URL itself. Never log it.
func (u DatabaseURL) Reveal() string { return u.s }

func (u DatabaseURL) String() string               { return redacted }
func (u DatabaseURL) GoString() string             { return redacted }
func (u DatabaseURL) LogValue() slog.Value         { return slog.StringValue(redacted) }
func (u DatabaseURL) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// ErrNoPostgres refuses a Postgres URL in a binary built without the
// postgres tag (exit 78).
var ErrNoPostgres = errors.New(DatabaseEnv + " is a Postgres URL but this stage binary has no Postgres support; run the Docker image, or unset " + DatabaseEnv + " for SQLite")

// ParseDatabase reads a STAGE_DATABASE_URL value: "" is SQLite;
// postgres:// or postgresql:// (exactly, lowercase: pgx reads anything else
// as a keyword/value string) is Postgres when PostgresBuilt, else
// ErrNoPostgres; anything else is invalid. Errors never echo the value.
func ParseDatabase(v string) (Database, error) {
	if v == "" {
		return Database{}, nil
	}
	if !strings.HasPrefix(v, "postgres://") && !strings.HasPrefix(v, "postgresql://") {
		return Database{}, errors.New(DatabaseEnv + ": want a postgres:// or postgresql:// URL, or unset for SQLite in the data dir")
	}
	if !PostgresBuilt {
		return Database{}, ErrNoPostgres
	}
	return Database{URL: DatabaseURL{s: v}}, nil
}

// Engines are the engines built into this binary.
func Engines() []string {
	if PostgresBuilt {
		return []string{EngineSQLite, EnginePostgres}
	}
	return []string{EngineSQLite}
}
