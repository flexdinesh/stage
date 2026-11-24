// Package postgres is the Postgres storage adapter (docs/storage.md): every
// Postgres statement, pgx v5 (database/sql only where goose needs it), the
// ownership advisory lock, SQLSTATE mapping and the Postgres migrations dir.
// Its code builds only with the postgres build tag: Postgres exists only in
// Docker builds, never in desktop, CLI or release binaries. This file is
// untagged so the package exists in every build (check:storage tests it
// without the tag) while nothing built without the tag depends on it.
//
// The engine (Open) runs READ COMMITTED transactions on a pgx pool, with
// row locks taken by the repositories' Lock methods; Write reruns its
// function on a serialization failure or deadlock. The migrations start at
// the baseline (0028, the SQLite head when the adapter landed).
package postgres
