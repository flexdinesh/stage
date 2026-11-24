# Storage contract

How the Go server stores its data: the engines, the layer below `db.Store`,
and the rules every storage change follows. Decision record:
[ADR 0003](adr/0003-storage-adapters.md). Runtime ownership, startup and exit
codes: [runtime.md](runtime.md). Agents: `AGENTS.md` (storage rule).

This is the target contract. The parts below land task by task (Stage tree
201); a task that lands a part updates its row and the section, and a change
that disagrees with this file updates it in the same PR.

| Part | Today | Task |
| --- | --- | --- |
| [Engines and selection](#engines-and-selection) | SQLite only, `<data dir>/stage.db` | #405 |
| [Layout](#layout-and-ownership) and [ports](#ports) | as described for `internal/db`, `internal/db/store`, `internal/db/sqlite`, `internal/db/dbtest` (#398), `internal/db/storetest` (#400) and `internal/db/backfill` (#401); `internal/db/postgres` holds only the migrator (#401) | Postgres adapter: #403 |
| [Transactions and locking](#transactions-and-locking) | SQLite: as described; the `storetest` concurrency cases exist (#400) | Postgres: #403 |
| [Single owner](#single-owner) | SQLite: `stage.lock`, as described | Postgres: #403 |
| [Encodings](#encodings) and [errors](#errors) | SQLite: as described | Postgres: #403 |
| [Search](#search) | `lower(...) LIKE` (ASCII folding) | #404 |
| [Migrations](#migrations) | per-engine dirs and Go backfills as described (#401): SQLite `server/internal/db/sqlite/migrations` head 28; Postgres `server/internal/db/postgres/migrations` empty until the baseline; no Go backfills yet (#417's 0027 is SQL: SQLite-only history below the baseline); [journal events](#journal-events) written by Go (#399) | Postgres baseline #403 |
| [Backups](#backups) | SQLite: as described | Postgres warning: #403 |
| [Checks](#checks) | the tasks exist (#185); `TestDriverImportBoundary` (#398), `TestMigrationVersionParity` and the SQLite `TestStepwiseMigrations` (#401) run; `storetest` runs on SQLite in `check:storage` and, until #403, on SQLite again in `check:postgres` (#400); the Postgres step-wise and schema parity tests exist and skip until the baseline (#401) | Postgres gates live: #403 |
| [Data copy and cutover](#data-copy-and-cutover) | none | #406 |
| Docker build and compose example | Docker image builds without the tag | #407 |
| [Legacy columns](#legacy-columns-and-compatibility-triggers) | audited (#402); load-bearing trigger logic in Go and those triggers dropped (#399, 0026); every other Drop row dropped from SQLite (#417: 0027 backfill, 0028 contract) | #403 (baseline omits them) |
| [Design only](#design-only-queue-events-leases) | Queue decided: [ADR 0004](adr/0004-queue-port.md) (#408), not built | Events, Leases: #409 |

## Engines and selection

One engine per process, chosen at startup.

| | SQLite | Postgres |
| --- | --- | --- |
| Built into | every build | only builds with `-tags postgres`: the `Dockerfile`. Desktop, CLI, installer and release binaries never contain it (no pgx) |
| Selected by | `STAGE_DATABASE_URL` unset | `STAGE_DATABASE_URL=postgres://...` or `postgresql://...` |
| Database | `<data dir>/stage.db` | the database the URL names |
| Used by | desktop, CLI, installs, the per-user service, dev, the default compose stack, tests | a main server run from the Docker image |

- Any other `STAGE_DATABASE_URL` value is invalid configuration (exit 78).
- A Postgres URL in a binary built without the tag exits 78 with an error
  that says the binary has no Postgres support and to use the Docker image.
- The data dir stays required on both engines: it holds `stage.lock`,
  sockets and logs. On SQLite it also holds `stage.db` and `backups/`.
- Health, version and startup logs report the engine (`sqlite` or
  `postgres`), never the URL or its credentials. No log line prints the URL.
- The desktop app, the installer and `stage service` are SQLite only.
- Tests read `STAGE_TEST_DATABASE_URL` only under `-tags postgres`, set only
  by `check:postgres` (see [Checks](#checks)).

## Layout and ownership

`db.Store` stays the facade: the ~50 packages that import `internal/db` keep
its API, and only `internal/serve` composes an engine.

| Path | Owns | Must not |
| --- | --- | --- |
| `internal/db` | `db.Store`, domain types, app errors (`ErrNotFound`, `ConflictError`, `InvalidError`), business rules (`SwitchInstance`, `ForgetConnection`, tree rules, acceptance, launch claims, journal events) | Import a driver or hold SQL |
| `internal/db/backfill` | Shared Go backfills: the step list (`backfill.Steps`, version and name), each backfill's logic and the query port it declares | Hold SQL; import a driver or an adapter |
| `internal/db/store` | Ports: the unit of work, repositories, capability ports, and the Go types they exchange | Import a driver, `internal/db/sqlite` or `internal/db/postgres` |
| `internal/db/sqlite` | Every SQLite statement, `modernc.org/sqlite`, pragmas, `stage_now`, `VACUUM INTO`, FTS5, SQLite error codes, the SQLite migrations dir | Hold business rules; import pgx |
| `internal/db/postgres` (build tag `postgres`) | Every Postgres statement, pgx v5 (`database/sql` only where goose needs it), the advisory lock, SQLSTATE mapping, the Postgres migrations dir | Hold business rules; import modernc; be needed by code built without the tag |
| `internal/db/storetest` | The adapter-agnostic contract suite: `Run(t, Factory)` runs every case through `db.Store` and the ports on engines from the factory ([Contract suite](#contract-suite)) | Depend on one engine's SQL; import an adapter |
| `internal/db/dbtest` | Test helpers; opens engines and stores (`Engine`, `DB.Reset`): a temp SQLite file, or the `check:postgres` database once #403 wires it | Open a database any other way |

Code outside the Postgres adapter compiles without the tag.
`TestDriverImportBoundary` (`internal/db/boundary_test.go`, run by
`check:storage`) scans every Go file of the server module: a driver import
(`modernc.org/sqlite`, pgx, `lib/pq`) outside its adapter fails, tests
included; a `database/sql` import or a SQL statement in a string literal
outside the adapters fails in non-test files. Tests may still query a
database directly (through the `*sqlite.DB` that `dbtest` and the adapter
return).

## Ports

Port signatures use Go types only: `time.Time`, `[]byte`, typed structs and
slices, `json.RawMessage` for opaque JSON. Never an engine encoding (no
timestamp strings, no JSON text for lists, no `sql.Null*` in a port).

**Unit of work.** `Read(ctx, fn)` and `Write(ctx, fn)` run `fn` with a
transaction handle (`store.Tx`); every repository is reached through it
(`tx.Tasks()`, `tx.Trees()`, ...), so every repository call runs in that unit
of work. Each `db.Store` method that writes runs one `Write`, its rules and
repository calls inside it; nothing writes outside a `Write`. On SQLite,
`Read` runs on the pool, not in a transaction (a transaction would take the
write lock under `_txlock=immediate`): each statement sees the latest
commit. A method that needs one consistent view across reads (journal
paging, completion preview) uses `Write`.

**Repositories**, one per aggregate, in `internal/db/store`: `Tasks` (task
rows and events), `Attempts` (launch attempts and checkpoints, claims, pinned
destinations and inputs, launch bases, handoffs), `Observations` (normalized
execution observations), `Trees` (trees, membership, needs), `Journal`,
`Acceptances`, `Schedules` (and runs), `Projects` (bindings, external
projects, imports), `Connections` (harness connections, T3 and OpenCode
settings, shared paths), `Machines` (pairing codes, machine tokens),
`Notifications`, `Sessions` (session identities), `Threads` (the T3 mirror,
watcher state, last words), `Stats` (read-only counts), `Attachments`,
`Users`, `Preferences`, `Push` (VAPID keys, subscriptions) and `Snippets`.
Repository methods are named queries: they hold no rules, return engine
errors unchanged and `store.ErrNotFound` for a missing row; `internal/db`
classifies errors through the `ErrorClassifier`.

**Capability ports**

| Port | SQLite | Postgres |
| --- | --- | --- |
| Ownership | `flock` on `<data dir>/stage.lock`, taken by `internal/serve` with the data dir before the engine opens ([runtime.md](runtime.md#ownership-and-locks)); the port's `Acquire` holds nothing more | session advisory lock on a dedicated connection ([Single owner](#single-owner)) |
| Backup | `VACUUM INTO` before migrating ([Backups](#backups)) | none: logs a warning before migrating |
| Migrator | goose `sqlite3` dialect over the SQLite dir | goose `postgres` dialect with its session locker over the Postgres dir |
| ErrorClassifier | SQLite extended codes ([Errors](#errors)) | SQLSTATE |
| Search | FTS5 trigram, `LIKE` for short queries ([Search](#search)) | `pg_trgm` GIN with `ILIKE` |

Repositories have one implementation per engine with engine-native SQL. Where
they differ in more than syntax:

| Repository | SQLite | Postgres |
| --- | --- | --- |
| tasks | claims rely on the single writer | the claim locks the task row (`FOR UPDATE`) |
| connections | single writer keeps one active connection per machine and kind | partial unique index plus a row lock on the machine's connections |
| journal, acceptance | `requestId` idempotency under the single writer | unique `(owner, request_id)`, `ON CONFLICT DO NOTHING`, then re-read and compare |
| notifications | `INSERT OR IGNORE` | `INSERT ... ON CONFLICT DO NOTHING` |
| attachments, machine tokens | `BLOB` | `bytea` |

The `harness_connections` snapshot cache stays in `db.Store` and is dropped by
every write to that table, as today. It assumes one process
([Design only](#design-only-queue-events-leases)).

## Journal events

Rows derived from a change are written by Go in `internal/db`
(`journal_events.go`), in the same `Write` as the change, so they commit and
roll back with it on either engine. SQLite triggers wrote them until
migration 0026 (#399).

| Cause (helper) | Derived rows |
| --- | --- |
| Task event (`addTaskEvent`) | event `task_created` (no from state), `task_state_changed` (detail plus ` (from -> to)`) or `task_event` (same state), on the task and its tree, at the task event's time, with the task's session and launch attempt; the task records work when launched, adopted or entering launching, running, needs input, done, settled, failed or interrupted, and a task with recorded work protects its tree |
| Note (`insertNote`) | protects its task and tree; the tree's `updated_at` becomes the note's time |
| Tree created, config, status (`insertTree`, `updateTree`, `setTreeStatus`, `archiveDissolved`) | `tree_created` (title), `tree_config_changed` (`Title: a -> b; start mode: x -> y`, only when either changed), `tree_status_changed` (`old -> new`, only on a change) |
| Membership (`setMembership`, `detachMembers`) | `task_moved` (task title, from and to tree) in the old and the new tree; both trees' membership revision + 1, `updated_at` now, recorded work carried over; an emptied old tree is archived (closed now), with its status event |
| Needs (`addNeed`, `keepNeeds`, `inheritNeeds`, `dropNeeds`, `detachMembers`) | `needs_changed` (`Added` or `Removed need #N`) in the dependent's tree, by task then need; none for a standalone task |
| Acceptance (`insertAcceptance`) | `task_acceptances.source_session_id` = the task's session |
| Handoff (`recordHandoff`) | source session: the evidence's session, else the legacy thread id under the local machine's active T3, else none (the trigger aborted the write) |
| New T3 mirror row (`mirrorThread`) | `threads.stage_project_id`: the session's task's project, else the project of the only active binding of its harness project on its connection |

- **Attribution:** derived events keep the triggers' attribution, the
  column defaults: the default user and no source machine
  ([architecture.md](architecture.md#users)).
- **Changes from the triggers:** task events no longer copy the legacy
  `tasks.t3_thread_id` into `source_thread_id` (the API's `sourceThreadId`
  reads the T3 session's id); deleting an unstarted tree writes no
  `needs_changed` rows into the tree it deletes; no direct `INSERT` of a task
  into a tree exists, so `journal_member_created` has no replacement. Events
  from one bulk change are written in the orders above.
- **Tests:** `TestJournalEventRowsExact` pins every row;
  `TestDerivedWritesOnceAndRollBack` proves each helper writes once and rolls
  back with its `Write`; `TestDerivedWritesBoundary` fails when `internal/db`
  calls a raw repository write outside its helper. They read SQL or call the
  helpers, so they stay in `internal/db`, not `storetest`.

## Transactions and locking

### SQLite

- Every `Write` is `BEGIN IMMEDIATE` (`_txlock=immediate`): it takes the
  database write lock at its first statement, so writes run one at a time.
  Read-check-write code is atomic because of it (tree rules say so).
- WAL, `synchronous(NORMAL)`, `foreign_keys(1)`, `busy_timeout` 10 s: a writer
  that waits longer gets `SQLITE_BUSY`, an internal error.
- `Write` rolls back on error or panic, so the lock is never left held.
- `Read` takes no write lock and never blocks a writer (WAL).

### Postgres

- Every transaction is READ COMMITTED. There is no global lock: no table
  lock, no advisory lock per write.
- Each use case that reads, checks and writes takes the lock or constraint
  that makes it atomic, below. A new use case of this kind adds its row to
  the table and a concurrency case to `storetest`.
- **Lock order** (prevents deadlocks): machine, connection, tree, task
  (ascending id), schedule. A use case that needs several takes them in this
  order.
- `fn` has no effects outside the transaction (external effects already go
  through launch checkpoints), so the adapter may rerun it after
  `40001`/`40P01`. #403 fixes whether it retries and how often; a retried
  `fn` that still fails returns the error.
- `Read` is a read-only READ COMMITTED transaction: each statement sees
  committed data. A use case that needs one snapshot across statements says
  so and #403 gives it `REPEATABLE READ READ ONLY`.

| Use case | Store entry (today) | Invariant | Postgres | `storetest` case |
| --- | --- | --- | --- | --- |
| Launch claim | `TransitionTask` (begin attempt) | one attempt begins per task and attempt number; current acceptance checked and inputs pinned atomically | task row `FOR UPDATE`; unique `(task_id, attempt)` | `ConcurrentLaunchClaim` |
| Release and schedule runner picks | `LaunchableForConnection`, `DueForConnection` | each item launches once | the launch claim's row lock decides; the runners stay reconcilers under Leases (#409), not Queue jobs ([ADR 0004](adr/0004-queue-port.md#loops)) | `ConcurrentLaunchClaim` (the claim decides) |
| Acceptance | `ChangeAcceptance` | one revision sequence per task; idempotent `requestId` | task row `FOR UPDATE`; unique request key with `ON CONFLICT` | `ConcurrentAcceptance`, `ConcurrentAcceptanceRequestID` |
| Tree mutations | `CreateTree`, `SetNeeds`, membership changes | no cycles; a task in at most one tree; needs within the tree | tree rows, then task rows, `FOR UPDATE` before any need check | `ConcurrentNeedsSerialize`, `ConcurrentTreeMembership`, `ConcurrentNeedsAndRemoval`, `JournalMoveAndAppendSerialize` |
| Schedule fire | `FireSchedule` | one spawn per fire time | schedule row `FOR UPDATE`; next fire advanced in the same transaction | `ConcurrentScheduleFire` |
| Connection switch | `SwitchInstance`, `ForgetConnection` | at most one active connection per machine and kind; identity unique per machine | the machine's connection rows `FOR UPDATE`; partial unique indexes | `ConcurrentConnectionSwitch` |
| Journal append | `AppendNote` | `requestId` idempotent per owner; same key with other content is 409 | unique `(owner, request_id)`; `ON CONFLICT DO NOTHING`, re-read, compare | `JournalConcurrentRetryAndPagination`, `ConcurrentNoteRequestIDConflict` |
| Pairing redeem | `RedeemPairing` | a code redeems once, before expiry | one `UPDATE ... WHERE` unused and unexpired `... RETURNING` | `RedeemPairingConcurrentSingleWinner` |
| Launch checkpoint | `SaveCheckpoint` | checkpoints of one attempt apply in order | attempt row `FOR UPDATE` | none yet |
| Project import | `ImportWorkspaces` | one project per workspace path per machine | connection row `FOR UPDATE`; unique binding indexes | none yet |

The table is indicative until #403 lands; the `storetest` concurrency cases
are the proof, and #403 corrects the table to what they prove. On SQLite the
single writer keeps every invariant, so these cases pass there by
construction; they bite on an engine without a global lock.

## Single owner

One server owns one database, on either engine. A second owner would run
duplicate schedulers, sync and launches.

- **SQLite:** the data-dir `stage.lock` ([runtime.md](runtime.md#ownership-and-locks)).
  `stage.db` must sit on a local filesystem.
- **Postgres:** a session advisory lock (`pg_try_advisory_lock` on one fixed
  64-bit key defined once in the adapter) on a dedicated connection held for
  the process lifetime, taken right after the data-dir lock and before
  migrating. Held by another session: exit 75, like a held data dir, with the
  holder's `application_name` (`stage <instanceId>`). Losing that connection
  loses ownership: the server stops (exit 1) rather than keep running
  unowned.
- Two servers with different data dirs on one Postgres database: the second
  exits 75. "One replica" ([ADR 0002](adr/0002-machines-and-workers.md),
  [runtime.md](runtime.md#remote-main-server-and-workers)) holds on both
  engines.

## Encodings

| Value | SQLite | Postgres |
| --- | --- | --- |
| Timestamps | TEXT, fixed-width UTC `2006-01-02T15:04:05.000000Z` (lexical order is time order); Go clock via `stage_now()`, schema defaults `strftime` (ms) | `timestamptz`; Go-supplied values, defaults `now()` |
| Precision | microseconds | microseconds |
| JSON (queried) | TEXT, read with `json_each` / `json_extract` | `jsonb` |
| JSON (evidence: bytes must survive) | TEXT | `text`, never `jsonb` (it reorders keys and drops whitespace) |
| Booleans | INTEGER 0/1 | `boolean` |
| Integer IDs | `INTEGER PRIMARY KEY AUTOINCREMENT` (never reused) | `bigint GENERATED BY DEFAULT AS IDENTITY` (BY DEFAULT so the copy keeps IDs) |
| Seeded IDs | default user 1, local machine 1 | the same rows and IDs |
| Go-generated UUIDs, request keys | TEXT | `text` (`uuid` only for a column that holds nothing but Go UUIDs) |
| Blobs | `BLOB` (attachments up to 10 MiB, 32-byte secret hashes) | `bytea` with the same `CHECK`s |

- The port boundary truncates `time.Time` to microseconds in UTC, so a
  value round-trips equal on both engines.
- Which JSON columns are evidence and which are queried is decided per column
  under [JSON columns](#json-columns) (#402; #403 may amend it there).
- Business rules never depend on the SQL clock; time comes from Go.

## Errors

The adapter maps engine errors to `db` errors; rules map those to
`ConflictError` or `InvalidError` with their messages, as today.

| Condition | SQLite | Postgres | Result |
| --- | --- | --- | --- |
| Unique violation | `SQLITE_CONSTRAINT_UNIQUE`, `SQLITE_CONSTRAINT_PRIMARYKEY` | `23505` | unique violation (the rule decides the 409) |
| Foreign-key violation | `SQLITE_CONSTRAINT_FOREIGNKEY` | `23503` | foreign-key violation (the rule decides 404 or 409) |
| No row | `sql.ErrNoRows` | `pgx.ErrNoRows` | `ErrNotFound` |
| Lock wait exhausted | `SQLITE_BUSY` | | internal error |
| Serialization failure, deadlock | | `40001`, `40P01` | retry policy ([Postgres](#postgres)), then internal error |
| Context ended | context error | `57014` or context error | the context's error |
| Anything else | | | internal error, unchanged |

## Search

Three searches: tasks (title, prompt), journal (text), threads (title).

- **Fixed on both engines:** the filters, the ordering, paging, substring
  matching, LIKE metacharacters (`%`, `_`, `\`) matched literally, and
  case-insensitive matching.
- **May differ:** relevance, if a search ever ranks (ties always break by
  id, so ordering stays deterministic), and case folding of non-ASCII text at
  the edges of Unicode.
- **SQLite:** FTS5 with the trigram tokenizer (`case_sensitive 0`) over
  external-content tables; queries shorter than 3 characters fall back to
  `LIKE`. #404 decides whether triggers or repositories keep the FTS tables in
  step and records it here.
- **Postgres:** `pg_trgm` GIN indexes on the searched columns, `ILIKE`
  substring matching.
- Contract tests: substring hits, non-ASCII case-insensitivity, literal
  metacharacters, short queries, paging.

## Migrations

- **Tool:** goose, migrations embedded per engine:
  `internal/db/sqlite/migrations` and `internal/db/postgres/migrations`, both
  tracked in `goose_db_version`.
- **Shared versions:** a schema change adds `NNNN_name.sql` (or a Go
  migration) with the same version and name in both dirs.
  `TestMigrationVersionParity` fails otherwise. Until the Postgres baseline
  lands, a migration goes in the SQLite dir only.
- **Postgres baseline:** one migration at the SQLite head when the Postgres
  adapter lands (#403; 28 today). It creates the current schema natively
  (`timestamptz`, `jsonb`, `bytea`, identity, partial and expression indexes,
  `CHECK`s) without the legacy columns and compatibility triggers
  [#402](#legacy-columns-and-compatibility-triggers) drops; #417 drops them
  from SQLite first, so the two heads match. Versions below it
  are SQLite-only history; the parity test compares versions from the
  baseline up.
- **No business rules in triggers.** Journal events are written by Go in the
  same `Write` as their cause ([Journal events](#journal-events)). A trigger may only maintain
  engine-internal structures (for example FTS tables), on one engine.
- **Expand, then contract.** Add tables and nullable or defaulted columns;
  backfill; move reads and writes; drop the old shape in a later migration.
  One migration never both drops and replaces a shape that running code reads.
- **Backfills in Go.** Data backfills are goose Go migrations registered with
  the same version on both engines, with no `.sql` file at that version. The
  logic lives once in `internal/db/backfill` (adapters cannot import
  `internal/db`, whose tests import the SQLite adapter): a `backfill.Step`
  (version, name) plus a function over a small query port declared next to
  it. Each adapter implements the port with its SQL and registers a runner
  for exactly `backfill.Steps` in its `goMigrations` (a test per adapter
  checks it); goose runs it in the migration transaction. No backfill before
  the Postgres baseline: `TestMigrationVersionParity` refuses one, and every
  backfill is above the baseline. A backfill below it is SQLite-only history
  and is plain SQL (0027, the #417 legacy backfill).
- **Roll forward only.** No down migrations. A bad migration is fixed by a
  new one. A downgrade restores a backup.
- **Version guard,** per engine with the same numbers: a database newer than
  the binary exits 78 and writes nothing
  ([runtime.md](runtime.md#schema-and-backups)).
- **Locking:** Postgres migrations run under goose's session locker
  (`lock.NewPostgresSessionLocker`: an advisory lock on goose's default key,
  held on a dedicated connection while migrating; the ownership lock uses a
  different key), after the ownership lock. SQLite migrations run under the
  data-dir lock.

### Tests

- `TestMigrationVersionParity` (`internal/db`, `check:storage`): reads both
  engine dirs and `backfill.Steps`; versions and names match from the
  Postgres baseline (its lowest version) up. An empty Postgres dir passes.
- `TestStepwiseMigrations`: for every version `v`, `UpTo(v-1)`, seed
  representative rows, `Up(v)`, assert the data. SQLite from version 1
  (`internal/db/stepwise_migration_test.go`, `check:storage`); Postgres from
  the baseline (`internal/db/postgres/stepwise_test.go`, `check:postgres`;
  skips until the baseline). Each engine's `stepwiseMigrations` registry
  holds one or more scenarios per version, a seed and a check; a version
  without an entry fails. Checks query that version's schema directly; where
  a migration's result is also read through `db.Store`, a separate test
  reuses the seed and reads it at the head. No releases exist yet, so this
  is the upgrade coverage.
- `TestSchemaParity` (`internal/db/postgres`, `check:postgres`): both engines
  migrated to their heads hold the same tables, columns, nullability,
  primary, foreign and unique keys, with types compared by class (`int`:
  SQLite INTEGER, Postgres integers and boolean; `text`: SQLite TEXT,
  Postgres text, timestamptz, jsonb; `bytes`; `real`). Partial and
  expression indexes are engine-native and not compared. Engine-only objects
  go in its `parityAllowlist`, each with why; an entry present on neither
  engine fails. Until the baseline only the SQLite introspection runs;
  `TestSchemaParityCompare` proves the comparator on hand-written schemas.
- Postgres tests each create and drop their own database on the
  `check:postgres` container; without `STAGE_TEST_DATABASE_URL` they fail.

### Adding a migration

1. Pick the next version `N`: the highest in either dir or `backfill.Steps`,
   plus one.
2. Schema change: write `N_name.sql` (`-- +goose Up` only) in
   `internal/db/sqlite/migrations` and, once the Postgres baseline exists,
   the same name in `internal/db/postgres/migrations`, each in its engine's
   native types.
3. Backfill (after the baseline only): add `backfill.Step{N, "name"}`, its
   function and query port in `internal/db/backfill`, and a runner for `N`
   in each adapter's `goMigrations` implementing that port. No `.sql` file
   at `N`.
4. Keep it expand-only unless it is the contract step of an earlier expand.
5. Add `N` to each engine's `stepwiseMigrations`: seed the tables it touches
   at `N-1`, check the data and constraints after `N`. Reads of the result
   through `db.Store` go in a separate test that reuses the seed.
6. Engine-only tables or columns (for example an FTS table on SQLite): add
   them to `parityAllowlist` with why.
7. Never edit an applied migration; fix forward with a new one.
8. Run `mise run check` (`check:postgres` runs because db code changed).
9. Update this file when the change alters a rule here.

## Backups

- **SQLite (automatic):** before migrating a non-empty database, `VACUUM INTO
  <data dir>/backups/stage-schema<N>-<UTC time>.db` (0600 in a 0700 dir), keep
  the newest 3; a failed backup exits 1 with the database untouched
  ([runtime.md](runtime.md#schema-and-backups)).
- **Postgres (operator-owned):** the server takes no backup. Before applying
  pending migrations it logs a warning naming the current and target
  versions and that no backup was taken, then migrates. Backups, snapshots and
  point-in-time recovery are the operator's (managed service, `pg_dump`).

## Checks

All gates run in `mise run check`; there is no CI.

| Task | Runs | Covers |
| --- | --- | --- |
| `check:go` | always | `go vet` (plain, `smoke`, `postgres` tags), `CGO_ENABLED=0 go build -tags postgres` of the server, `go test ./...` (SQLite) |
| `check:storage` (`scripts/check-storage.sh`) | always | the gate tests by name under `server/internal/db` (`TestMigrationVersionParity`, `TestStepwiseMigrations`, `TestDriverImportBoundary`) and the `internal/db/{store,sqlite,postgres,storetest}` packages; a gate or package not written yet prints `not present yet` and passes |
| `check:postgres` (`scripts/check-postgres.sh`) | from `check` only when the branch changes `server/internal/db/**`, `docs/storage.md`, `server/go.mod` or `Dockerfile` vs the merge-base with `origin/main` (committed, uncommitted or untracked); always when run directly | a throwaway `stage-pgtest-<random>` container (`STAGE_PG_IMAGE`, random `127.0.0.1` port, tmpfs, always removed), `go test -tags postgres ./internal/db/... ./internal/api/...` with `STAGE_TEST_DATABASE_URL`: migrate up, schema parity, step-wise from the baseline, `storetest`, db and api suites. Without Docker it fails, never skips |

- New gate tests use the names above; `check:storage` finds them by name.
- `STAGE_PG_IMAGE` in `mise.toml` is the one Postgres pin (latest major, one
  exact tag), shared with the compose example.
- Postgres tests use only the `stage-pgtest-*` containers; never another
  database, the prod stack or the default data dir.
- `storetest` runs on SQLite in `check:storage` and on Postgres in
  `check:postgres` ([Contract suite](#contract-suite)).

### Contract suite

`internal/db/storetest` holds every test that needs only `db.Store` and the
ports: business rules, port contracts (unit of work, error classification,
ordering, microsecond time, JSON evidence bytes, blobs, Unicode) and the
concurrency cases of [Transactions and locking](#transactions-and-locking).
Tests that need SQL, a migration step or `internal/db` internals stay in
`internal/db` (or the adapter's package).

- **Shape:** `storetest.Run(t, Factory)` runs each registered case as a
  subtest, `TestContract/<Name>`, in name order. `Factory` opens a fresh,
  migrated, empty engine per call (closed when the test ends); engines from
  separate calls share nothing.
- **Engine:** `TestContract` (`storetest_test.go`) runs it with
  `dbtest.Engine`: a temp SQLite file. Under `-tags postgres` with
  `STAGE_TEST_DATABASE_URL` set, `dbtest/engine_postgres.go` only logs that
  the Postgres adapter is not present yet and stays on SQLite. #403 replaces
  that file with an `Engine` that opens a fresh database or schema on the
  URL, so `check:postgres` runs the suite on Postgres with no script change.
- **Run one case:** `go test ./internal/db/storetest -run 'TestContract/<Name>$'`.

Adding a case:

1. Put it in the `storetest` file for its aggregate (`concurrency.go` for a
   race, `ports.go` for a port contract), as
   `func caseName(t *testing.T, open Factory)`, and add
   `register("Name", caseName)` to that file's `init`. Names are unique
   (`register` panics on a duplicate).
2. Open engines with `open(t)` or a Store with `setup(t, open)`, and reach data
   only through `db.Store`, `store.Tx` repositories and the engine's capability
   ports. No SQL, no driver, no adapter import: `storetest` is non-test code
   to `TestDriverImportBoundary`.
3. A concurrency case releases its racers together with `race(n, fn)`, then
   asserts the invariant, not an ordering. A new read-check-write use case
   adds its row to the [Transactions and locking](#transactions-and-locking)
   table, naming the case.
4. Assert engine-neutral facts: compare times with `Equal` at microsecond
   precision, don't depend on collation beyond lowercase ASCII, and give
   ordering a documented tiebreak.
5. Run `mise run check` (`check:storage`; `check:postgres` runs because db
   code changed).

## Data copy and cutover

`stage db copy --from <sqlite path> --to <postgres url>` moves a SQLite
database into Postgres. It needs a binary with Postgres support, so it runs
from the Docker image.

- **Offline:** refused while the SQLite data dir lock is held.
- **Versions:** the source must be at a version this binary knows, at or
  above the Postgres baseline. The target holds no Stage rows; an empty target
  is migrated to the source's version, a target at another version is
  refused.
- **Copy:** every table in foreign-key order, IDs preserved, encodings
  converted (TEXT time to `timestamptz`, TEXT JSON to `jsonb` or `text` per
  [Encodings](#encodings), `BLOB` to `bytea`), identity sequences reset past
  the highest ID. No legacy transforms: the source is at or above the
  baseline, where #417 has already dropped the
  [legacy columns](#legacy-columns-and-compatibility-triggers).
- **Verify:** per-table row counts and checksums; any mismatch exits
  non-zero.

### Cutover

1. Stop the server (Kubernetes: scale to 0; compose: stop the service).
2. Back up `stage.db` while stopped (copy the file and any `stage.db-wal`).
3. Start the new image once on SQLite if the source is below the Postgres
   baseline (it migrates, with its own backup), then stop it.
4. `stage db copy --from <data dir>/stage.db --to <url>`; require exit 0.
5. Start with `STAGE_DATABASE_URL=<url>`; check `/api/health` reports
   `postgres` and the expected schema.

**Rollback:** stop, unset `STAGE_DATABASE_URL`, start. The SQLite file is
untouched by the copy. Writes made on Postgres after the cutover are not
copied back.

## Legacy columns and compatibility triggers

The #402 audit of the SQLite schema at head 25. It covers every legacy or
retired column and every compatibility trigger. Business-rule triggers
(`task_event_journal`, `journal_*`) are not listed: #399 moved them to Go
([Journal events](#journal-events)).

**Policy (user decision on #402; no users yet).** Anything whose only role is
legacy compatibility is dropped from **both** engines:

- **Dead:** nothing reads or writes it.
- **Fallback-only:** it is read only when the neutral column is empty on an
  old row. The neutral column is backfilled first, then the column goes.

Raw retired values are not preserved. A historical row keeps its meaning
through the neutral column: a session identity, `execution_config` or a
binding. Journal note and event text, accepted snapshot evidence and
`acceptance_requests` bytes are untouched. A legacy id the backfill cannot
resolve becomes NULL.

**Who does what:**

- The Postgres baseline (#403) omits every **Drop** row.
- SQLite dropped them in #417 (expand, backfill, contract) before #403, so
  the baseline equals the SQLite head and
  [Data copy](#data-copy-and-cutover) needs no legacy transforms. Migration
  0027 backfills the neutral columns in SQL (SQLite-only history below the
  baseline, so not a `backfill.Steps` Go migration); 0028 drops the columns,
  indexes, `question_responses` and triggers, and rebuilds `tasks`,
  `schedules`, `projects` and `threads` (`connection_id` on `tasks` and
  `schedules` is now a real foreign key).
- Load-bearing trigger logic moved to Go in #399, and migration 0026 dropped
  those triggers. #417 dropped the rest.

**Status (#417): every Drop row below is done.** The "Readers and writers"
column is the audit at head 25. How 0027 applied the backfills:

- A legacy id resolves to a connection: the row's own (a task's
  `connection_id`; a notification's task's), else an existing identity with
  that external id on a T3 connection (the local active T3 first, so a
  retired connection's id keeps its connection), else the local active T3
  (the old trigger rule), else none (NULL).
- `tasks.t3_thread_id`: "verify every row has `session_id`" became a
  backfill. A legacy id whose session another task already holds stays NULL
  (`task_session_identity` is unique).
- `execution_config` is filled only for T3-connection rows where it was NULL,
  with `store.LegacyT3Config`'s shape (the model key keeps Go's JSON
  escaping). New writes translate the legacy inputs there (create, update as a
  partial merge, duplicate, schedule spawn).

**Settled before this audit:** 0025 dropped `threads_parent_session_id_*`.

Readers and writers below are live code paths reachable from the server, API,
MCP or CLI. Test-only callers are noted where they are the only ones.

### Columns

| Item | Readers and writers today | Decision | Work first (#417 unless named) |
| --- | --- | --- | --- |
| `projects.t3_project_id` (NOT NULL UNIQUE) | Write-only inert marker (`"stage:"+uuid`, `projects.go`, `import.go`); no reader | Dropped (0028) | Remove the marker writes |
| `projects.workspace_root`, index `projects_workspace_root_idx` | Write-only `''`; no reader (0008 copied paths into bindings) | Dropped (0028) | Remove the writes |
| `projects.source` (`'app'`/`'t3'`) | Write-only `'app'`; no reader | Dropped (0028) | Remove the writes |
| `projects.t3_deleted_at` | None (read once by 0008) | Dropped (0028) | None |
| `projects.origin`, `imported_connection_id`, `imported_at`, `archive_owner` | Project import and archive-follow (`import.go`); API `origin`, `importedAt` | Keep | None |
| `tasks.t3_thread_id` | Written with `session_id` on T3 launch and adopt (`execution.go` `FinishAttempt`, `adopt.go`); read as a fallback for `externalSessionId`, as the "has started" marker (`trees.go` `unstartedSQL`, `acceptance.go`, the recorded-work rule in `journal_events.go`), and as evidence `threadId`; journal events no longer copy it (#399) | Fallback-only: Dropped (0027, 0028) | Verify every row has `session_id`; markers and evidence `threadId` read `session_id` |
| `tasks.origin_thread_id`, index `tasks_origin_thread_idx` | Insert column, but no caller sets `NewTask.OriginThreadID` (API v6 rejects it); read into a `json:"-"` field nothing uses | Dropped (0028) | `origin_session_id` already backfilled (0008) |
| `tasks.after_thread_id`, `after_task_id`, constraint `tasks_one_wait` | None; 0003 nulled both and dropped their indexes | Dropped (0028) | None (`tasks_start_pair` stays) |
| `tasks.launch_reuse` | Always 0 outside tests (`ReuseAttempt` is test-only, `ReusesLaunch` has no caller); 0009 moved reuse into `execution_attempts` | Dropped (0028) | Remove the reads and `ReuseAttempt` |
| `tasks` and `schedules`: `provider_instance_id`, `model`, `model_options`, `runtime_mode`, `interaction_mode` | Primary storage for the legacy T3 model inputs: stored raw on create and update, copied by duplicate and schedule spawn, translated only on read (`LegacyModel`/`LegacyConfig` in `api/shapes.go`, `sync/service.go`) | Legacy storage: Dropped (0027, 0028) | Translate legacy inputs to `execution_config` on write; backfill `execution_config` where NULL. API inputs may stay, translated |
| `tasks.launch_request_id`, `settled_at`, `handoff` | Launch intents, task API | Keep | None |
| `schedules.origin_thread_id` | Insert column, never set by a caller; `COALESCE` puts it first, into a `json:"-"` field | Dropped (0028) | `origin_session_id` already backfilled (0008) |
| `threads.t3_thread_id`, `t3_project_id`, `status`, `parent_thread_id` | The T3 mirror's key and live fields, written by `PutObservation` and read by the session API, push and binding lookup | Keep | None |
| `threads.provider_instance_id`, `creation_source`, `run_count` | No writer; old values still appear in session JSON | Dropped (0028) | Remove them from `threadSelect` and the session API shape |
| `threads.latest_run_status` | None | Dropped (0028) | None |
| `threads.recent_runs` | No writer; read only by `ThreadRuns`, which has no caller | Dropped (0028) | Remove `ThreadRuns` |
| `threads.pending_question_count` | No writer (`SetPendingQuestionCount` has no caller); read in `min()` into `json:"-"` fields | Dropped (0028) | Remove it from the `min()` reads |
| `threads.parent_session_id` | Written by `PutObservation` (0025); never read | Dropped (0028) | Remove the write (`parent_thread_id` stays the mirror's parent) |
| `threads.link`, `last_word_item_id` | Written by `PutObservation`; read only as acceptance-preview fallbacks that the observation overwrites | Fallback-only: Dropped (0027, 0028) | Evidence reads `execution_observations` only |
| `threads.pull_requests` | No writer (PRs arrive as observation artifacts); old values feed task `artifactLinks` and the acceptance-preview fallback | Fallback-only: Dropped (0027, 0028) | Old PR links leave `artifactLinks`; accepted evidence keeps its copy |
| Other `threads` columns (`title`, `execution_state`, `settled*`, `branch`, `worktree_path`, run ids and times, `last_word_*` except `item_id`, `model`/modes, `observed_state`, `session_id`, `stage_project_id`, ...) | `PutObservation`, `SetObservedState`; session API, push, acceptance | Keep | None |
| `threads` indexes `threads_status_idx`, `threads_updated_at_idx`, `threads_root_created_idx`, `threads_t3_project_id_idx` | Used only by `ListThreads` and `Stats`, which have no caller since `/api/threads` and `/api/stats` were removed | Dropped (0028) | Remove those store functions |
| `notifications.t3_thread_id` | Written beside `session_id` (`notify.go`); read as a fallback for the key and `externalSessionId` | Fallback-only: Dropped (0027, 0028) | Backfill `session_id`; the key reads `session_id` only |
| `task_handoffs.source_thread_id` (NOT NULL), `task_launch_bases.source_thread_id` | Written beside `source_session_id` (`sync/handoff.go`, `sync/service.go`); read for API `sourceThreadId` | Fallback-only: Dropped (0027, 0028) | Backfill `source_session_id`; `sourceThreadId` derives from it (T3 sessions) or leaves the API |
| `journal_entries.source_thread_id` | Old task events only (`task_event_journal` copied it until 0026; Go events write `source_session_id` only); the note input is unreachable; `COALESCE` puts it first for API `sourceThreadId` | Fallback-only: Dropped (0027, 0028) | Backfill `source_session_id`; derive `sourceThreadId`; note and event `text` unchanged |
| `task_acceptances.source_session_id` | Set by Go on insert from the task's session (#399; `acceptance_session_insert` until 0026); read as a fallback when evidence has no `session` | Keep | None |
| `question_responses` (table) | None (read once by 0008) | Dropped (0028) | None |
| `t3_connection`, `opencode_settings` (all columns) | Live harness setup | Keep | None |

### Triggers

The legacy-id triggers each fill a `*_session_id` column from a bare
`*_thread_id`, under the local machine's active T3. With no active local T3
their `session_identities` insert fails on NOT NULL `connection_id` and aborts
the write (the 0025 failure).

| Trigger | Today | Decision | Work first |
| --- | --- | --- | --- |
| `threads_session_id_insert/update` | Redundant: `PutObservation` always sets `session_id` | Dropped (0028) | None |
| `tasks_session_id_insert/update` (no guard) | Redundant live: launch and adopt set `session_id` with the same identity; only test-only `Transition{ThreadID}` relies on it | Dropped (0028) | Tests set `session_id` |
| `tasks_origin_session_id_*`, `schedules_origin_session_id_*` (no guard) | Fire only for `origin_thread_id`, which no live caller sets | Dropped (0028) | Drop with the columns |
| `notifications_session_id_*` | Redundant: Go resolves the session first | Dropped (0028) | None |
| `task_launch_bases_source_session_id_*` | Redundant for live writes | Dropped (0028) | None |
| `task_handoffs_source_session_id_*` | Was load-bearing for legacy evidence with `threadId` and no `session` (aborted the launch without a local T3) | Dropped (0026) | Done in #399: `recordHandoff` resolves the source session (evidence, else the legacy T3 rule, else none) |
| `journal_entries_source_session_id_*` | Redundant: notes resolve in Go; Go task events set `source_session_id` and no longer set `source_thread_id` | Dropped (0028) | None |
| `thread_project_insert` | Was load-bearing: the only writer of `threads.stage_project_id` (project activity reads it) | Dropped (0026) | Done in #399: `mirrorThread` sets `stage_project_id` (task's project, else the single active binding) |
| `acceptance_session_insert` | Was load-bearing for `task_acceptances.source_session_id` | Dropped (0026) | Done in #399: `insertAcceptance` sets it |
| `tasks_connection_insert/update`, `schedules_connection_insert/update` | Foreign-key substitute: 0008 added `connection_id` with `ALTER TABLE` and a default, which cannot carry `REFERENCES`. `schedules_connection_insert` is the only guard against an unknown schedule connection | Dropped (0028) | Postgres: a real foreign key. SQLite (#417): rebuild `tasks` and `schedules` with the foreign key |

### JSON columns

Go marshals every JSON value it writes, so key order and whitespace are
already Go's. **Evidence** means bytes are compared or must stay exactly as
recorded. It is `text` on Postgres. Everything else is **queried** (by SQL or
by Go decode) and is `jsonb`.

| Column | Class | Postgres |
| --- | --- | --- |
| `acceptance_requests.input` | Evidence: idempotency compares bytes (`previous != string(raw)`) | `text` |
| `task_acceptances.evidence` | Evidence: an immutable accepted snapshot (its `evidenceVersion` hash comes from the Go struct, not the stored bytes) | `text` |
| `acceptance_requests.result` | Queried: decoded for idempotent replay | `jsonb` |
| `execution_observations.observation` | Queried: `json_extract` in execution, acceptance, notifications, push, tasks | `jsonb` |
| `tasks.workspace` | Queried: `json_extract`/`json_set` (tasks, trees) | `jsonb` |
| `schedules.workspace`, `tasks`/`schedules.execution_config`, `harness_connections.new_task_config`, `preferences.notify_events` | Queried (decoded) | `jsonb` |
| `execution_attempts.intent`, `checkpoint`, `observed_session` | Queried (decoded; the checkpoint is opaque adapter state) | `jsonb` |
| `tasks`/`schedules.model_options`, `threads.pull_requests`, `threads.recent_runs`, `question_responses.answers` | Dropped (see Columns) | none |

`journal_entries.text` and `journal_launch_context.instructions` are plain
text, not JSON, and stay `text`.

### Doc claims this audit corrects

- [execution.md](execution.md): `LegacyConfig` is not the only reader of the
  legacy model columns. Duplicate, schedule spawn and patch validation also
  read them.
- [api.md](api.md): legacy model inputs are stored as sent and translated on
  read, not on write.
- #417 rewrote both when it dropped the columns.

## Design only: Queue, Events, Leases

Not built. Each needs a port with a single-process SQLite fallback before a
multi-replica Postgres deployment exists. Until then "one replica" holds on
both engines.

- **Queue** ([ADR 0004](adr/0004-queue-port.md), #408): River evaluated and
  not adopted. A capability port over an engine-native `jobs` table per
  adapter, created with its first consumer: enqueue inside the caller's
  `Write`, claim with a lease (Postgres `SKIP LOCKED`, SQLite one
  `BEGIN IMMEDIATE` write), attempt-fenced complete and fail, retry with
  backoff, a unique key over live jobs, run-at. One-shot jobs only. First
  consumer: Web Push delivery. The schedule runner, release runner, sync and
  sweeps stay reconcilers or periodic passes under Leases
  ([ADR 0004](adr/0004-queue-port.md#loops)).
- **Events** (#409): publish and subscribe. Postgres `LISTEN/NOTIFY`
  carrying ids that subscribers re-read; SQLite in-process. Today's
  in-process users: the notification inbox stream and the connections cache
  invalidation.
- **Leases** (#409): one holder per named job across replicas (schedulers,
  per-machine runners, the worker channel owner). Postgres advisory-lock or
  lease-row leases with renewal; SQLite always the one holder.
