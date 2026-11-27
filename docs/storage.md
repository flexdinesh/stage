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
| [Engines and selection](#engines-and-selection) | as described (#405); the Docker image is built with the `postgres` tag (#407) | |
| [Layout](#layout-and-ownership) and [ports](#ports) | as described, including the Postgres adapter `internal/db/postgres` (#403) and its composition in `internal/serve` (#405) | |
| [Transactions and locking](#transactions-and-locking) | as described on both engines; the `storetest` concurrency cases pass on Postgres (#403) | |
| [Single owner](#single-owner) | as described on both engines (#403, in `serve` #405) | |
| [Encodings](#encodings) and [errors](#errors) | as described on both engines (#403) | |
| [Search](#search) | as described on both engines (#404): SQLite FTS5 trigram (0029), Postgres `pg_trgm` GIN (0029) | |
| [Migrations](#migrations) | per-engine dirs and Go backfills as described (#401): SQLite `server/internal/db/sqlite/migrations` head 37; Postgres `server/internal/db/postgres/migrations` from the baseline `0028_baseline.sql` (#403), head 37 (0030 `claude_settings`, #372; 0031 `codex_settings`, #384; 0032 `no_default_harness`, #420; 0033 `launch_options` and 0034 `drop_workspaces`, #420; 0035 `drop_settled`; 0036 `harness_setup`; 0037 `connection_facts`); no Go backfills yet (#417's 0027 is SQL: SQLite-only history below the baseline); [journal events](#journal-events) written by Go (#399) | |
| [Backups](#backups) | as described on both engines (#403) | |
| [Checks](#checks) | as described (#185, #398, #400, #401); `check:postgres` runs the Postgres schema parity, step-wise, adapter and `storetest` suites on Postgres (#403) and `serve` on Postgres (#405) | |
| [Data copy and cutover](#data-copy-and-cutover) | as described (#406): `stage db copy` (`internal/db/datacopy` over the copy ports), cutover and rollback runbook | |
| [Docker image and compose example](#docker-image-and-compose-example) | as described (#407) | |
| [Legacy columns](#legacy-columns-and-compatibility-triggers) | audited (#402); load-bearing trigger logic in Go and those triggers dropped (#399, 0026); every other Drop row dropped from SQLite (#417: 0027 backfill, 0028 contract); the Postgres baseline omits them (#403); the `threads` mirror dropped on both engines (0032, #420); task and schedule `workspace` and `task_launch_bases` dropped (0034, #420); `tasks.settled_at` dropped (0035) | |
| [Design only](#design-only-queue-events-leases) | Queue decided: [ADR 0004](adr/0004-queue-port.md) (#408), not built | Events, Leases: #409 |

## Engines and selection

One engine per process, chosen at startup.

| | SQLite | Postgres |
| --- | --- | --- |
| Built into | every build | only builds with `-tags postgres`: the `Dockerfile` ([below](#docker-image-and-compose-example)). Desktop, CLI, installer and release binaries never contain it (no pgx) |
| Selected by | `STAGE_DATABASE_URL` unset | `STAGE_DATABASE_URL=postgres://...` or `postgresql://...` |
| Database | `<data dir>/stage.db` | the database the URL names |
| Used by | desktop, CLI, installs, the per-user service, dev, the default compose stack, tests | a main server run from the Docker image, such as `examples/postgres` |

- `config.ParseDatabase` reads it: the prefixes match exactly (lowercase;
  pgx reads anything else as a keyword/value string). Any other
  `STAGE_DATABASE_URL` value is invalid configuration (exit 78).
- A Postgres URL in a binary built without the tag exits 78 with an error
  that says the binary has no Postgres support and to use the Docker image,
  before the data dir is locked (`config.PostgresBuilt`, set by the tag).
- `internal/serve` (`engine.go`) composes the engine, its Ownership and
  Backup ports and the migration guard. On Postgres, opening and taking
  ownership are bounded at 30 s; an unreachable database exits 1
  ([runtime.md](runtime.md#ownership-and-locks)).
- The data dir stays required on both engines for `stage.lock`, sockets,
  logs and pending private setup output. On SQLite it also holds `stage.db`
  and `backups/`.
- Detailed health (`engine`), `stage status` and startup logs report the running
  engine (`sqlite` or `postgres`). Shared public health omits it.
  `stage version` reports the engines built
  in (`engines`). Never the URL or its password: `config.DatabaseURL` prints
  `[redacted]` (fmt, slog, JSON), and no log line or error prints the URL.
  pgx connection errors may name the user, database and host.
- The desktop app, the installer and `stage service` are SQLite only:
  `serve --managed` ignores an inherited `STAGE_DATABASE_URL` and
  `service.env` refuses it ([service.md](service.md#serviceenv)).
- Tests read `STAGE_TEST_DATABASE_URL` only under `-tags postgres`, set only
  by `check:postgres` (see [Checks](#checks)).

### Docker image and compose example

- The `Dockerfile` is the only build with `-tags postgres`. The image runs on
  SQLite in `/data` by default and on Postgres when `STAGE_DATABASE_URL` is
  set. `scripts/release.sh` (CLI, installer and desktop binaries) never passes
  the tag.
- The root `compose.yaml` (the production stack) stays on SQLite.
- `examples/postgres/compose.yaml` (project `stage-postgres`, with a README)
  runs the image against Postgres:
  - a `postgres` service on `STAGE_PG_IMAGE` (`check:storage` fails if the
    tag differs), initialized with the builtin `C.UTF-8` locale
    ([Search](#search)), a `pg_isready` healthcheck, the named volume
    `pgdata`, no published port;
  - an `app` service started once Postgres is healthy, with
    `STAGE_DATABASE_URL` from the required `STAGE_PG_PASSWORD`, a
    `/stage status` healthcheck and no Stage data volume. Postgres
    holds the durable data.
- Backups in the example are the operator's ([Backups](#backups)).

## Layout and ownership

`db.Store` stays the facade: the ~50 packages that import `internal/db` keep
its API, and only `internal/serve` composes an engine.

| Path | Owns | Must not |
| --- | --- | --- |
| `internal/db` | `db.Store`, domain types, app errors (`ErrNotFound`, `ConflictError`, `InvalidError`), business rules (`SwitchInstance`, `ForgetConnection`, tree rules, acceptance, launch claims, journal events) | Import a driver or hold SQL |
| `internal/db/backfill` | Shared Go backfills: the step list (`backfill.Steps`, version and name), each backfill's logic and the query port it declares | Hold SQL; import a driver or an adapter |
| `internal/db/store` | Ports: the unit of work, repositories, capability ports, and the Go types they exchange | Import a driver, `internal/db/sqlite` or `internal/db/postgres` |
| `internal/db/sqlite` | Every SQLite statement, `modernc.org/sqlite`, pragmas, `stage_now`, `VACUUM INTO`, FTS5, SQLite error codes, the SQLite migrations dir | Hold business rules; import pgx |
| `internal/db/postgres` (build tag `postgres`) | Every Postgres statement, pgx v5 (`database/sql` only where goose needs it), the advisory lock, SQLSTATE mapping, the Postgres migrations dir; `postgres/pgtest` opens test engines on the `check:postgres` server | Hold business rules; import modernc; be needed by code built without the tag |
| `internal/db/storetest` | The adapter-agnostic contract suite: `Run(t, Factory)` runs every case through `db.Store` and the ports on engines from the factory ([Contract suite](#contract-suite)) | Depend on one engine's SQL; import an adapter |
| `internal/db/dbtest` | Test helpers; opens engines and stores (`Engine`, `DB.Reset`): a temp SQLite file, or under `check:postgres` (`Engine` only) a fresh Postgres database from `postgres/pgtest` | Open a database any other way |

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
destinations and inputs, handoffs), `Observations` (normalized
execution observations), `Trees` (trees, membership, needs), `Journal`,
`Acceptances`, `Schedules` (and runs), `Projects` (bindings, external
workspaces, identity and imports), `Connections` (harness connections),
`HarnessSetup` (settings, secrets and sync status), `Machines` (pairing codes,
machine tokens), `ObservationProducers` (producer cursors and dispositions),
`Notifications`, `Sessions` (session identities), `Attachments`,
`Users`, `Preferences`, `Push` (VAPID keys, subscriptions) and `Snippets`.
Repository methods are named queries: they hold no rules, return engine
errors unchanged and `store.ErrNotFound` for a missing row; `internal/db`
classifies errors through the `ErrorClassifier`. `Machines`, `Connections`,
`Trees`, `Tasks` and `Schedules` also have `Lock`: the rules call it first
in a read-check-write use case ([Transactions and
locking](#transactions-and-locking)); a no-op on SQLite.

Task creator attribution uses the stored `origin_session_id`. The task list
can filter by the creator's scoped session identity.

**Capability ports**

| Port | SQLite | Postgres |
| --- | --- | --- |
| Ownership | `flock` on `<data dir>/stage.lock`, taken by `internal/serve` with the data dir before the engine opens ([runtime.md](runtime.md#ownership-and-locks)); the port's `Acquire` holds nothing more and its lease is never lost | session advisory lock on a dedicated connection; the lease reports loss ([Single owner](#single-owner)) |
| Backup | `VACUUM INTO` before migrating ([Backups](#backups)) | none: logs a warning before migrating |
| Migrator | goose `sqlite3` dialect over the SQLite dir | goose `postgres` dialect with its session locker over the Postgres dir |
| ErrorClassifier | SQLite extended codes ([Errors](#errors)) | SQLSTATE |
| Search | FTS5 trigram, `LIKE` for short queries ([Search](#search)) | `pg_trgm` GIN with `ILIKE` |

Repositories have one implementation per engine with engine-native SQL. Where
they differ in more than syntax:

| Repository | SQLite | Postgres |
| --- | --- | --- |
| tasks | claims rely on the single writer | the claim locks the task row (`Tasks().Lock`) |
| connections | single writer keeps one active connection per machine and kind | partial unique index plus a lock on the machine row (`Machines().Lock`) |
| journal, acceptance | `requestId` idempotency under the single writer | the owner row's lock (`Tasks().Lock`, `Trees().Lock`) serializes appends and changes, then read, compare, insert; the unique keys stay as a backstop |
| attachments, machine tokens | `BLOB` | `bytea` |

The `harness_connections` snapshot cache stays in `db.Store` and is dropped by
every write to that table, as today. It assumes one process
([Design only](#design-only-queue-events-leases)).

## Notification receipts

`notifications` retains one event per occurrence. In shared mode, the event
and unread `notification_recipients` rows commit in the same transaction.
The recipient insert selects active human accounts whose current notification
filter allows the event. Personal mode adds one unread receipt for user 1
after the notifier applies the owner's filter. Pruning an event deletes its
receipts through the foreign key. PostgreSQL serializes event ID allocation
until commit so a stream cursor cannot skip a late commit with a lower ID.

Inbox history and stream replay include retained events for every account.
Each account sees its own receipt timestamp. An event without a receipt reads
as of its creation time. Read actions update that account's unread receipts.
Delivery requires an unread receipt. Joining or changing a filter affects
future receipts. `notification_preferences` stores each user's filter, with
all notification events enabled by default. The default connection remains
an installation-wide setting in `preferences`.

In shared access, browser push subscriptions reference an active browser
session. Soft revocation deletes the affected bindings in the same
transaction. Expired sessions are excluded immediately and their bindings
are pruned. Deleting sessions on explicit restore cascades to their
subscriptions. Database copy preserves receipts, filters, VAPID keys, and
personal subscriptions. It skips session-bound subscriptions together
with the transient credentials.

## Journal events

Rows derived from a change are written by Go in `internal/db`
(`journal_events.go`), in the same `Write` as the change, so they commit and
roll back with it on either engine. SQLite triggers wrote them until
migration 0026 (#399).

| Cause (helper) | Derived rows |
| --- | --- |
| Task event (`addTaskEvent`) | event `task_created` (no from state), `task_state_changed` (detail plus ` (from -> to)`) or `task_event` (same state), on the task and its tree, at the task event's time, with the task's session and launch attempt; the task records work when launched, adopted or entering launching, running, needs input, done, failed or interrupted, and a task with recorded work protects its tree |
| Task edit (`UpdateTask`) | `task_edited` and timeline event in the edit transaction. A start-time edit records the source of a later scheduled launch. |
| Successful harness action on a linked session (`RecordSessionAction`, `RecordTaskOperation`) | `session_action`, `session_response` or `task_stopped` on the task and its tree. Session responses retain the request ID. Dispatch occurs outside the database transaction. |
| Note (`insertNote`) | protects its task and tree; the tree's `updated_at` becomes the note's time |
| Tree created, config, status (`insertTree`, `updateTree`, `setTreeStatus`, `archiveDissolved`) | `tree_created` (title), `tree_config_changed` (`Title: a -> b; start mode: x -> y`, only when either changed), `tree_status_changed` (`old -> new`, only on a change) |
| Membership (`setMembership`, `detachMembers`) | `task_moved` (task title, from and to tree) in the old and the new tree; both trees' membership revision + 1, `updated_at` now, recorded work carried over; an emptied old tree is archived (closed now), with its status event |
| Needs (`addNeed`, `keepNeeds`, `inheritNeeds`, `dropNeeds`, `detachMembers`) | `needs_changed` (`Added` or `Removed need #N`) in the dependent's tree, by task then need; none for a standalone task |
| Acceptance (`insertAcceptance`) | `task_acceptances.source_session_id` = the task's session |
| Handoff (`recordHandoff`) | source session: the evidence's session, else none (a bare legacy thread id names no connection, #420) |

- **Attribution:** new work records the admitted actor and known cause with
  each event in the same transaction. Existing creator IDs retain their
  semantics. Public names are resolved by stable user ID on reads.
  [Work attribution](api.md#work-attribution)
- **Changes from the triggers:** task events no longer copy the legacy
  `tasks.t3_thread_id` into `source_thread_id` (the API's `sourceThreadId`
  reads the source session's external id, for every harness); deleting an unstarted tree writes no
  `needs_changed` rows into the tree it deletes; no direct `INSERT` of a task
  into a tree exists, so `journal_member_created` has no replacement. Events
  from one bulk change are written in the orders above.
- **Tests:** `TestJournalEventRowsExact` pins every row;
  `TestDerivedWritesOnceAndRollBack` proves each helper writes once and rolls
  back with its `Write`; `TestDerivedWritesBoundary` fails when `internal/db`
  calls a raw repository write outside its helper. They read SQL or call the
  helpers, so they stay in `internal/db`, not `storetest`.

## Harness setup

`harness_settings` stores one public JSON object per machine and harness kind.
An absent settings row reads as `{}`. A save takes the machine lock and merges
top-level keys in one transaction. Omitted keys retain their values and an
explicit JSON null is persisted. Modules own defaults and validation.

`harness_secrets` stores an opaque JSON value per machine, kind and key. Reads
return an absent value separately from JSON null. The local store can merge a
settings patch and update or delete one secret atomically. Worker secrets stay
in the worker credential file. `harness_sync_status` stores nullable last
error and last successful full sync per machine and kind, independently of a
connection. Each status write updates only its own field.

## Worker observation receipts

`observation_producers` records the committed cursor per machine and
producer. One producer is active per machine, and superseded producer IDs
stay recorded to fence replay. A record's effects and cursor commit in one
write. A rejected record, such as one for a retired connection, advances
the cursor with a code in `observation_dispositions`. A gap disposition
also records the time range and the loss count as decimal text to preserve
the full unsigned range. Dispositions store metadata only. A failed write
stops advancement at the last committed record. A retry after a lost reply
returns the committed cursor and skips applied records. Transient
notifications run after commit.

The worker's outbox and cached compose inputs live under its own config
directory. The database copy carries the server's observation receipts
and task effects, so the server can recognize replay after a cutover.
If a restored receipt is behind the worker's released cursor, the main
advances it and writes a `receipt_rollback` disposition in the same
transaction. The disposition records the exact count of unavailable
records through the released cursor. The main requests a resample.
[Worker observation files](runtime.md#worker-observation-files)

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
  that makes it atomic, below. Locks are explicit: the rule calls the
  repository's `Lock` (`Machines`, `Connections`, `Trees`, `Tasks`,
  `Schedules`) before its first checked read. The adapter takes
  `SELECT ... FOR NO KEY UPDATE` in ascending id, which serializes writers
  of the row without blocking foreign-key checks on it (inserting an event
  or a need never waits on it). A new use case of this kind adds its row to
  the table and a concurrency case to `storetest`.
- **Lock order** (prevents deadlocks): machine, connection, tree, task
  (ascending id), schedule. A use case that needs several takes them in this
  order. Derived writes can still update a row out of order (a task's
  transition marks its tree's recorded work after the task lock); a deadlock
  that causes is detected and retried.
- **Retry:** `Write` reruns `fn` after a serialization failure (`40001`) or
  deadlock (`40P01`), up to 3 runs in all with a short jittered backoff, then
  returns the error. `fn` has no effects outside the transaction (external
  effects already go through launch checkpoints), and every `Write` closure
  in `internal/db` resets the results it captures at its start, so a rerun
  starts from scratch. Other errors never rerun.
- `Read` is a read-only READ COMMITTED transaction: each statement sees
  committed data, and a write inside it fails (`25006`). No use case needs
  one snapshot across statements yet; one that does says so and gets
  `REPEATABLE READ READ ONLY`.

| Use case | Store entry (today) | Invariant | Postgres | `storetest` case |
| --- | --- | --- | --- | --- |
| Launch claim | `TransitionTask` (begin attempt) | one attempt begins per task and attempt number; current acceptance checked and inputs pinned atomically | task row lock; unique `(task_id, attempt)` | `ConcurrentLaunchClaim` |
| Release and schedule runner picks | `LaunchableForConnection`, `DueForConnection` | each item launches once | the launch claim's row lock decides; the runners stay reconcilers under Leases (#409), not Queue jobs ([ADR 0004](adr/0004-queue-port.md#loops)) | `ConcurrentLaunchClaim` (the claim decides) |
| Acceptance | `ChangeAcceptance` | one revision sequence per task; idempotent `requestId` | task row lock before the request lookup; unique request key as a backstop | `ConcurrentAcceptance`, `ConcurrentAcceptanceRequestID` |
| Tree mutations | `CreateTree`, `UpdateTree`, `SetTreeStatus`, `DeleteTree`, `DissolveTree`, `AddToTree`, `RemoveFromTree`, `SetNeeds` | no cycles; a task in at most one tree; needs within the tree | `lockTreeWork`: the named trees and the trees of the tasks involved, then the tasks (the task, its needs, the members joining), before any check; a task that moved meanwhile gets its new tree locked too | `ConcurrentNeedsSerialize`, `ConcurrentNeedsCycle`, `ConcurrentTreeMembership`, `ConcurrentNeedsAndRemoval`, `JournalMoveAndAppendSerialize` |
| Schedule fire | `FireSchedule` | one spawn per fire time | schedule row lock; next fire advanced in the same transaction | `ConcurrentScheduleFire` |
| Connection switch | `SwitchInstance`, `RetireConnection`, `ForgetConnection`, `RetireAndForget` | at most one active connection per machine and kind; identity unique per machine | the machine row lock; partial unique indexes | `ConcurrentConnectionSwitch` |
| Journal append | `AppendNote` | `requestId` idempotent per owner; same key with other content is 409 | the owner's row lock (task or tree), then read, compare, insert; unique `(owner, request_id)` as a backstop | `JournalConcurrentRetryAndPagination`, `ConcurrentNoteRequestIDConflict` |
| Pairing redeem | `RedeemPairing` | a code redeems once, before expiry | expiry read, then one `UPDATE ... WHERE redeemed_at IS NULL`: the loser updates no row | `RedeemPairingConcurrentSingleWinner` |
| Launch checkpoint | `SaveCheckpoint` | checkpoints of one attempt apply in order | one `UPDATE ... WHERE revision = $expected`: a stale writer updates no row (409) | none yet |
| Project identity | inventory refresh, resolve/apply, bind/unbind, primary, decline, adoption, merge, split and dismissals | placement sees all connections and serializes with evidence and binding changes | `Projects().LockIdentity` first. SQLite single writer, Postgres transaction advisory lock | `ConcurrentImportAcrossConnections` |

Adoption refreshes the verified live workspace inventory under the identity
lock before choosing or creating a binding. The refresh, binding, adopted
task and tree changes commit together.

Each row's lock was checked to matter: with it removed, its `storetest`
case fails on Postgres. On SQLite the single writer keeps every invariant,
so these cases pass there by construction; they bite on an engine without a
global lock. The Postgres test engine opens its whole pool before a case
runs, so racers overlap instead of waiting on connection dials.

## Single owner

One server owns one database, on either engine. A second owner would run
duplicate schedulers, sync and launches.

- **SQLite:** the data-dir `stage.lock` ([runtime.md](runtime.md#ownership-and-locks)).
  `stage.db` must sit on a local filesystem.
- **Postgres:** a session advisory lock (`pg_try_advisory_lock` on one fixed
  64-bit key, `ownerLockKey` in `ownership.go`) on a dedicated connection held
  for the process lifetime, taken right after the data-dir lock and before
  migrating. Held by another session: `Acquire` returns `store.OwnedError`
  naming the holder's `application_name` (`stage <instanceId>`, from
  `postgres.Options`), and the server exits 75, like a held data dir. The
  lease pings its connection every 5 s; a failed ping closes `Lost()`, and
  `serve` stops with `store.ErrOwnershipLost` (exit 1) rather than keep
  running unowned.
- Two servers with different data dirs on one Postgres database: the second
  exits 75. "One replica" ([ADR 0002](adr/0002-machines-and-workers.md),
  [runtime.md](runtime.md#remote-main-server-and-workers)) holds on both
  engines.

## Encodings

| Value | SQLite | Postgres |
| --- | --- | --- |
| Timestamps | TEXT, fixed-width UTC `2006-01-02T15:04:05.000000Z` (lexical order is time order); Go clock via `stage_now()`, schema defaults `strftime` (ms) | `timestamptz`; Go-supplied values; adapter stamps `clock_timestamp()` (advances within a transaction, like `stage_now()`); defaults `now()` |
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
  under [JSON columns](#json-columns) (#402; the #403 baseline follows it).
- Business rules never depend on the SQL clock; time comes from Go.
- `jsonb` keeps values, not bytes: keys come back reordered and whitespace
  normalized. Queried columns decode to the same values; only the evidence
  columns (`text`) keep bytes.
- A port field that carries a time as text (`HarnessConnection.RetiredAt`,
  `ForgottenAt`) reads in the SQLite format on both engines.

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

Two searches, through `TaskFilter.Q` (tasks: title or prompt) and
`JournalQuery.Q` (journal: text). There is no separate port object: the `Q`
fields are the Search capability, and each adapter builds the predicate in
its `search.go`, inside the same query as the other filters, the ordering
and paging. (Thread search was removed with API v3.)

- **Fixed on both engines:** the filters, the ordering (tasks
  `updated_at DESC, id DESC`, journal `id DESC` with its cursor), paging,
  substring matching, every character matched literally (`%`, `_`, `\`,
  `"`), and case-insensitive matching, non-ASCII included. Nothing ranks.
- **May differ:** relevance, if a search ever ranks (ties always break by
  id, so ordering stays deterministic), and case folding at the edges of
  Unicode (FTS5's folding, Go's `strings.ToLower` and the Postgres ctype
  disagree on rare characters).
- **SQLite:** FTS5 external-content tables with the trigram tokenizer
  (`case_sensitive 0`, no diacritic removal): `tasks_fts(title, prompt)` over
  `tasks` and `journal_fts(text)` over `journal_entries` (migration 0029). A
  query of 3 or more characters is one FTS5 phrase (`MATCH '"..."'`, `"`
  doubled), which the trigram tokenizer matches as a substring. Shorter
  queries cannot form a trigram: they fall back to
  `stage_lower(col) LIKE stage_lower(?) ESCAPE '\'`, where `stage_lower` is a
  registered Go function (Unicode `strings.ToLower`; SQLite's `lower()` folds
  ASCII only), and scan.
- **SQLite index upkeep: triggers.** `AFTER INSERT`, `AFTER DELETE` and
  `AFTER UPDATE OF` the searched columns keep the FTS tables in step with
  every write path (repositories, imports, cascades, SQL migrations). They
  maintain a derived index, not a business rule, so they stay in SQL (unlike
  [journal events](#journal-events)). A migration that rebuilds `tasks` or
  `journal_entries` must recreate the six `*_fts_*` triggers and run
  `INSERT INTO x(x) VALUES ('rebuild')`; `TestSearchIndexFollowsWrites`
  checks the triggers and FTS5's `integrity-check` at the head.
- **Postgres:** the `pg_trgm` extension and GIN `gin_trgm_ops` indexes on
  `tasks.title`, `tasks.prompt` and `journal_entries.text` (migration 0029);
  `ILIKE ... ESCAPE '\'` substring matching, which the indexes serve for
  queries of 3 or more characters (shorter ones scan). `pg_trgm` is a
  trusted extension: the migrating role needs to own the database or hold
  `CREATE` on it.
- **Postgres locale:** `ILIKE` and `pg_trgm` fold case with the database's
  ctype. Non-ASCII case-insensitivity needs a UTF-8 ctype with Unicode case
  mapping: the builtin `C.UTF-8` provider, ICU, or a libc UTF-8 locale. A
  database with ctype `C` or `POSIX` folds ASCII only; nothing checks this
  at startup. Test databases use the builtin `C.UTF-8` provider
  (`pgtest.Locale`).
- **Contract tests** (`storetest` `Search*`): substring hits in title and
  prompt, ASCII and non-ASCII case-insensitivity, literal metacharacters and
  quotes, 1 and 2 character queries, other filters still applying, the
  ordering, journal paging by cursor, and edits and deletes changing what
  matches.

## Migrations

- **Tool:** goose, migrations embedded per engine:
  `internal/db/sqlite/migrations` and `internal/db/postgres/migrations`, both
  tracked in `goose_db_version`.
- **Shared versions:** a schema change adds `NNNN_name.sql` (or a Go
  migration) with the same version and name in both dirs.
  `TestMigrationVersionParity` fails otherwise.
- **Postgres baseline:** one migration at the SQLite head when the Postgres
  adapter landed: `0028_baseline.sql` (#403). It creates the current schema
  natively (`timestamptz`, `jsonb`, `bytea`, identity, partial and expression
  indexes, `CHECK`s), with SQLite's seeded rows and IDs, without the legacy
  columns and compatibility triggers
  [#402](#legacy-columns-and-compatibility-triggers) drops; #417 drops them
  from SQLite first, so the two heads match. Versions below it are
  SQLite-only history; the parity test compares versions from the baseline
  up.
- **Shared migrations since the baseline:** 0029 search indexes (#404);
  0030 `claude_settings` (#372): each machine's Claude Code setup
  (`binary_path`, the claude command name or path, `''` for the default
  lookup, not `binary`, a reserved word on Postgres; `config_dir`, the
  config directory Claude reported at the last connect, the connection's
  identity that health checks; `disconnected`; `updated_at`). No row reads the defaults and the first save inserts one,
  so neither engine seeds it. 0031 `codex_settings` (#384): each machine's
  Codex setup (`binary_path`, the Codex CLI, `''` for `codex` on PATH;
  `connected`, set by connect and cleared by disconnect; `updated_at`),
  unseeded the same way. 0032 `no_default_harness` (#420, contract, ADR 0007
  decision 12): drops the T3-only `threads` mirror (every harness's observed
  state is in `execution_observations`; run span, origin title, last-word
  preview and tree activity derive from it) and deletes the seeded T3
  connection 1 when it was never configured (no identity, no T3 token,
  default settings, not the default connection) and no row references it
  (tasks, schedules, session identities, bindings, external
  projects, imports, launch destinations). A fresh install therefore has no
  connection: the first pairing or connect creates one. A paired or used
  connection 1 stays. Its `t3_connection` row (keyed by machine) stays for
  the next pairing. Tests: the step-wise scenarios on both engines (unused
  connection removed; paired, stamped, default, referenced and bound kept)
  and `TestFreshInstallHasNoConnection` (`dbtest.FreshEngine`, both
  engines); `dbtest.Reset`/`Engine` seed connection 1 for the suites that
  need a T3 connection.
  0033 `launch_options` (#420, expand): a worktree becomes a harness launch
  option. Each task's and schedule's `workspace` of type `worktree` adds
  `{"id":"worktree","value":true}` to its `execution_config` options on a
  T3, Claude Code or OpenCode connection (Codex has none); on T3 it also
  adds its non-empty `baseRef` and `branch` as text options, a
  predecessor-based workspace's stored fallback included. Unfinished launch attempts (`prepared`, `unconfirmed`)
  get the same options in their intent's config, so recovery keeps the
  worktree. An option already set is kept. Each predecessor-based task
  that can still start (backlog, failed, interrupted or cancelled, not
  archived; finished history gets none) gets one `task_event` journal entry naming where its worktree now
  starts, and that the handoff carries the predecessor's branch: T3 from
  its `baseRef`; a T3 task with no stored `baseRef` (it carries `worktree`
  without one, which T3 rejects at launch) that a base ref must be set
  before it starts; Claude Code and OpenCode the harness default. 0034
  `drop_workspaces` (#420, contract): drops `tasks.workspace`,
  `schedules.workspace` and `task_launch_bases`. Tests: the step-wise
  scenarios on both engines (each harness kind, root and worktree, T3
  predecessor tasks with and without a stored base ref and their journal
  text, attempts, schedules; columns and table gone).
  0035 `drop_settled` (API v9, contract): converts task state `settled` to
  `done`, using `settled_at` only when `finished_at` is null. Current acceptance
  IDs stay as recorded, so accepted tasks stay completed and reopened tasks
  remain unaccepted. Task event state columns and notification events map to
  `done`, while event details, journal text and stored acceptance evidence
  and request bytes stay intact. Both engines remove the `settled` preference
  entry, preserving the order of the rest and allowing an empty array.
  Both remove `settled` from the task and notification CHECK constraints and
  drop `tasks.settled_at`. Postgres uses a new preference column default
  `["needs_input","failed","done"]`. SQLite keeps its original default literal
  `["needs_input","failed","done","settled"]`. The preferences singleton already
  exists, and reads filter by `NotificationEvents`. SQLite rebuilds tasks and
  notifications with their ID sequences and indexes, restores the task FTS
  triggers and rebuilds the FTS index. Paired migration tests seed 0034 data
  and check normalization, retained acceptance, preference ordering and
  identical evidence bytes. SQLite also checks title search, foreign keys and
  subsequent ID allocation.
  0036 `harness_setup` moves public settings, opaque secrets and sync status
  into generic machine-and-kind tables. Both engines copy and compare row counts
  and values before dropping `t3_connection`, `opencode_settings`,
  `claude_settings` and `codex_settings` in the same transaction. Settings data,
  sync timestamps and partial credentials survive even without an active
  connection. The unconfigured local baseline documents become empty objects,
  with defaults supplied by their modules. Connection rows and references are
  untouched. The additive 0037 `connection_facts` migration adds nullable
  `last_known_facts` to each connection. The server saves sanitized display
  facts from a connected status response and can read them without contacting
  the worker. Older connections begin without a snapshot. A replacement starts
  its own snapshot, and a forgotten connection's snapshot is excluded from reads.
  Paired migration 0038 `project_identity` backfills the new shape before
  dropping the superseded columns and table in the coordinated layer 07
  upgrade. See [Project identity](#project-identity).
  Paired migration 0039 `observation_producers` adds durable worker observation
  receipts on SQLite and Postgres. Existing task and observation data is
  retained. The receipts are included in `db copy`. Update the main server
  and workers together.
  See [Worker observation receipts](#worker-observation-receipts).
  Paired migration 0040 adds durable installation and shared account state.
  Existing installations start personal unless opted in. First-admin claim
  binds a local identity to user 1 and keeps existing creator references,
  accepted evidence and worker pairings. The persisted mode advances from
  personal through setup pending to claimed.
  Paired migration 0041 `access_audit` adds durable access-management events
  on SQLite and Postgres. An event records an action, the account or local
  installation owner who performed it, the affected account or grant ID
  when applicable, and its time. The account service writes the event in
  the same transaction as the change. Audit rows carry no addresses or
  credential material. Grant IDs survive copy as historical references.
  The migration also enforces a single unconsumed, unrevoked password
  reset per account. Reissuing revokes the outstanding row, including an
  expired one, before inserting the replacement.
  Paired migration 0043 `notification_recipients` adds account receipts,
  notification filters, and browser session bindings on SQLite and Postgres.
  It copies existing read times and the saved filter to user 1 and removes
  unclaimed push endpoints from already opted-in installations. The legacy
  columns stay available during the expansion.
  See [Notification receipts](#notification-receipts).
  Paired migration 0044 `work_attribution` adds nullable attribution
  columns to task events, journal entries, acceptances, execution attempts
  and schedule runs. The text columns carry a structured actor and separate
  cause and machine provenance. A null stamp reads as historical. Existing
  creator IDs, journal text and acceptance evidence bytes are preserved.
  Database copies preserve actor stamps and the user IDs they reference.
  Paired migration 0045 `workspace_refresh` adds nullable last-success
  and failure fields to each harness connection. A complete snapshot and
  its success time commit together. An unsuccessful read updates only the
  failure field. Existing inventory remains available as last-known data
  until the next successful refresh supplies a timestamp.
  Paired migration 0048 `project_groups` adds catalog membership, a
  membership revision and an attribution stamp to each external workspace.
  It seeds exact manual and original import bindings and automatic joins
  corroborated by a strong key on an anchor of the same project. Existing
  exact historical associations can seed other rows. The
  revision and stamp belong to membership. Catalog-created project IDs
  support the same history, aliases and snippet scopes as other projects.
  Copies carry the membership columns and project IDs without a transform.
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
  the baseline (`internal/db/postgres/stepwise_test.go`, `check:postgres`).
  Each engine's `stepwiseMigrations` registry
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
  engine fails. `TestSchemaParityCompare` proves the comparator on
  hand-written schemas.
- Postgres tests each create and drop their own database on the
  `check:postgres` container; without `STAGE_TEST_DATABASE_URL` they fail.
  `postgres/pgtest` clones engine databases from a template migrated once
  per test process.

### Adding a migration

1. Pick the next version `N`: the highest in either dir or `backfill.Steps`,
   plus one.
2. Schema change: write `N_name.sql` (`-- +goose Up` only) in
   `internal/db/sqlite/migrations` and the same name in
   `internal/db/postgres/migrations`, each in its engine's native types.
3. Backfill: add `backfill.Step{N, "name"}`, its
   function and query port in `internal/db/backfill`, and a runner for `N`
   in each adapter's `goMigrations` implementing that port. No `.sql` file
   at `N`.
4. Keep it expand-only unless it is the contract step of an earlier expand.
5. Add `N` to each engine's `stepwiseMigrations`: seed the tables it touches
   at `N-1`, check the data and constraints after `N`. Reads of the result
   through `db.Store` go in a separate test that reuses the seed.
6. Engine-only tables or columns (for example an FTS table on SQLite): add
   them to `parityAllowlist` with why. A SQLite rebuild of `tasks` or
   `journal_entries` recreates the [search](#search) triggers and rebuilds
   the FTS index.
7. Never edit an applied migration; fix forward with a new one.
8. Run `mise run check` (`check:postgres` runs because db code changed).
9. Update this file when the change alters a rule here.

## Backups

- **SQLite (automatic):** before migrating a non-empty database, `VACUUM INTO
  <data dir>/backups/stage-schema<N>-<UTC time>.db` (0600 in a 0700 dir), keep
  the newest 3; a failed backup exits 1 with the database untouched
  ([runtime.md](runtime.md#schema-and-backups)).
- **Postgres (operator-owned):** the server takes no backup. Before applying
  pending migrations the Backup port logs a warning naming the current and
  target versions and that no backup was taken, then the server migrates. Backups, snapshots and
  point-in-time recovery are the operator's (managed service, `pg_dump`;
  `examples/postgres/README.md` shows one).

## Checks

All gates run in `mise run check`; there is no CI.

| Task | Runs | Covers |
| --- | --- | --- |
| `check:go` | always | `go vet` (plain, `smoke`, `postgres` tags), `CGO_ENABLED=0 go build -tags postgres` of the server, `go test ./...` (SQLite) |
| `check:storage` (`scripts/check-storage.sh`) | always | the gate tests by name under `server/internal/db` (`TestMigrationVersionParity`, `TestStepwiseMigrations`, `TestDriverImportBoundary`) and the `internal/db/{store,sqlite,postgres,storetest}` packages; a gate or package not written yet prints `not present yet` and passes; the Postgres pin (the image in `examples/postgres/compose.yaml` is `STAGE_PG_IMAGE`) |
| `check:postgres` (`scripts/check-postgres.sh`) | from `check` only when the branch changes `server/internal/db/**`, `server/internal/auth/**`, `server/internal/serve/engine*.go`, `server/internal/config/database*.go`, `server/cmd/stage/db*.go`, `docs/storage.md`, `server/go.mod` or `Dockerfile` vs the merge-base with `origin/main` (committed, uncommitted or untracked); always when run directly | a throwaway `stage-pgtest-<random>` container (`STAGE_PG_IMAGE`, random `127.0.0.1` port, tmpfs, always removed), `go test -tags postgres ./internal/db/... ./internal/auth/... ./internal/api/... ./internal/serve/... ./internal/config/... ./cmd/stage/...` with `STAGE_TEST_DATABASE_URL`: migrate up, schema parity, step-wise from the baseline, `storetest`, db, auth and api suites, `serve` on Postgres (engine reported, single owner across data dirs, newer schema refused, unreachable database), and the [data copy](#data-copy-and-cutover) round trip and command. Without Docker it fails, never skips |

- New gate tests use the names above; `check:storage` finds them by name.
- `STAGE_PG_IMAGE` in `mise.toml` is the one Postgres pin (latest major, one
  exact tag), shared with the compose example (`examples/postgres`);
  `check:storage` fails when they differ.
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
  `STAGE_TEST_DATABASE_URL` set (`check:postgres`), a fresh Postgres database
  per engine (`dbtest/engine_postgres.go`, `postgres/pgtest`). The other
  `internal/db` and `internal/api` tests use `dbtest.DB` and stay on SQLite
  under the tag: they hold SQLite SQL.
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
   precision, don't depend on collation beyond lowercase ASCII (the
   `Search*` cases are the exception: Unicode case-insensitivity is part of
   the [search](#search) contract), and give ordering a documented
   tiebreak.
5. Run `mise run check` (`check:storage`; `check:postgres` runs because db
   code changed).

## Data copy and cutover

`stage db copy --from <sqlite path> --to <postgres url>` moves a SQLite
database into Postgres. It needs a binary with Postgres support, so it runs
from the Docker image; any other binary exits 78 before touching anything.

| Path | Owns |
| --- | --- |
| `cmd/stage` (`db.go`, `db_postgres.go`) | the command: flags, the data dir lock, opening both engines, output, exit codes |
| `internal/db/store` (`copy.go`) | the copy ports: `CopySource`, `CopyTarget`, `CopyLoad`, `CopyKind` |
| `internal/db/sqlite` (`copy.go`) | the read-only source and the SQLite-encoding conversions |
| `internal/db/postgres` (`copy.go`) | the target: state, schema in foreign-key order, the load transaction |
| `internal/db/datacopy` | the rules below and the checksums; no SQL, no driver |

- **Offline:** the command takes the source data dir's `stage.lock` (the
  directory holding the file) and holds it for the whole copy: a running
  server there exits it 75, and no server can start on the source meanwhile.
  It also holds the target's [ownership lock](#single-owner): a server on
  the target exits it 75, and none can start until the copy ends.
- **Source untouched:** opened read-only (`mode=ro`, `query_only`). Without a
  `stage.db-wal` (a cleanly stopped server leaves none) it opens
  `immutable`, so no `-wal` or `-shm` file appears; with one (a crashed
  server) it reads the WAL's commits and may create a `-shm` file. Nothing
  writes the database file.
- **Versions:** the source must be at this binary's schema head. Below it,
  start this image once on the SQLite database (it migrates, with its
  [backup](#backups)), stop it, then copy; above it, use a newer image. The
  target must be either fresh (no tables: the copy migrates it to the head) or
  at the head holding only default rows (user 1, machine 1, the
  preferences row, empty settings on machine 1, status rows with both
  fields unset, and the personal installation row). A previously started
  empty personal target takes the source installation ID. The copy refuses
  other versions, other rows, and tables
  without a Stage schema. Refusals write no rows; only a schema mismatch (below) is found
  after a fresh target was migrated.
- **Schemas:** both sides must hold the same tables and columns, apart from
  engine-internal tables (SQLite's `sqlite_*`, `goose_db_version`, the FTS5
  tables and their shadow tables: the Postgres trigram indexes build from
  the copied rows). The target's column types decide each value's encoding.
- **Copy:** one Postgres transaction: clear the seeded rows, copy every table
  in foreign-key order (parents first, from the target's catalog) with its
  IDs, converting TEXT time to `timestamptz` (UTC, microseconds), INTEGER
  0/1 to `boolean`, TEXT JSON to `jsonb` (queried) or `text` (evidence, bytes
  kept) per [Encodings](#encodings), `BLOB` to `bytea`. Text must be valid
  UTF-8 without NUL. A value that does not convert fails the copy, naming the
  table, column and row. Identity sequences restart past
  `max(sqlite_sequence, highest ID)`, so IDs SQLite issued and deleted are
  never reissued (`AUTOINCREMENT`'s rule). No legacy transforms: the source
  is at the head, where #417 has already dropped the
  [legacy columns](#legacy-columns-and-compatibility-triggers).
- **Auth material:** preserve account IDs, identity mappings, password
  verifiers, roles, account status, access audit records, and the installation
  ID and sticky mode. Copy zero rows from `auth_sessions` and `auth_grants`.
  The target starts with interactive and agent credentials and setup, invite
  and reset links invalidated. Existing worker identity and pairing survive.
  Authentication matches the requested audience and a currently active
  account. Agent credential rotation revokes that account's previous
  agent sessions and creates the replacement in one locked transaction.
  Browser and CLI sessions keep their own lifetimes. Password replacement
  and account disable revoke all audiences for the affected account.
- **Verify:** inside the same transaction, before committing, compare
  durable rows with the source and confirm the two transient tables are
  empty. Verification uses row count and a checksum (sha256
  over the sorted sha256 of each row's canonical encoding, so engine row
  order and collation don't matter; times compare as UTC microseconds,
  queried JSON by value since `jsonb` reorders keys and normalizes numbers,
  everything else by bytes). Any error or mismatch rolls the transaction
  back: the target is left as it was (or migrated with only its seeds, when
  it was fresh), and rerunning is safe.
- **Output:** one line naming the source, schema, table and row counts, then
  one line per table (rows, checksum prefix, `ok`). Never the URL.
- **Exit:** 0 copied and verified; 2 usage; 75 the source data dir or the
  target is held by a server; 78 no Postgres support or an invalid `--to`;
  1 refused, failed or mismatched (the target rolled back).
- **Tests** (`check:postgres`): `datacopy/roundtrip_postgres_test.go` seeds
  every table of a SQLite database with edge values (and fails when a table
  has no rows, so a new table needs a seed), copies it into a fresh database,
  compares `db.Store` reads on both engines, checks evidence bytes, new IDs
  past the high-water mark, the refusals and the rollback, and that the
  source directory is unchanged; `cmd/stage` runs the command (held data
  dir, owned target, old source). Coordinator rules run against fakes in
  `check:go`.

Treat database backups and copies as secrets. After restoring a backup,
keep the server stopped and run `stage db invalidate-access` as the
installation owner. Set `STAGE_DATA_DIR` explicitly to the restored server's
data directory, including for a managed installation. For the Postgres
image, also set `STAGE_DATABASE_URL` to the restored database. The command
requires an existing database and applies pending supported migrations
under the usual backup policy. It clears restored browser, CLI and agent
sessions, session-bound push subscriptions and outstanding setup, invite
and reset links. It preserves the installation ID, account identities,
passwords, roles, shared mode and worker pairings. Resume service once
invalidation succeeds. A restored installation in `setup_pending` needs a
[fresh local-owner setup link](runtime.md#shared-setup-and-owner-recovery).
`stage db copy` invalidates transient credentials in the target during the
copy. Ordinary restart keeps valid credentials. See
[ADR 0009](adr/0009-shared-access.md).

### Cutover

Plan a short write outage: the server is down from step 1 to step 6.

1. **Stop the server** and keep it stopped (Kubernetes: scale to 0; compose:
   `docker compose stop <service>`). The copy refuses while it holds the data
   dir.
2. **Back up** `<data dir>/stage.db`, and `stage.db-wal` if present, while
   stopped (copy the files). The copy leaves them untouched; this is for
   anything else that goes wrong.
3. **Prepare the target:** an empty Postgres database the server's role owns
   (or holds `CREATE` on: migrations create `pg_trgm`), with a UTF-8 ctype
   ([Search](#search)). Use the same image version for every step below.
4. **Bring the source to this image's schema** if `stage db copy` reports it
   older: start the image once on SQLite (`STAGE_DATABASE_URL` unset, same data
   dir), wait for `/api/health`, stop it.
5. **Copy:** run the image with the data dir mounted:
   `stage db copy --from <data dir>/stage.db --to <url>`. Require exit 0 and
   `verified`. On a refusal or a failure, fix the cause and rerun: a failed
   copy leaves the target empty.
6. **Start on Postgres** with `STAGE_DATABASE_URL=<url>` and the same data
   dir. Check that `/api/health` reports `"engine": "postgres"` and the
   expected schema, and that `stage status` prints `database ok (postgres)`.
   Spot-check recent tasks and journals in the app.

**Rollback:** stop the server, unset `STAGE_DATABASE_URL`, start it: it runs
on the SQLite `stage.db` in the data dir, which the copy never wrote. Writes
made on Postgres after the cutover are not copied back (there is no reverse
copy); to retry the cutover later, drop and recreate the Postgres database
first, since a copy refuses a target holding data.

## Project identity

Migration 0038 is paired for SQLite and Postgres. It adds attested file host,
canonical path, Git common directory, inode hint, evidence time and source
to `external_projects`, alongside the remote and subpath. Evidence source
is empty, `harness` or `machine`. Existing rows keep legacy evidence until
refreshed. The migration lowercases the legacy remote and marks workspace
rows with active bindings as bound. It backfills active bindings as primary
and maps the former binding origin to `rule` (`imported` becomes `new`,
`path` becomes `local`). It preserves project IDs and archive state.

`workspace_binding_active` keeps one active owner for an exact workspace
tuple (connection, external ID, reported path). `project_binding_primary`
permits several active bindings on a project/connection and enforces at
most one active primary. The store assigns or promotes a primary when
there are active bindings. Tasks and schedule templates store a nullable
`binding_id`. A launch attempt stores its own pinned destination.

| Stored field | Values and use |
| --- | --- |
| `project_bindings.status` | `available`, `missing` or `changed`. An available binding matches a current workspace row. A changed binding has a different observed path for its external ID. A missing binding has no available workspace at the recorded path or a replacement path for that ID. |
| `project_bindings.rule` | `manual`, `new`, `remote`, `local`, `moved`. Records how the binding was made. |
| `external_projects.resolution` | Empty for unresolved legacy or unavailable unbound rows, otherwise `bound`, `join`, `new` or `review`. Derived from workspace evidence, bindings and declines. |
| `external_projects.resolution_reason` | Empty or `ambiguous`, `declined`, `moved`, `suggested`. The resolution also stores the resolver rule and candidate project IDs. |
| `external_projects.historical_project_id` | Nullable project used when a human tracks a session in this exact workspace without a current binding. Paired migration 0046 adds it. |

`workspace_declines` stores both the identity and external keys per
connection. The migration backfills older declines from external IDs and,
for rows with a path, any known remote and subpath. `project_aliases`
retains merged IDs and titles, `project_merge_dismissals` stores unordered
pairs by ascending project ID, and `project_events` records moves, merges
and splits. Data copy includes these four tables. Migration 0038 drops
`shared_paths`, `projects.archive_owner`, `project_bindings.origin` and
`external_projects.declined_at`.

The identity transaction lock is taken before row locks. On PostgreSQL it
is `pg_advisory_xact_lock`, scoped to one transaction and shared by identity
writes across all connections. SQLite relies on its single writer.
Automatic import reads the evidence, plans, applies and updates derived
resolutions in one write transaction. Confirmation recomputes the plan
under that lock. Task and schedule creation, edits, duplication and spawning
use the same lock to serialize the selected workspace with identity changes.
Session tracking uses the exact workspace's current project when bound.
Otherwise it reuses its recorded historical project. When recording that
association for the first time, it uses current group membership, a unique
strong match, or a new project. The recorded association survives workspace
refreshes and group membership edits. Task and schedule creation uses the
same association when the caller selects a workspace without naming a
project. The caller's exact workspace selection supplies the launch pin.

Migration 0047 adds nullable `workspace_pin` to tasks and schedules on both
engines. With the owner's `connection_id`, its native ID and root identify one
catalog row. Legacy selections backfill when the recorded binding and catalog
prove the key. Prior claims provide the exact key for later retries. Other
work stays unresolved until edited. The database copy includes these fields
and the attempt rows.

Launch claims take the identity lock before the task lock, compare the
verified selection with the saved pin, and check its current catalog row.
Each attempt keeps its original connection, path, native workspace ID and
any binding revision. A nullable binding ID supports unbound destinations.
Task readers use the attempt's destination for launch history.

Replacement and retirement leave task and schedule pins on the previous
connection. Backlog work and schedule templates can be edited to select a
current destination. Forgetting a connection retains historical references
and deactivates its bindings.

Review reads the undismissed project pairs from the per-pair dismissal records.
See [workspace discovery](discovery.md) for rule priority
and [the API](api.md#workspace-discovery-and-project-identity) for previews.

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
| `tasks.launch_request_id`, `handoff` | Launch intents, task API | Keep | None |
| `tasks.settled_at` | Historical finish timestamp | Dropped (0035) | Backfill missing `finished_at` when converting state `settled` to `done` |
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
| `t3_connection`, `opencode_settings`, `claude_settings`, `codex_settings` | Legacy harness setup | Dropped (0036) | Copy settings, secrets and sync status into the generic tables |

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
| `tasks`/`schedules.execution_config`, `harness_connections.new_task_config`, `notification_preferences.notify_events` | Queried (decoded) | `jsonb` |
| `harness_settings.settings`, `harness_secrets.value` | JSON objects and opaque values compared by value | `jsonb` |
| `execution_attempts.intent`, `checkpoint`, `observed_session` | Queried (decoded; the checkpoint is opaque adapter state) | `jsonb` |
| `tasks`/`schedules.model_options`, `threads.pull_requests`, `threads.recent_runs`, `question_responses.answers` | Dropped (see Columns) | none |
| `tasks.workspace`, `schedules.workspace` | Dropped (0034, #420; moved into `execution_config` options by 0033) | none |

Session links come from the latest observation in either placement. An
unconfirmed attempt retains its saved resume command when its observation
could not be stored. Existing attempt and acceptance evidence keep their
stored format.

`journal_entries.text` and `journal_launch_context.instructions` are plain
text, not JSON, and stay `text`.

`execution_observations.observation` carries the session's `usage` when the
harness reports one (#380, no migration: a new key in the JSON). Usage is
cumulative per session, so `PutObservation` keeps the stored usage when a
new observation has none.

The same JSON carries optional display `facts`. Old observations decode
without facts. These values require no schema migration on either engine.

Stage stores its sample capture time as `stageCapturedAt` in the stored
observation JSON to order snapshots with equal harness timestamps. Older
rows use the storage receipt time in `observed_at`. The adapter and API
observation payloads keep their existing format.

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
