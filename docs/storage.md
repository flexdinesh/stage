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
| [Layout](#layout-and-ownership) and [ports](#ports) | one package, `internal/db`, SQL beside the rules | #398 |
| [Transactions and locking](#transactions-and-locking) | SQLite: as described | Postgres: #403, proven by #400 |
| [Single owner](#single-owner) | SQLite: `stage.lock`, as described | Postgres: #403 |
| [Encodings](#encodings) and [errors](#errors) | SQLite: as described | Postgres: #403 |
| [Search](#search) | `lower(...) LIKE` (ASCII folding) | #404 |
| [Migrations](#migrations) | one SQLite dir, `server/internal/db/migrations`, head 24; journal events by triggers | per-engine dirs #401, triggers to Go #399, Postgres baseline #403 |
| [Backups](#backups) | SQLite: as described | Postgres warning: #403 |
| [Checks](#checks) | the tasks exist (#185); every gate reports `not present yet` | gates: #398, #400, #401, #403 |
| [Data copy and cutover](#data-copy-and-cutover) | none | #406 |
| Docker build and compose example | Docker image builds without the tag | #407 |
| [Legacy columns](#legacy-columns-and-compatibility-triggers) | none | #402 |
| [Design only](#design-only-queue-events-leases) | none | #408, #409 |

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
| `internal/db` | `db.Store`, domain types, app errors (`ErrNotFound`, `ConflictError`, `InvalidError`), business rules (`SwitchInstance`, `ForgetConnection`, tree rules, acceptance, launch claims, journal events), shared Go backfills | Import a driver or hold SQL |
| `internal/db/store` | Ports: the unit of work, repositories, capability ports, and the Go types they exchange | Import a driver, `internal/db/sqlite` or `internal/db/postgres` |
| `internal/db/sqlite` | Every SQLite statement, `modernc.org/sqlite`, pragmas, `stage_now`, `VACUUM INTO`, FTS5, SQLite error codes, the SQLite migrations dir | Hold business rules; import pgx |
| `internal/db/postgres` (build tag `postgres`) | Every Postgres statement, pgx v5 (`database/sql` only where goose needs it), the advisory lock, SQLSTATE mapping, the Postgres migrations dir | Hold business rules; import modernc; be needed by code built without the tag |
| `internal/db/storetest` | The adapter-agnostic contract suite: takes an engine factory, runs every port through `db.Store` and the ports | Depend on one engine's SQL |
| `internal/db/dbtest` | Test helpers; opens stores through the engine (a temp SQLite file, or the `check:postgres` database) | Open a database any other way |

Code outside the Postgres adapter compiles without the tag. Driver imports
and SQL strings outside their adapter fail `TestDriverImportBoundary`.

## Ports

Port signatures use Go types only: `time.Time`, `[]byte`, typed structs and
slices, `json.RawMessage` for opaque JSON. Never an engine encoding (no
timestamp strings, no JSON text for lists, no `sql.Null*` in a port).

**Unit of work.** `Read(ctx, fn)` and `Write(ctx, fn)` run `fn` with a
transaction handle that every repository call takes. A rule calls its
repositories inside one `Write`; nothing writes outside a `Write`.

**Repositories**, one per aggregate: tasks (and attempts, checkpoints,
observations), trees (needs, membership), journal, projects (bindings,
external projects, imports), connections (harness connections, T3 and
OpenCode settings), machines (pairing codes, machine tokens), schedules,
notifications, acceptance, attachments, users and identities, preferences,
push subscriptions, snippets, threads and sessions (the T3 mirror, session
identities). #398 may merge or split these where the code shows a better
seam; it updates this list.

**Capability ports**

| Port | SQLite | Postgres |
| --- | --- | --- |
| Ownership | `flock` on `<data dir>/stage.lock` ([runtime.md](runtime.md#ownership-and-locks)) | session advisory lock on a dedicated connection ([Single owner](#single-owner)) |
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

| Use case | Store entry (today) | Invariant | Postgres |
| --- | --- | --- | --- |
| Launch claim | `TransitionTask` (begin attempt) | one attempt begins per task and attempt number; current acceptance checked and inputs pinned atomically | task row `FOR UPDATE`; unique `(task_id, attempt)` |
| Release and schedule runner picks | `LaunchableForConnection`, `DueForConnection` | each item launches once | the launch claim's row lock decides; a later Queue uses `SKIP LOCKED` |
| Acceptance | `ChangeAcceptance` | one revision sequence per task; idempotent `requestId` | task row `FOR UPDATE`; unique request key with `ON CONFLICT` |
| Tree mutations | `CreateTree`, `SetNeeds`, membership changes | no cycles; a task in at most one tree; needs within the tree | tree rows, then task rows, `FOR UPDATE` before any need check |
| Schedule fire | `FireSchedule` | one spawn per fire time | schedule row `FOR UPDATE`; next fire advanced in the same transaction |
| Connection switch | `SwitchInstance`, `ForgetConnection` | at most one active connection per machine and kind; identity unique per machine | the machine's connection rows `FOR UPDATE`; partial unique indexes |
| Journal append | `AppendNote` | `requestId` idempotent per owner; same key with other content is 409 | unique `(owner, request_id)`; `ON CONFLICT DO NOTHING`, re-read, compare |
| Pairing redeem | `RedeemPairing` | a code redeems once, before expiry | one `UPDATE ... WHERE` unused and unexpired `... RETURNING` |
| Launch checkpoint | `SaveCheckpoint` | checkpoints of one attempt apply in order | attempt row `FOR UPDATE` |
| Project import | `ImportWorkspaces` | one project per workspace path per machine | connection row `FOR UPDATE`; unique binding indexes |

The table is indicative until #403 lands; the `storetest` concurrency cases
(#400) are the proof, and #403 corrects the table to what they prove.

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
- Which JSON columns are evidence (accepted snapshots, journal bytes,
  checkpoints) and which are queried: decided per column by #402 and #403,
  recorded under [Legacy columns](#legacy-columns-and-compatibility-triggers).
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
  `TestMigrationVersionParity` fails otherwise.
- **Postgres baseline:** one migration at the SQLite head when the Postgres
  adapter lands (#403; 24 today). It creates the current schema natively
  (`timestamptz`, `jsonb`, `bytea`, identity, partial and expression indexes,
  `CHECK`s) without the legacy columns and compatibility triggers
  [#402](#legacy-columns-and-compatibility-triggers) drops. Versions below it
  are SQLite-only history; the parity test compares versions from the
  baseline up.
- **No business rules in triggers.** Journal events are written by Go in the
  same `Write` as their cause (#399). A trigger may only maintain
  engine-internal structures (for example FTS tables), on one engine.
- **Expand, then contract.** Add tables and nullable or defaulted columns;
  backfill; move reads and writes; drop the old shape in a later migration.
  One migration never both drops and replaces a shape that running code reads.
- **Backfills in Go.** Data backfills are goose Go migrations registered with
  the same version on both engines. The logic lives once in `internal/db`;
  each adapter supplies only its SQL.
- **Roll forward only.** No down migrations. A bad migration is fixed by a
  new one. A downgrade restores a backup.
- **Version guard,** per engine with the same numbers: a database newer than
  the binary exits 78 and writes nothing
  ([runtime.md](runtime.md#schema-and-backups)).
- **Locking:** Postgres migrations run under goose's session locker, after
  the ownership lock. SQLite migrations run under the data-dir lock.

### Tests

- `TestStepwiseMigrations`: for every version `v`, `UpTo(v-1)`, seed
  representative rows, `Up(v)`, assert the data. SQLite from version 1;
  Postgres from the baseline (in `check:postgres`). No releases exist yet, so
  this is the upgrade coverage.
- Logical schema parity (`check:postgres`): tables, columns, nullability,
  primary, foreign and unique keys, and normalized types match across
  engines, with an explicit allowlist for engine-only objects.

### Adding a migration

1. Pick the next version `N` (the highest in either dir plus one).
2. Write `N_name.sql` in both engine dirs, each in its engine's native types;
   for a backfill, a Go migration registered as `N` on both.
3. Keep it expand-only unless it is the contract step of an earlier expand.
4. Extend the step-wise seed for the tables it touches.
5. Run `mise run check` (`check:postgres` runs because db code changed).
6. Update this file when the change alters a rule here.

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
  `check:postgres`. Adding a case: write it once in `storetest` against the
  ports and `db.Store`; it runs on both engines.

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
  [Encodings](#encodings), `BLOB` to `bytea`), columns the legacy audit drops
  handled as its table says, identity sequences reset past the highest ID.
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

#402 adds the decision table here: every legacy or retired column and
compatibility trigger (for example the legacy-id triggers of migrations 0008,
0012 and 0018), who reads or writes it, and whether it is kept in the
Postgres baseline, dropped from it, or a candidate to drop from SQLite later.
[architecture.md](architecture.md#builds-and-legacy-paths) promises raw retired columns are
preserved for historical records: a dropped column keeps its data in a
history-preserving form, or stays.

## Design only: Queue, Events, Leases

Not built. Each needs a port with a single-process SQLite fallback before a
multi-replica Postgres deployment exists. Until then "one replica" holds on
both engines.

- **Queue** (#408 evaluates River): enqueue inside the caller's `Write`,
  claim with `SKIP LOCKED`, retry with backoff, unique and scheduled jobs.
  SQLite: a jobs table under `BEGIN IMMEDIATE`. Candidates to move: the
  schedule runner, release runner, sync and sweeps.
- **Events** (#409): publish and subscribe. Postgres `LISTEN/NOTIFY`
  carrying ids that subscribers re-read; SQLite in-process. Today's
  in-process users: the notification inbox stream and the connections cache
  invalidation.
- **Leases** (#409): one holder per named job across replicas (schedulers,
  per-machine runners, the worker channel owner). Postgres advisory-lock or
  lease-row leases with renewal; SQLite always the one holder.
