//go:build postgres

package config

// PostgresBuilt reports whether this binary has the Postgres adapter (the
// postgres build tag: the Docker image only).
const PostgresBuilt = true
