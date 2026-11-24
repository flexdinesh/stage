# Postgres migrations

goose migrations for the Postgres adapter, embedded by `migrate.go` (build tag
`postgres`). Same version numbers as `internal/db/sqlite/migrations`, written
in Postgres-native types.

The first is the baseline, `0028_baseline.sql` (#403): the SQLite schema at
0028 created natively. Versions below it are SQLite-only history. Workflow:
[docs/storage.md](../../../../../docs/storage.md#adding-a-migration).
