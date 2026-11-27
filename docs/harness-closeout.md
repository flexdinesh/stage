# Harness foundation closeout

[ADR 0008](adr/0008-harness-foundation.md) records the approved contracts and
the [P2](adr/0008-harness-foundation.md#p2-review-checklist) and
[P3](adr/0008-harness-foundation.md#p3-scoped-inventory) review checklists.
The [authoring guide](harnesses.md) specifies the registration footprint,
remaining per-kind integration points and [Amp evidence](harnesses.md#amp-mapping).

## Reviewed history

Stage tree 204's master plan is journal entry 4505. Layer 01 recorded the
baseline `c32718e73b61e7befe7e6c646b4dc900fbeba32e` in entry 4518. The
initial integrated review used `30f115f579e7ba5b9d65844b2b5c30380c2dcf31`,
the freshly fetched `origin/main` when layer 11 began. GitHub reports each
of the ten predecessors merged into `main`. Each squash result below is
reachable from that ref. Review used each squash against its first parent
for attribution and the integrated revision for behavior. PRs 234 and 236
also landed in the interval and were present in the integrated revision.

| Layer / Task | PR | Squash on `main` | Recorded verification and decisions |
| --- | --- | --- | --- |
| 01 / 425 | [#230](https://github.com/flexdinesh/stage/pull/230) | `7c28c36a104f23645320300473cdc369f7dec777` | Check and link audit. Postgres gate skipped for docs-only. No product build or platform smoke trigger. Journal 4523. |
| 02 / 426 | [#231](https://github.com/flexdinesh/stage/pull/231) | `a84343a610f873312987f42b95203aefd6df979c` | Check, both engines from seeded fixtures, build, 16 distribution assets and migration UI at phone and laptop widths in both themes. Journal 4543. |
| 03 / 427 | [#232](https://github.com/flexdinesh/stage/pull/232) | `2388bede578b7092bb654fe7e99410b775e72791` | Check, build, distribution, Linux service smoke, scoped race tests and Machines UI. Serial rerun passed after a startup timeout during the initial concurrent race run. Journal 4554. |
| 04 / 428 | [#233](https://github.com/flexdinesh/stage/pull/233) | `fc98730d18f2becaf13d5d2a799a254cb64c936e` | Serial check with both engines, build, distribution, Linux service and installer smoke, scoped race tests and four setup panels. The user confirmed fresh OpenCode shows the Stage auto-completion preference after first connection and preserves existing preferences. Journals 4568 and 4569. |
| 05 / 429 | [#235](https://github.com/flexdinesh/stage/pull/235) | `61cfa6407530c1bca98f1d38b5f1e55380f02c31` | Serial check, both engines, build and distribution. The approved picker eligibility uses `localProcesses` as recorded in D8. Journal 4641. |
| 06 / 430 | [#237](https://github.com/flexdinesh/stage/pull/237) | `17925ba66b1c353d863804f5e51f8250d92b064f` | Check, both engines, build, distribution and browser contract scenarios. Decision 4665 adds paired migration 0037 for sanitized connection facts that remain visible offline. Journal 4682. |
| 07 / 431 | [#238](https://github.com/flexdinesh/stage/pull/238) | `be6595c564880d94161dfcd196fc576ae1c68c42` | Check, concurrent Postgres import, both migrations, build, distribution, Docker build and workspace UI. The user confirmed D9 pathless identity stays connection-scoped even with remote evidence. Journals 4721 and 4740. |
| 08 / 432 | [#239](https://github.com/flexdinesh/stage/pull/239) | `0b855c73a984e2a862b4e31a21760921bfd8a548` | Check, both engines, build, distribution and browser workspace, discovery, merge, split and pin recovery scenarios. Journal 4770. |
| 09 / 433 | [#240](https://github.com/flexdinesh/stage/pull/240) | `1ea4b650817783f6604c382f91b5b8e8edc6a3c3` | Check, both engines, headless desktop, 30 remote contract subtests and distribution. First check exposed an invalid retired-connection fixture, corrected before merge. Package installs skipped. Journal 4779. |
| 10 / 434 | [#241](https://github.com/flexdinesh/stage/pull/241) | `30f115f579e7ba5b9d65844b2b5c30380c2dcf31` | Check, both engines, distribution, Linux service smoke and race tests. Real channel tests cover offline restarts, lost acknowledgements and offline cache. The pre-commit rollback uses an injected failure. Package installs and macOS launchd skipped. Journal 4791. |

The distribution checks in layers 02–10 ran Linux amd64 CLI and desktop
archive smoke natively. Linux arm64 and macOS assets were cross-built.
Desktop GUI e2e was unavailable on those runs because the host lacked a
display and WebKitGTK 4.1. All layers used isolated test data. The layer 11
PR, its validated head, its squash and the final check ledger are recorded
in the layer 11 handoff.

## Contract trace

1. HTTP `POST /api/tasks`, CLI `stage task add` and MCP `create_task` resolve
   a Stage project and choose a connection with an optional workspace binding.
   `GET /api/connections/{id}/capabilities`, `stage models ls` and `list_models`
   expose declarations, including workspace-specific launch choices. The
   [API handlers](../server/internal/api/handlers_execution.go) validate the
   selected destination and the [launch coordinator](../server/internal/coordinator/launch.go)
   freezes it with the launch configuration, accepted need IDs and journal
   instructions before dispatch. The local host and worker execute the same
   `Launch` contract. [Lost reply and restart tests](../server/internal/engine/contract_remote_test.go)
2. The adapter returns a connection-scoped session reference. The local and
   worker observers feed the same transactional ingest used by external
   bridge pushes. Interaction routes require the declared operation and trait
   for the owning connection. Where unavailable, task controls show the
   harness reason and offer its supplied web, native or resume destination.
   [Worker channel tests](../server/internal/engine/observer_channel_test.go),
   [interaction placement test](../server/internal/engine/contract_v2_test.go),
   [API interaction tests](../server/internal/api/interaction_test.go)
3. Task completion in Stage records accepted evidence. Observation and
   archive only affect acceptance through the connection's closure policy.
   The dependent launch pins and uses accepted evidence even when the source
   goes offline. MCP and CLI surface the same Stage task and handoff reads.
   [Acceptance and dependent launch](../server/internal/api/acceptance_test.go),
   [handoff tests](../server/internal/api/handoff_test.go)
4. A schedule carries its explicit workspace into the spawned task and
   launch intent. If a selected workspace goes missing or its connection is
   replaced, a pinned occurrence waits for a new choice. Unpinned work uses
   the available primary. The same workspace resolver handles refresh,
   adoption, review and bindings. An adopted task attaches to a session that
   already exists. Replacement retires the old connection, preserves its
   evidence and rejects pushes to it. [Binding choice tests](../server/internal/db/storetest/binding_choices.go),
   [adoption tests](../server/internal/api/adopt_test.go),
   [bridge tests](../server/internal/api/observations_test.go),
   [identity tests](../server/internal/engine/identity_test.go)

The migrated fixtures exercise the pre-v9 to schema 39 chain on both engines,
retaining historical acceptance evidence and settings. Stage owns project IDs
and aliases, including across same-repository workspaces. Session IDs,
worktrees and processes remain adapter concerns. [Storage contract](storage.md),
[SQLite steps](../server/internal/db/stepwise_migration_test.go),
[Postgres steps](../server/internal/db/postgres/stepwise_test.go),
[project lifecycle tests](../server/internal/db/storetest/project_lifecycle.go)
