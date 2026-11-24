# ADR 0004: Queue port (River evaluated, not adopted)

- **Status:** accepted (2026-11-24), Stage task #408. Design only: no code,
  no schema. Refines the Queue part of
  [ADR 0003](0003-storage-adapters.md) decision 4. The contract stays
  [storage.md](../storage.md#design-only-queue-events-leases); Events and
  Leases are #409.
- **Sources:** River v0.51.2 (2026-11-09) source and the riverqueue.com docs,
  read 2026-11-24. Source links pin the `v0.51.2` tag.

## Context

ADR 0003 left the Queue design-only: enqueue inside the caller's `Write`,
claim with `SKIP LOCKED`, retry with backoff, unique and scheduled jobs, and a
single-process SQLite fallback. #408 asks whether River should provide it, and
which background loops would move.

Stage's background work today (`server/internal`, started by `serve`, one
process):

| Loop | Trigger | Durable state | Shape |
| --- | --- | --- | --- |
| Release runner (`release/runner.go`), one per active connection | 10 s ticker; kicked by transitions, acceptance and tree changes | task state; the claim is `TransitionTask` (begin attempt) | reconciler over domain rows |
| Schedule runner (`schedule/runner.go`), one per active connection | 15 s ticker | `schedules.next_run_at`, advanced in `FireSchedule`; backlog `start_at` | reconciler |
| Sync (`sync/service.go`) | per connection: 5 s poll, stream and watch, 5 min full sync, kicks; 10 min catalog | observations and attempts; launch retry in `execution_attempts.retry_at` and `retry_count` | per-connection harness loop |
| Moved-connection retirer (`serve/moved.go`) | 5 min, kicks | connections | reconciler |
| Attachment sweep, notification prune (`serve/serve.go`) | hourly | none (idempotent deletes) | periodic |
| T3 token renewal (`t3/mcpclient.go`) | 1 h; backoff in memory | the credential | local-machine timer |
| Notifier and Web Push (`notify/notify.go`) | in-memory channel (256, drops when full) | last result per subscription | one-shot external effect, no retry |

