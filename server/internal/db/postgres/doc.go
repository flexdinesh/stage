// Package postgres is the Postgres storage adapter (docs/storage.md). Its
// code builds only with the postgres build tag: Postgres exists only in
// Docker builds, never in desktop, CLI or release binaries. This file is
// untagged so the package exists in every build (check:storage tests it
// without the tag) while nothing built without the tag depends on it.
//
// Today it holds the migrator only: goose's postgres dialect under its
// session locker over the embedded migrations dir, which stays empty until
// the Postgres baseline (#403, the SQLite head when the adapter lands).
package postgres
