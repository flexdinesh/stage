# Harness workspace and project group closeout

[ADR 0010](adr/0010-harness-workspaces-and-logical-projects.md) records the
decision accepted in Stage task #461. Tree #207 delivered it. The nine
predecessor PRs were verified merged into `main` and reviewed against the ADR.

| Task | Merged PR | Contract verified |
| --- | --- | --- |
| 462 | [#253](https://github.com/flexdinesh/stage/pull/253) | Harness catalogs, logical groups, exact selectors and shared route ownership. |
| 463 | [#255](https://github.com/flexdinesh/stage/pull/255) | Connection-scoped inventory includes nested, plain and unbound workspaces. Cached evidence survives refresh failures. |
| 464 | [#258](https://github.com/flexdinesh/stage/pull/258) | Exact pins flow through task and schedule writes and launch claims. Migration 0047 retains resolvable legacy pins and frozen attempts. |
| 465 | [#259](https://github.com/flexdinesh/stage/pull/259) | Migration 0048 adds group membership. Strong evidence groups rows, ambiguous rows stay separate, and membership edits preserve recorded work. |
| 466 | [#254](https://github.com/flexdinesh/stage/pull/254) | Claude searches its current roster. Capability failures preserve named lookup. |
| 467 | [#256](https://github.com/flexdinesh/stage/pull/256) | T3, Codex and OpenCode search via local adapters and negotiated worker channels. |
| 468 | [#260](https://github.com/flexdinesh/stage/pull/260) | Creation, duplication and scheduling choose a connection and exact workspace. Unresolved backlog remains available. |
| 469 | [#261](https://github.com/flexdinesh/stage/pull/261) | Projects uses logical groups and reversible workspace edits. Historical URLs and snippets stay reachable. |
| 470 | [#257](https://github.com/flexdinesh/stage/pull/257) | Track session searches, previews and confirms within one connection. Named lookup and adoption eligibility remain available. |

Task #471 retires the routing inputs and directs old callers to exact selectors
or membership edits. New exact selections without a project use the current
group. Existing work keeps its recorded project and destination. The shared
worker workflow exposed a malformed Postgres watch query. Correcting it
restored delivery of the worker's settled state and final reply.

## Validation

| Check | Result |
| --- | --- |
| `mise run check` | Passed server, storage, SQLite and Postgres suites, browser and desktop host tests, headless desktop integration, website and release tooling. |
| [Upgrade fixture](../server/internal/db/legacy_project_upgrade_test.go) | SQLite and Postgres upgraded a personal 0046 database through setup pending and claim. Both retained user 1, aliases, snippets, exact pins, unresolved work, frozen attempts, disconnected history, provenance and paired workers. |
| [Shared worker workflow](../server/internal/serve/shared_workflow_test.go) | Passed on fresh SQLite and Postgres. Two members shared work, selected and repaired the worker pin, browsed and tracked a listed session and kept separate inbox receipts. Agent roster access was denied. Exact agent launch and named adoption passed. The schedule kept its creator and recorded the member who ran it. |
| [Claude isolation](../server/internal/serve/shared_claude_workflow_test.go) | Discovered a nested folder, searched by title, tracked the listed session and launched in the exact folder. Both task histories opened after the connection was forgotten. |
| [Roster contracts](../server/internal/api/session_search_harnesses_test.go) | T3, Claude, Codex and OpenCode passed roster coverage. Local and worker tests preserved named lookup when the capability or worker protocol could not browse. |
| Chromium and Impeccable | Light laptop and dark phone layouts, title search and tracking passed. A delayed roster response was discarded on browser sign-out and during a simulated desktop target switch between two real isolated servers. The design detector returned zero findings. |
| `mise run build` and `TARGETS=all mise run dist` | Passed. The release verifier checked 16 artifacts. Linux amd64 CLI and desktop archives passed native smoke. Linux arm64 and macOS were cross-built. Native desktop GUI testing requires a display and WebKitGTK 4.1, which this host lacks. |
| `mise run opencode:live` and `mise run opencode:workflow` | Passed against isolated OpenCode V2, including lost launch replies, scoped capabilities, native MCP, named adoption, a scheduled run and restart. |