Only push delivery is job-shaped: a one-shot external effect that should
survive a restart and be retried. The others derive their work from domain
rows on every pass. On more than one replica they need one holder at a time
(Leases, #409), not a queue.

## River, evaluated

| Question | Finding |
| --- | --- |
| Port fit on Postgres | Good. `InsertTx` takes the caller's `pgx.Tx`; workers claim with `SKIP LOCKED`; 25 attempts by default with `attempt^4` backoff and a pluggable policy ([retry_policy.go][r-retry]); `UniqueOpts` by args, period, queue and state, backed by a `unique_key` column and a partial unique index ([006_bulk_unique][r-unique]); `ScheduledAt`; LISTEN/NOTIFY wakeups or `PollOnly` ([client.go][r-client]); a rescuer re-runs jobs stuck for over 1 h ([job_rescuer.go][r-rescuer]). |
| Periodic jobs | Started by the elected leader, kept in memory, no strong durability ([periodic_job.go][r-periodic]). Durable periodic jobs are River Pro ([Pro][pro]). |
| Maturity | Pre-1.0: v0.1.0 in March 2024, v0.51.2 on 2026-11-09, a minor release every 2 to 4 weeks ([CHANGELOG][r-changelog], [releases][r-releases]). Two core maintainers. |
| License | MPL-2.0 ([LICENSE][r-license]). River Pro is a proprietary EULA; shipping it inside a distributed product needs its Appliance License ([Pro license][pro-license]). |
| pgx v5 | `riverpgxv5` is the primary driver. Core's `go.mod` requires pgx v5 ([go.mod][r-gomod]), though core code does not import it. `riverdatabasesql` is Postgres-only. |
| SQLite driver | `riversqlite` since v0.23.0 (June 2025), with "minimal real world vetting" ([CHANGELOG][r-changelog]); the package says it is "in early testing" ([driver][r-sqlite-driver]) and the docs call it experimental ([SQLite docs][docs-sqlite]). Tested with modernc; the docs set `SetMaxOpenConns(1)`. No NOTIFY: an outbox table polled every 50 ms ([listener][r-sqlite-listener]). `InsertTx` takes a `*sql.Tx`. |
| Binary size | Measured with go1.27.3, `CGO_ENABLED=0`, `-ldflags '-s -w'`: modernc alone 6.53 MB; with `river`, `riversqlite` and `rivermigrate` 8.43 MB. About +1.9 MB on desktop and CLI binaries; no pgx linked. Postgres builds already carry pgx. |
| Migrations | Its own migrator (`rivermigrate`) and `river_migration` table, keyed by line and version ([river_migrate.go][r-migrate]). `river migrate-get` exports SQL, and the docs show embedding it in goose ([migration docs][docs-migrations]). River keeps one version number per change on both engines, with a no-op where an engine needs nothing (version 8: [SQLite][r-sqlite-008], [Postgres][r-pg-008]), so each River schema change would map onto one Stage version on both engines. Its Postgres schema uses an enum, `BIT(8)`, a plpgsql function, a trigger and UNLOGGED tables ([002_initial_schema][r-schema]). |
| Alternatives | [gue][gue] (MIT, Postgres only), [neoq][neoq] (MIT; memory, Postgres, Redis), [goqite][goqite] (MIT; SQLite, also Postgres), [backlite][backlite] (MIT, SQLite only). Each adds a dependency and its own schema for one consumer. |

## Decision

1. **No River, and no other queue library, for now.**
2. **The Queue is a capability port in `internal/db/store`.** Each adapter
   implements it over its own `jobs` table, in its engine's native types. A
   migration with one version on both engines creates the table when the
   first consumer lands, not before.
3. **One-shot jobs only**, optionally run at a later time. No periodic or
   cron jobs: periodic work and the reconcilers run under Leases (#409).
4. **First consumer: Web Push delivery.** It becomes durable and retried.
5. **Revisit River only for the Postgres adapter, and only if a need outgrows
   the port:** throughput beyond one replica, workflows, rate or concurrency
   limits. River has no claim API, so a swap replaces the adapter's Queue and
   the runner together. Callers of `Enqueue` do not change.

## Queue port

```go
// package store

type Job struct {
	ID          int64
	Kind        string
	Args        json.RawMessage
	Attempt     int // 1 on the first run; each claim increments it
	MaxAttempts int
	RunAt       time.Time
	LastError   string
}

type EnqueueOpts struct {
	RunAt       time.Time // zero: now
	MaxAttempts int       // zero: the kind's default
	UniqueKey   string    // empty: none; else at most one live job per (kind, key)
}

type Queue interface {
	// Enqueue inserts a job inside the caller's Write. Claims see it only
	// after commit. A UniqueKey that matches a live job returns that job's
	// id and inserted=false.
	Enqueue(ctx context.Context, tx Tx, kind string, args json.RawMessage, opts EnqueueOpts) (id int64, inserted bool, err error)

	// Claim marks up to n due jobs of the given kinds running until
	// now+lease and increments their attempt. Due: available with
	// run_at <= now, or running with an expired lease (rescue). Oldest
	// run_at first, ties by id.
	Claim(ctx context.Context, kinds []string, n int, lease time.Duration) ([]Job, error)

	// Complete marks the job done inside the caller's Write, so a handler's
	// own writes and the completion commit together.
	Complete(ctx context.Context, tx Tx, id int64, attempt int) error

	// Fail records cause. A non-zero retryAt makes the job available again
	// at that time; zero discards it.
	Fail(ctx context.Context, id int64, attempt int, cause string, retryAt time.Time) error

	// Prune deletes done and discarded jobs that finished before the given time.
	Prune(ctx context.Context, before time.Time) (int, error)
}
```

- **States:** `available`, `running`, `done`, `discarded`. A retry is
  `available` with a later `run_at`. Live means `available` or `running`.
- **Fencing:** `Complete` and `Fail` match `(id, attempt)` on a running job.
  Otherwise they return a conflict: the job was rescued and claimed again,
  and the stale worker drops its result. Delivery is at least once, so
  handlers are idempotent.
- **Unique:** on both engines, a partial unique index on `(kind, unique_key)`
  over live jobs, `INSERT ... ON CONFLICT DO NOTHING`, then a read of the
  existing id. A key frees up once its job is done or discarded.
- **Claim:** Postgres runs one `UPDATE ... WHERE id IN (SELECT ... ORDER BY
  run_at, id LIMIT n FOR UPDATE SKIP LOCKED) RETURNING ...`. SQLite runs the
  same select and update in one `BEGIN IMMEDIATE` `Write`.
- **Time** comes from Go ([Encodings](../storage.md#encodings)); no SQL clock.
- **Runner** (outside `internal/db`, for example `internal/jobs`; no business
  rules): a handler per kind with its max attempts, timeout and backoff
  policy (exponential with a cap; the numbers are fixed with the first
  consumer). It claims, runs, then completes or fails. It wakes on Events
  (#409): Postgres `NOTIFY` after commit, SQLite an in-process kick after the
  `Write` commits, plus a polling fallback. It prunes hourly. On several
  replicas every runner claims safely (`SKIP LOCKED`), so the Queue itself
  needs no lease.

`jobs` table (indicative; the first consumer's migration fixes it):

| Column | SQLite | Postgres |
| --- | --- | --- |
| `id` | `INTEGER PRIMARY KEY AUTOINCREMENT` | `bigint GENERATED BY DEFAULT AS IDENTITY` |
| `kind`, `state`, `last_error` | `TEXT` (`state` with a `CHECK`) | `text` (`state` with a `CHECK`) |
| `args` | `TEXT` (JSON) | `jsonb` |
| `attempt`, `max_attempts` | `INTEGER` | `integer` |
| `run_at`, `lease_until`, `created_at`, `finished_at` | `TEXT`, fixed-width UTC | `timestamptz` |
| `unique_key` | `TEXT`, nullable | `text`, nullable |

Indexes: one for claims on `(state, run_at, id)` (partial over live jobs on
Postgres), and the partial unique index above.

`storetest` cases, written with the first consumer: an enqueue rolled back
with its `Write` is never claimed; a unique key dedupes while its job is live
and frees up after; claim order and `run_at`; concurrent claims never share a
job; an expired lease is rescued with `attempt + 1`; a stale `Complete` or
`Fail` conflicts; retry and discard; `Prune`.

## Loops

| Loop | Outcome |
| --- | --- |
| Web Push delivery | First Queue consumer: one delivery job per subscription, enqueued with the notification it delivers; a 404 or 410 deletes the subscription, as today |
| Release runner | Stays a reconciler. The claim stays the `TransitionTask` row lock; one Lease per connection (#409); kicks through Events |
| Schedule runner | Stays a reconciler. `next_run_at` stays the durable state (one spawn per fire time); one Lease per connection |
| Sync, moved-connection retirer | Stay per-connection loops under a Lease (#409) |
| Launch retry | Stays in `execution_attempts`: already durable and fenced by `revision` |
| Attachment sweep, notification prune | Stay periodic, under one Lease |
| T3 token renewal | Stays in-process on the machine that holds the credential |

The #408 brief named the schedule runner, release runner, sync and sweeps as
loops to move. They don't move to the Queue: they are reconcilers or periodic
passes, and their multi-replica need is a single holder, which #409 designs.

## Rejected alternatives

| Alternative | Why rejected |
| --- | --- |
| River on both engines | +1.9 MB in every desktop and CLI binary; an experimental SQLite driver; its 50 ms outbox poll and maintenance writes contend with our single writer, and its docs pin one connection where we run WAL readers beside the writer; a second migration line to fold into goose. |
| River on Postgres, own table on SQLite | Two queue semantics (River's states, snooze, 25 attempts with `attempt^4`) to keep equal in `storetest`; a pre-1.0 API on the production engine; River's leader election beside our Ownership and Leases; its schema changes become Stage migrations. All for one consumer. |
| River's migrator beside goose | Two version tables and two orders; breaks one version on both engines. |
| Periodic jobs in the Queue | Schedules already have a durable `next_run_at`; River's OSS periodic jobs are not durable; reconcilers need a single holder, which a queue does not give. |
| Build the Queue now | Nothing needs it until push delivery is made durable or a second replica exists. |

## Consequences

- We own a small amount of queue code (claim, rescue, fencing, backoff) that
  River has already tested; the `storetest` cases above are its proof.
- No new dependency: desktop and CLI size and the migration rule are
  unchanged.
- Push delivery stays best effort until it moves to the Queue.
- Multi-replica readiness rests on Leases and Events (#409) more than on the
  Queue.

[r-retry]: https://github.com/riverqueue/river/blob/v0.51.2/retry_policy.go
[r-unique]: https://github.com/riverqueue/river/blob/v0.51.2/riverdriver/riverpgxv5/migration/main/006_bulk_unique.up.sql
[r-client]: https://github.com/riverqueue/river/blob/v0.51.2/client.go
[r-rescuer]: https://github.com/riverqueue/river/blob/v0.51.2/internal/maintenance/job_rescuer.go
[r-periodic]: https://github.com/riverqueue/river/blob/v0.51.2/periodic_job.go
[r-changelog]: https://github.com/riverqueue/river/blob/v0.51.2/CHANGELOG.md
[r-releases]: https://github.com/riverqueue/river/releases
[r-license]: https://github.com/riverqueue/river/blob/v0.51.2/LICENSE
[r-gomod]: https://github.com/riverqueue/river/blob/v0.51.2/go.mod
[r-sqlite-driver]: https://github.com/riverqueue/river/blob/v0.51.2/riverdriver/riversqlite/river_sqlite_driver.go
[r-sqlite-listener]: https://github.com/riverqueue/river/blob/v0.51.2/riverdriver/riversqlite/river_sqlite_listener.go
[r-migrate]: https://github.com/riverqueue/river/blob/v0.51.2/rivermigrate/river_migrate.go
[r-sqlite-008]: https://github.com/riverqueue/river/blob/v0.51.2/riverdriver/riversqlite/migration/main/008_job_id_autoincrement.up.sql
[r-pg-008]: https://github.com/riverqueue/river/blob/v0.51.2/riverdriver/riverpgxv5/migration/main/008_job_id_autoincrement.up.sql
[r-schema]: https://github.com/riverqueue/river/blob/v0.51.2/riverdriver/riverpgxv5/migration/main/002_initial_schema.up.sql
[pro]: https://riverqueue.com/pro
[pro-license]: https://riverqueue.com/pro/license
[docs-sqlite]: https://riverqueue.com/docs/sqlite
[docs-migrations]: https://riverqueue.com/docs/migrations
[gue]: https://github.com/vgarvardt/gue
[neoq]: https://github.com/acaloiaro/neoq
[goqite]: https://github.com/maragudk/goqite
[backlite]: https://github.com/mikestefanello/backlite
