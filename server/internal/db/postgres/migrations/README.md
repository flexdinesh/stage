# Postgres migrations

goose migrations for the Postgres adapter, embedded by `migrate.go` (build tag
`postgres`). Same version numbers as `internal/db/sqlite/migrations`, written
in Postgres-native types.

Empty until the baseline: one migration at the SQLite head when the Postgres
adapter lands (#403). Versions below it are SQLite-only history. Workflow:
[docs/storage.md](../../../../../docs/storage.md#adding-a-migration).
