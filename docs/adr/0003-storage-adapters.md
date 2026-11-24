# ADR 0003: Storage adapters (SQLite and Postgres)

- **Status:** accepted (2026-11-24). The user confirmed the decisions in
  Stage planning task #391 (journal entries 3781, 3821, 3822). This record
  changes no code. Stage tree 201 (tasks #396 to #409,
  [phased tasks](#phased-tasks)) implements it. The engineering contract is
  [storage.md](../storage.md); where this record and that contract differ,
  the contract wins and this record is corrected.
- **Amends:** [ADR 0002](0002-machines-and-workers.md): the main server owns
  one database on either engine, not SQLite only. ADR 0002's "one replica"
  rule is unchanged.

## Context

#52 (`05413f5`) dropped Postgres for a single-binary, single-machine Stage:
pure Go SQLite, every transaction `BEGIN IMMEDIATE`, timestamps and JSON as
TEXT, one server per data dir through `stage.lock`. That fit one process on
one laptop.

Stage is no longer that. A main server can run on a VM or a cluster while
workers on other machines host harnesses
([ADR 0002](0002-machines-and-workers.md)), one main server serves many
machines and harnesses, and work rows already carry a creating user, with
sign-in to follow. A production main server wants a
managed database: backups, point-in-time recovery, monitoring and storage
that are not one file on one volume. That is Postgres.

`db.Store` is already the domain boundary: about 50 packages import it, none
of them see SQL, and only `internal/serve` opens the database. The coupling to
SQLite sits below it (#391 findings):

| Coupling | Where (at planning) |
| --- | --- |
| Every transaction is `BEGIN IMMEDIATE`: one writer at a time, which read-check-write code relies on | `sqlite.go` `inTx` (47 bodies); `trees.go` says so explicitly |
| `stage.lock` is the single-owner guarantee | [runtime.md](../runtime.md#ownership-and-locks) |
| 24 SQLite-only migrations, 76 triggers; journal events are written by triggers | `server/internal/db/migrations` |
| `stage_now()` (a registered Go function), `strftime` defaults, TEXT timestamps compared as text | `sqlite.go`, schema defaults |
| `json_each`, `json_extract`, `json_object`, `json_group_array` | 14 files |
| `VACUUM INTO` backup before migrating | `schema.go` `Backup` |
| SQLite error codes for unique and foreign-key violations | `db.go` |
| `lower()` folds ASCII only; `GLOB`, `INSERT OR IGNORE` | `likeSQL`, search in tasks, journal, threads |

## Decision

1. **Two engines, one per process.** SQLite stays the default for the desktop
   app, the CLI, installs and dev, unchanged. Postgres is the production
   engine for the main server and exists only in Docker builds (build tag
   `postgres`); desktop, CLI and release binaries never contain it.
   `STAGE_DATABASE_URL=postgres://...` selects Postgres; unset means SQLite in
   the data dir.
2. **Engine-native adapters, not a lowest common denominator.** Each adapter
   uses its engine's strengths. Postgres: `timestamptz`, `jsonb`, identity
   columns, `bytea`, row locks and `SKIP LOCKED`, GIN indexes, advisory locks.
   SQLite: TEXT time and JSON, `BEGIN IMMEDIATE` single writer, FTS5.
3. **`db.Store` stays the facade and business rules stay in `internal/db`.**
   Below it: ports in `internal/db/store` (a unit of work with `Read` and
   `Write`, per-aggregate repositories, capability ports), the adapters
   `internal/db/sqlite` and `internal/db/postgres`, and the adapter-agnostic
   contract suite `internal/db/storetest`. Consumers do not change.
4. **Capability ports.** Search, Ownership (Postgres session advisory lock,
   SQLite `stage.lock`) and Backup (SQLite `VACUUM INTO` before migrating;
   Postgres operator-owned, a warning before migrating) are built now. Queue,
   Events and Leases are design-only.
5. **Concurrency per engine.** SQLite keeps one writer at a time. Postgres
   runs READ COMMITTED with explicit row locks or constraints per use case and
   no global lock; the contract suite's concurrency tests prove each use case.
6. **Business-rule triggers move to Go.** Journal events are written by the
   rules layer inside the same write transaction. Legacy columns and
   compatibility triggers stay out of the Postgres baseline when nothing reads
   them (audit: #402).
7. **Migrations.** goose, one embedded directory per engine, the same version
   numbers on both. Postgres starts with one baseline migration whose version
   is the SQLite head when the Postgres adapter lands (24 today; see
   [Planning note](#planning-note)). Expand then contract, shared Go
   backfills, roll forward only, no down migrations.
8. **One Postgres pin.** The latest major, one exact image tag, shared by the
   checks and the compose example (`STAGE_PG_IMAGE` in `mise.toml`).
9. **No CI.** Every gate lives in `mise run check`: `check:go` also vets and
   builds with `-tags postgres`, `check:storage` always runs, and
   `check:postgres` (a throwaway container, fails without Docker) runs when db
   code changed. No releases exist yet, so upgrade coverage is a step-wise
   migration test on both engines.
10. **Data copy is in scope.** `stage db copy` moves a SQLite database into an
    empty Postgres one, with a cutover and rollback runbook.

## Rejected alternatives

| Alternative | Why rejected |
| --- | --- |
| Portable SQL only (the subset both engines share) | Gives up `timestamptz`, `jsonb`, row locks, GIN and FTS5, keeps TEXT time on Postgres, and still needs per-engine locking. The dialect differences only move into string helpers. |
| Repository interfaces imported across the ~50 consumer packages | `db.Store` already is the domain contract. Splitting it widens every change; the ports belong below the facade. |
| One shared migration directory | The schemas use different types, indexes and triggers. Shared SQL forces the lowest common denominator back in. Shared version numbers give the same ordering guarantee. |
| Postgres in desktop, CLI and release binaries | A desktop or CLI install never needs it, and pgx would grow every artifact. The build tag keeps it in Docker only. |
| A global lock on Postgres (one advisory lock per write) | Recreates SQLite's single writer on a server built for concurrency, and hides the races the contract suite must prove absent. |
| Multi-replica now | ADR 0002's single-owner runtime (schedulers, sync, the connections cache, per-machine runners) assumes one process. Events and Leases are designed first (#409); "one replica" stays until they exist. |
| CI | The project has none. `mise run check` is the one gate every agent and the user already run. |

## Consequences

- Every schema change is written twice, once per engine, with the same
  version, and reviewed in two dialects. This is the main ongoing cost.
- A Postgres server takes no automatic backup before migrating; the
  operator owns backups and the server warns.
- Single-owner safety on Postgres rests on an advisory lock instead of a
  file lock.
- Search relevance may differ between engines; filters, ordering and paging
  do not.
- Business rules no longer hide in triggers, so they are tested once, in Go.
- An embedded install is unchanged: same binary, same file, same backups.

## Planning note

#391 fixed the Postgres baseline at version 23, the SQLite head at planning.
Migration 0024 (#180) landed right after. The user then chose the rule
instead of the number: the baseline is the SQLite head when the Postgres
adapter (#403) lands, so migrations added before then need no Postgres port.

## Phased tasks

Stage tree 201 (auto start). Each task ships as its own PR.

| Task | Scope | Needs |
| --- | --- | --- |
| #391 | Planning and decisions | |
| #396 | This ADR and [storage.md](../storage.md) | #391 |
| #397 | `mise` storage checks and the AGENTS.md storage rule (#185) | #391 |
| #398 | Extract ports; SQLite adapter (no behavior change) | #396, #397 |
| #399 | Journal-event triggers into Go | #398 |
| #400 | `storetest` contract suite | #398 |
| #401 | Migration framework (per-engine dirs, step-wise test) | #398 |
| #402 | Legacy column and compatibility-trigger audit | #396 |
| #403 | Postgres adapter | #399, #400, #401, #402 |
| #404 | Search port (GIN trigram / FTS5) | #403 |
| #405 | Configuration and composition (`STAGE_DATABASE_URL`) | #403 |
| #406 | `stage db copy` and cutover runbook | #403, #405 |
| #407 | Docker build tag and Postgres compose example | #405 |
| #408 | Evaluate River for the Queue port ([ADR 0004](0004-queue-port.md)) | #396 |
| #409 | Events and Leases design (multi-replica prep) | #396 |
