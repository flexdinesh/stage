# ADR 0008: Harness foundation v2

- **Status:** Accepted on 2026-11-26 in Stage task #421.
- **Record:** Stage tree #204, MASTER PLAN journal #4505 (supersedes #4498).
  Layer 01 makes this ADR the reference for the remaining layers.
- **Baseline:** `c32718e73b61e7befe7e6c646b4dc900fbeba32e`, recorded in
  tree journal #4518 for layer 11.

## Context

[ADR 0010](0010-harness-workspaces-and-logical-projects.md) supersedes
[D9](#d9) and [D10](#d10). Read-only evidence still informs grouping.
Launches use an exact workspace on the selected connection.

[ADR 0007](0007-harness-boundary.md) establishes the Stage and harness
boundary. At the baseline, adding a harness still requires per-kind storage,
configuration, worker dispatch and composition. The server drives remote
observation over RPC. Import and the match action use different input sets
and rules, and equal path strings can describe unrelated filesystems.
The UI has custom harness panels, with some operation controls offered
without checking availability. The decisions below retain those panels and
reduce the coupling that makes each adapter a product-wide change.

## Ownership at the baseline

Stage allocates project, task, connection, machine, tree, schedule, journal
and acceptance IDs. It records a session under
`{connectionId, externalSessionId}`. The harness owns the external session
and workspace namespaces, even when a launch reserves a session key in
advance. A binding relates a harness workspace on a connection to a stable
Stage project ID. A worker or bridge operates on those scoped references.

| Path | Who creates the session | Who observes it | Who accepts the task | ID ownership |
| --- | --- | --- | --- | --- |
| Direct launch | Stage requests launch. The harness creates the session and its workspace effects. | Stage's coordinator ingests adapter reads or events. | The user accepts in Stage, or Stage records completion under the configured closure policy. | Stage owns the task, attempt and connection. The harness supplies or honors the reserved external session key. |
| Adopted session | The harness already has a session. Stage creates a task linked to it. | Stage reads through the owning connection and follows the same observation contract. | Stage applies the same acceptance rules. Adoption alone records no completion. | The existing external session key stays in its connection's namespace. Stage allocates the task ID. |
| Remote worker | The worker executes the same harness launch through its adapter. The main server owns the launch intent and checkpoints. | At the baseline the main server requests reads via the worker proxy and ingests pushed events. [D11](#d11) moves observation beside the remote adapter. | The main server records acceptance. | Stage owns machine, connection and task IDs. The remote harness owns its external IDs. |
| External bridge | The harness owns the session. A bridge supplies observations for its existing Stage connection. | The bridge pushes to `POST /api/connections/{id}/observations`. Stage validates and ingests; adapter polling reconciles. | The main server records acceptance under the same policy. | The bridge uses a Stage connection ID and the harness's external session ID. Its push allocates no task or session. |

A wrapper declares the origins it leaves in sessions. Stage refuses adoption
through the wrapped harness when a registered wrapper owns the session.
Caller attribution follows the wrapper's declaration. This preserves
[ADR 0007 decision 13](0007-harness-boundary.md#decisions).

## Principles

### P1

**Keep ownership with the system that makes the decisions.** Stage owns
projects, tasks, trees, schedules, journals, acceptance and handoff. A
harness owns its sessions, including their workspaces, worktrees, branches,
conversation, models, permissions, archive and processes. This lets Stage
coordinate work across harnesses while the harness governs execution.
[ADR 0007](0007-harness-boundary.md) establishes this boundary.

### P2

**Make Stage-wide behavior portable.** A Stage feature works across harnesses
or degrades through declared capability. Depending on one harness's extra
would give a Stage workflow different semantics for another harness.

### P3

**Keep each harness's useful extras in scope.** A harness can declare
connection, task and session capabilities, including workspace-scoped
extras where applicable. Keep its settings, setup panel, identity and
panel vocabulary, along with its launch options, operations, actions,
facts and interaction. Users retain what makes their chosen harness useful.

### P4

**Type the decisions Stage makes.** Use a core type when Stage's own logic
branches on it. Declare and relay other data and behavior so adding an
extra does not spread harness semantics through the product.

### P5

**Keep authoring local to the harness.** An adapter, a setup package, one
module registration, tests and docs should suffice. This makes the cost of
adding a harness proportional to its own integration.

### P6

**Run the same code on the local machine and a worker.** Compose the same
modules over placement-specific ports. Parity keeps behavior independent of
where a harness is hosted.

## Decisions

### D1

**Sequence all eleven layers.** Each layer is independently usable and
merges its own PR to `main`. Start each task with a fresh fetch of
`origin/main` after its predecessor has merged, in its own Worktrunk
worktree. This makes the merged base the dependency between tasks.

### D2

**Roll forward as API v9** (introduced by layer 02). The series is breaking
and rolls forward without public compatibility shims. Apply paired SQLite
and Postgres migrations at the next free version when storage changes,
explicitly handle older data, and preserve recorded evidence. Update the
main server and workers together. Removed inputs receive an actionable
error naming their replacement. This keeps one contract while retaining
the meaning of existing records. Worker negotiation is a separate contract
([D12](#d12)).

### D3

**Remove obsolete Stage concepts.** Layer 02 removes the `settled` task
state, legacy PR and run artifacts, and thread vocabulary from the Stage
surface. Migrate `settled` rows to `done` and use acceptance to express
completion. Keep T3's native wording in its own panel and declarations.
Layer 07 removes shared paths and project auto-archive follow. Stage owns
project archival, and a harness closure signal can inform acceptance
without being a Stage state. This leaves execution details with the
harness and completion and archive decisions with Stage.

### D4

**Give every harness the same boundary** (layers 02, 04 and 06). T3-specific
needs belong to its module or a declared policy, as do the needs of other
harnesses. Core behavior based on a kind name makes a second harness
implement the first harness's model.

### D5

**Compose one module per harness** (layer 04). Each module supplies a
descriptor and composition over narrow ports. Serve and worker iterate the
same module list. Store settings JSON per machine and kind and secrets by
kind and key. Each setup package owns its typed settings. Preserve the
visible settings, actions and panels. This removes duplicated composition
and per-harness storage plumbing without changing the setup users rely on.

### D6

**Declare identity policy and keep connection continuity rules** (layer
04). One connection per kind per machine is active. Replacement retires
the old instance; reconnecting the same instance revives its connection.
Modules declare stable identity, continuity proof or moved-retire policy
as appropriate. This preserves history while allowing each harness to
prove instance continuity using its own evidence.

### D7

**Use typed operations for Stage decisions and declarations for extras**
(layer 05 backend and layer 06 UI). Keep launch, stop, adopt, reply,
opening, attention, images, respond and send as typed operations. Declared
actions target a connection, session or project, describe typed fields and
confirmation, and name whether users alone or users and agents may call
them. The adapter validates the opaque input. Declared facts carry a
label, value and tone on connection status and observations. Use one
route, one MCP tool, one CLI command and one renderer for the common
action surface, with facts rendered by the same generic approach. Keep
per-kind panels. This gives each harness a way to expose its extras
without new product branches.

| What needs to happen | Where it belongs |
| --- | --- |
| Stage branches on an invariant such as launch correlation, identity or session ownership | Descriptor policy interpreted by Stage |
| Stage acts on a common capability such as Stop or responding to an approval | Typed operation with availability and reason |
| A harness needs an input when Stage launches a task | Typed launch option validated and applied by the adapter |
| A scoped harness feature has its own input or side effect | Declared action, dispatched generically and validated by its owner |
| A harness reports a detail that Stage displays | Declared fact |
| A typed capability needs adapter-specific behavior beyond the base adapter | Optional interface reached via `execution.As`, with the declared operation or policy applied |

### D8

**Separate process placement from session locality** (layer 05). Declare
whether the module requires local processes and whether its sessions live
on a machine or in the cloud. Descriptors also declare state reasons.
This lets Stage decide where code can run and explain session state
without inferring either from the harness name.

Layer 05 also switches the existing connection picker's eligibility check
to `localProcesses` when it removes `hosting` from the descriptor.

### D9

**Persist read-only evidence per workspace** (layer 07). Record file host,
canonical path, checkout key, subpath, normalized remote and inode hint.
Read filesystem evidence where the files live, without a git binary or
writes to the repository. A remote plus subpath identifies one project
across machines, across clones and across worktrees. A fork has its own
remote. With no remote, a file host, checkout and subpath make a
machine-scoped key. A plain directory can use its file host and canonical
path. A pathless workspace binds by connection and external ID, even
when it carries remote metadata. Explicit file ownership keeps unrelated
equal paths from being combined.

### D10

**Use one resolver for import, Resolve now and bind preview** (layer 07
backend, layer 08 UI). Outcomes are `bound`, `join`, `new` and `review`.
A project can bind several workspaces on one connection, with one
primary and a per-task or per-schedule choice. Rebind a move automatically
only when exactly one unavailable source and one eligible replacement
match. Send ambiguity to review and preserve the user's declines. Support manual merge and split. The merged ID resolves
as an alias of the survivor. A migration may suggest merges; the user
decides whether to combine existing projects. This keeps project identity
stable and gives users a way to correct the evidence.

### D11

**Run observation beside the adapter.** Layer 09 composes `coordinator`,
`observe` and `discovery` in `engine`. Per-connection loops still run on
the main server, and adapter events and bridge pushes meet at
`observe.Service.Ingest`. Layer 10 changes placement. The `observe`
package then runs in the server for the local machine and in
`stage worker` for a remote machine. The main server sends a watch set. The observer pushes cursor-bearing batches and
keeps a bounded outbox during an outage. Commands remain request and
response. The existing processes host observation without a separate
daemon or per-harness process. This lets remote observation continue
when the server is unreachable.

### D12

**Negotiate the worker channel explicitly** (layer 03 handshake, layer 10
settings cache). Exchange protocol range, harness kinds, channel kinds and
traits independently of the product API version. Workers compose from
locally cached settings. Negotiation distinguishes unsupported capabilities
from a lost connection, and cached setup lets the worker compose while
the main server is unavailable. Layer 07 bumps the worker protocol from
2 to 3 for the inventory evidence contract and removal of `ops.checkout`.
The API remains v9.

### D13

**Map Amp as a future integration** (layers 01 and 11). The
[Amp mapping](../harnesses.md#amp-mapping) records the fit of a local
CLI or runner with cloud sessions, account-plus-machine identity,
repository evidence, scoped actions and usage facts. Verify the
observation, cancellation, approval, directory inventory and attribution
gaps before an adapter. This tests the design against a different harness
through documentation alone. The series ships four harnesses.

### D14

**Shape UI changes with Impeccable** (layers 06 and 08, plus UI work in
other layers). Start from the existing visual world and shape the
interaction before implementation. This lets capability and discovery
changes refine familiar screens and preserve harness-specific settings
and vocabulary.

### D15

**Merge and verify each PR independently.** Fetch and integrate current
`origin/main` before opening or updating a PR, then fetch, rebase and
validate again before merging. Verify the actual merged result on
`origin/main` and hand off through the journal. The user starts root task
#425. Stage controls successor readiness. No layer releases the series.
This makes the reviewed merged result the source for the next layer.

## Superseded clauses

| Earlier clause | Replacement |
| --- | --- |
| [ADR 0007 decision 8](0007-harness-boundary.md#decisions), polling placement | [D11](#d11) places observation beside the adapter. The separate-daemon prohibition and external bridge ingest remain. |
| [ADR 0007 decision 11](0007-harness-boundary.md#decisions), project identity ladder | [D9](#d9) and [D10](#d10) replace the ladder with evidence keys and one resolver. Stage project ownership and read-only discovery still apply. |
| [ADR 0002 decisions 2 and 3](0002-machines-and-workers.md#2-thin-worker-no-database), shared-path translation and matching | [D3](#d3) removes shared paths. [D9](#d9) scopes file evidence to the actual file host. |
| [ADR 0002 decision 4](0002-machines-and-workers.md#4-transport-and-rpc-channel), exact API version check for workers | [D12](#d12) negotiates a separate worker protocol. |
| Stage task settlement and legacy PR and run artifacts | [D3](#d3) uses `done` plus acceptance for completion and removes the legacy artifact surface. |
| Automatic project archive follow | [D3](#d3) leaves archive state with Stage and records harness archive as workspace evidence. |

## P2 review checklist

For each change, trace the shared entry point and check the behavior or
declared fallback on all four harnesses. The evidence column names tests
or entry points to inspect. “Shared” means Stage uses the implementation
named in that row for each connection.

| Feature | T3 | OpenCode | Claude Code | Codex | Shared path and evidence |
| --- | --- | --- | --- | --- | --- |
| Launch and adopt | Reserved thread ID in the pairing namespace. Adopt by thread ID or link. [Adapter](../../server/internal/t3/adapter.go) | Reserved session and prompt IDs. Adopt by session ID or URL. [Launch](../../server/internal/opencode/launch.go), [link resolver](../../server/internal/opencode/client.go) | Correlated launch marker. Adopt by UUID, short ID or command. [Policy](../../server/internal/claudecode/descriptor.go), [adapter contract](../../server/internal/claudecode/adapter_test.go) | Checkpoint the daemon's thread ID before starting the turn. Adopt by thread ID or resume command. [Launch](../../server/internal/codex/launch.go), [adapter contract](../../server/internal/codex/adapter_test.go) | Gate on `launch` and `adopt`. Refuse wrapped sessions through the descriptor. [Launch coordinator](../../server/internal/coordinator/launch.go), [adoption tests](../../server/internal/api/adopt_test.go) |
| Stop | Interrupt the thread. [Adapter](../../server/internal/t3/adapter.go) | Interrupt the session. [Adapter](../../server/internal/opencode/session.go) | Stop a background session. For a terminal or another app, stop there. [Stop test](../../server/internal/claudecode/adapter_test.go) | Interrupt the active turn. [Stop test](../../server/internal/codex/adapter_test.go) | Check `stop` before dispatch. [Task handler](../../server/internal/api/handlers_tasks.go) |
| Images | Upload and checkpoint attachment references. [Adapter](../../server/internal/t3/adapter.go) | `images` unavailable with a delivery-verification reason. [Declaration](../../server/internal/opencode/workspace.go) | `images` available with an image root. [Declaration](../../server/internal/claudecode/adapter.go), [launch tests](../../server/internal/engine/contract_claude_test.go) | Send data URLs with turn input. [Launch](../../server/internal/codex/launch.go) | Check `images` for the selected destination. [API tests](../../server/internal/api/images_test.go), [task form](../../packages/app/src/components/TaskForm.tsx) |
| Interaction | Answer questions and send follow-ups (mode `auto`: new turn when idle, steer when running) through T3's MCP. Permission requests are approved in T3 (`unlistedRequests`). [Declaration](../../server/internal/t3/adapter.go), [interaction](../../server/internal/t3/interaction.go) | Answer permissions (Allow once, Allow always, Deny) and forms Stage can show, and send follow-ups (delivery `steer`), through the session endpoints. `respond` needs the reply operations in OpenAPI. Forms with conditions, hidden or external fields are answered in OpenCode (`unlistedRequests`); a session waiting on any request takes no message. [Declaration](../../server/internal/opencode/workspace.go), [interaction](../../server/internal/opencode/interaction.go) | Respond through hooks when available. Send follow-ups to supported sessions. [Declaration](../../server/internal/claudecode/adapter.go), [requests](../../server/internal/claudecode/requests.go), [send](../../server/internal/claudecode/send.go) | Answer or follow up using the Codex resume command. [Declaration](../../server/internal/codex/adapter.go) | Check `respond` and `send` plus `Interactive`. The adopted prompt UI reads `opening`; Stage retains available final replies. [Interaction tests](../../server/internal/api/interaction_test.go), [prompt panel](../../packages/app/src/components/PromptPanel.tsx), [snapshot contract](../execution.md#the-snapshot) |
| Session rename | Rename with `t3_thread_update` (`rename`). [Adapter](../../server/internal/t3/adapter.go) | `PATCH /api/session/{id}` when OpenAPI advertises `session.update` with `title`, else unavailable. [Rename](../../server/internal/opencode/rename.go) | Unavailable: no rename interface. [Declaration](../../server/internal/claudecode/adapter.go) | `thread/name/set`. [Rename](../../server/internal/codex/rename.go) | After a title change, rename Stage-launched sessions only when `sessionRename` is available, in the background; other failures note the task's journal. [Coordinator](../../server/internal/coordinator/rename.go), [trigger tests](../../server/internal/api/session_rename_test.go), [worker parity](../../server/internal/remote/rename_test.go) |
| Attention | List eligible unsettled threads. [Adapter](../../server/internal/t3/adapter.go) | List active sessions and pending forms or permissions in loaded locations. [Adapter](../../server/internal/opencode/session.go) | List waiting sessions. The silent policy keeps discovered sessions out of inbox and push. [Contract test](../../server/internal/engine/contract_claude_test.go) | List loaded threads waiting for approval or input. [Adapter](../../server/internal/codex/session.go) | Poll beside the adapter and apply notification policy at ingest. [Runner tests](../../server/internal/observe/runner_test.go), [ingest](../../server/internal/observe/ingest.go) |
| Links | Web and declared `t3code` scheme. [Descriptor](../../server/internal/t3/descriptor.go), [adapter tests](../../server/internal/t3/adapter_test.go) | Web destination with an optional separate client origin. [Adapter tests](../../server/internal/opencode/adapter_test.go) | Copy an attach or resume command. [Adapter tests](../../server/internal/claudecode/adapter_test.go) | Copy a resume command for the owning machine. [Contract tests](../../server/internal/engine/contract_codex_test.go) | Read stored observation links and offer the host destination or resume command. [Local and worker contract](../../server/internal/engine/contract_links_test.go), [session control](../../packages/app/src/components/SessionAction.tsx) |
| Acceptance | Optional inference from settled closure, also after Settle in T3 from Stage. [Descriptor](../../server/internal/t3/descriptor.go), [acceptance tests](../../server/internal/api/acceptance_test.go), [settle action test](../../server/internal/api/session_archive_test.go) | Optional inference from archive. [Descriptor](../../server/internal/opencode/descriptor.go), [observation](../../server/internal/opencode/session.go) | Optional inference from removal. [Descriptor](../../server/internal/claudecode/descriptor.go), [contract test](../../server/internal/engine/contract_claude_test.go) | Optional inference from archive. [Descriptor](../../server/internal/codex/descriptor.go), [contract test](../../server/internal/engine/contract_codex_test.go) | User acceptance and closure inference both record immutable Stage evidence. [Storage](../../server/internal/db/acceptance.go), [dependency release test](../../server/internal/api/acceptance_test.go) |
| Handoff | Shared | Shared | Shared | Shared | Compose from pinned acceptance, including when the source is offline or its reply is missing. [Coordinator](../../server/internal/coordinator/handoff.go), [handoff tests](../../server/internal/api/handoff_test.go) |
| Trees | Shared | Shared | Shared | Shared | Stage owns dependencies, readiness and starts. Each task launches on its own connection. [Tree handlers](../../server/internal/api/handlers_trees.go), [lifecycle and fan-in tests](../../server/internal/api/trees_test.go) |
| Schedules | Shared | Shared | Shared | Shared | Stage spawns a task with the schedule's scoped destination and launch config, then uses the same launch coordinator. [Schedule handlers](../../server/internal/api/handlers_schedules.go), [scoped launch contract](../../server/internal/engine/contract_test.go) |
| Journals | Caller hint uses `T3CODE_THREAD_ID`. [Descriptor](../../server/internal/t3/descriptor.go) | Caller hint uses MCP metadata or `OPENCODE_SESSION_ID`. [Descriptor](../../server/internal/opencode/descriptor.go) | Caller hint uses `CLAUDE_CODE_SESSION_ID` and declared origin and precedence. [Descriptor](../../server/internal/claudecode/descriptor.go) | Caller hint uses `CODEX_THREAD_ID` and observed origin. [Descriptor](../../server/internal/codex/descriptor.go) | Stage owns task and tree notes, events and launch instructions. [Journal API tests](../../server/internal/api/journal_test.go), [launch instructions tests](../../server/internal/coordinator/journal_test.go) |
| Discovery | Live T3 projects with reported paths and unknown file host. [Adapter](../../server/internal/t3/adapter.go) | Service projects. File evidence only for local authentication. Verification uses only the project and worktree lists, so it never persists or unarchives a project. [Adapter](../../server/internal/opencode/workspace.go) | Local folders in the Claude state file and session roster. Auto-import checkout roots. Untrusted folders stay listed but blocked, using Claude's trust rule on machine evidence. Their project action `trust` fixes that. [Adapter tests](../../server/internal/claudecode/workspace_test.go) | Local trusted folders. Auto-import folders inside checkouts. [Adapter tests](../../server/internal/codex/adapter_test.go) | Refresh through `Workspaces` and machine evidence. Retain the last good inventory on failure. A row the harness will not launch in stays listed with its reason and is not eligible. [Inventory contract](../discovery.md#refresh-and-adapter-contract-07-behavior-09-ownership), [failure test](../../server/internal/engine/inventory_test.go) |
| Project identity | Reported origin and subpath, or scoped evidence under [D9](#d9). | Local Git evidence when local-auth, otherwise connection-scoped workspace evidence. [Inventory](../discovery.md#shipped-inventory) | Local checkout evidence. [D9](#d9) | Local checkout and subpath evidence. [D9](#d9) | Use one Stage resolver for import, Resolve now and bind preview. [Resolver](../../server/internal/db/import.go), [four-harness identity test](../../server/internal/engine/identity_test.go), [review API tests](../../server/internal/api/project_identity_test.go) |
| Workspace add | Sources `folder`, `clone` and `new` through `t3_project_create` and `t3_project_clone`. A pre-list returns an existing target, and a re-list reconciles a lost reply. [Sources](../../server/internal/t3/workspace_add.go), [tests](../../server/internal/t3/workspace_add_test.go), [local and worker tests](../../server/internal/api/workspace_add_t3_test.go) | Source `folder` (existing folder, `path`) when `/openapi.json` has `location.get`: `GET /api/location` persists the project (undocumented, pinned by a fake), then the project list confirms it. Missing folder 400, `global` project 400. [Adapter](../../server/internal/opencode/workspace_add.go), [tests](../../server/internal/opencode/workspace_add_test.go) | Source `trust` "Trust in Claude Code" (an existing folder; confirmation required): the one documented file write, `projects[folder].hasTrustDialogAccepted`, under Claude's state lock with every other byte kept ([ADR 0012](0012-harness-workspace-add.md) decision 8). Existing when Claude trusts the folder and lists it. [Trust](../../server/internal/claudecode/trust.go), [tests](../../server/internal/claudecode/trust_test.go) | Source `trust` ("Trust folder"): `fs/getMetadata` directory check, then `config/batchWrite` upserts the folder's `trust_level` (never a file edit). Already trusted is `existing`. [Adapter](../../server/internal/codex/workspace_add.go), [tests](../../server/internal/codex/workspace_add_test.go), [both placements](../../server/internal/engine/contract_codex_test.go) | [ADR 0012](0012-harness-workspace-add.md). Check the op, source, input and confirmation, relay to `WorkspaceAdder`, refresh inventory and resolve the listed row. A busy harness that changed nothing answers 409 `harness_busy`. [Handler](../../server/internal/api/handlers_workspace_add.go), [local and worker tests](../../server/internal/api/workspace_add_test.go), [Claude Code local and worker test](../../server/internal/api/claude_trust_test.go) |

## P3 scoped inventory

Setup actions and facts are connection-scoped and setup actions are user-only.
Session actions (users and agents, never automatic) archive a tracked session
in its harness; the session's observation reports when one is unavailable
([execution](../execution.md#actions-and-facts)). Project actions target one
listed workspace, also one its harness will not launch in, and refresh the
connection's inventory after they succeed.
Launch choices belong to the task or schedule's selected connection and, for
OpenCode's catalog, its selected workspace. The [UI slot registry](../../packages/app/src/harnesses/index.ts)
preserves each panel's vocabulary, while the [shared presentation](../../packages/app/src/components/HarnessPresentation.tsx)
renders status fixes and facts from the connector. Display probes for facts
are bounded and cached for a minute, and they never fail a status
([execution](../execution.md#connection-setup-and-ports)). A missing Stage MCP
server becomes the status fix when the status has no other fix.

| Harness | Connection actions and setup settings | Task and schedule launch choices | Connection facts and UI states | Session actions |
| --- | --- | --- | --- | --- |
| T3 | `pair` with a secret code, `auto-pair` when available and disconnect. T3 URL, browser URL and optional admin token for renewal. [Descriptor](../../server/internal/t3/descriptor.go), [setup panel](../../packages/app/src/harnesses/t3/Setup.tsx) | New worktree with required base ref and optional branch. Provider-instance models, model options and permissions, default or plan mode. [Options](../../server/internal/t3/options.go), [catalog](../../server/internal/t3/adapter.go) | Instance label and version, T3 and browser URLs, environment and client IDs, token expiry and admin-token state. Connected, unpaired, expired, unreachable or rejected, with an initial sync step in setup. [Facts](../../server/internal/t3setup/connector.go), [status](../../packages/app/src/harnesses/t3/status.ts) | `settle` "Settle in T3" and `archive` "Archive in T3" through `t3_thread_organize`; unavailable as T3 refuses them. [Actions](../../server/internal/t3/actions.go) |
| OpenCode | `connect-local`, `pair` with a secret link, `check` and disconnect. Service, browser and web client URLs, username and password, and explicit new-instance confirmation. [Descriptor](../../server/internal/opencode/descriptor.go), [setup panel](../../packages/app/src/harnesses/opencode/Setup.tsx) | Worktree using the service's default base. Models and variants and visible primary agents come from the selected workspace. The agent supplies the permission policy. [Workspace catalog](../../server/internal/opencode/workspace.go) | Service, browser and web client URLs, local service version, sign-in method, token expiry, last check and Stage MCP status (`GET /api/mcp`). Connected, not connected, unavailable, rejected or expired. [Facts](../../server/internal/opencodesetup/connector.go), [status chip](../../packages/app/src/harnesses/opencode/opencode.ts) | `archive` "Archive in OpenCode" through `PATCH /api/session/{id}` when the spec has it. [Adapter](../../server/internal/opencode/session.go) |
| Claude Code | `connect`, `check` and disconnect. Configurable Claude command, with login and config directory owned by Claude Code. [Descriptor](../../server/internal/claudecode/descriptor.go), [setup panel](../../packages/app/src/harnesses/claude/Setup.tsx) | Model alias, effort, permission mode and Claude-managed worktree. [Catalog](../../server/internal/claudecode/adapter.go), [launch](../../server/internal/claudecode/launch.go) | Command, version, config directory, update mode and Stage MCP status (`claude mcp get stage`). The panel also describes approval availability and last hook or polling reason. Connected, not connected, not found, logged out or not answering. [Facts](../../server/internal/claudecodesetup/setup.go), [status](../../packages/app/src/harnesses/claude/claude.ts) | None. Project action `trust` "Trust in Claude Code" on a folder Claude does not trust, users and agents, confirmation required; the workspace source's code path. [Trust](../../server/internal/claudecode/trust.go) |
| Codex | `connect`, `check` and disconnect. Configurable Codex CLI, with daemon identity and login owned by Codex. [Descriptor](../../server/internal/codex/descriptor.go), [setup panel](../../packages/app/src/harnesses/codex/Setup.tsx) | Daemon model catalog, per-model reasoning effort and approval/sandbox presets. Launch uses the bound checkout. [Catalog](../../server/internal/codex/adapter.go), [launch validation test](../../server/internal/codex/adapter_test.go) | CLI and app-server versions, Codex home, daemon, account, Stage MCP status (`mcpServerStatus/list`), ChatGPT usage windows with reset time (`account/rateLimits/read`) and last check. Connected, signed out, unreachable, not installed or not connected. [Facts](../../server/internal/codexsetup/connector.go), [status](../../packages/app/src/harnesses/codex/status.ts) | `archive` "Archive in Codex" through `thread/archive`. [Adapter](../../server/internal/codex/session.go) |
| Generic fixture | `hosted-kit` declares a user-only `connect` and disconnect. The generic panel reads setup fields, live availability and secret inputs from declarations. [Fixture](../../server/internal/api/hosted_test.go), [generic panel](../../packages/app/src/harnesses/generic.tsx), [setup form tests](../../packages/app/tests/connection-setup.test.mjs) | Fixture model `m`. [Catalog](../../server/internal/api/hosted_test.go) | Config-folder fact and connected/disconnected states. A second fixture exercises the workspace-scoped session action `label` and a copyable label fact, user-confirmed `clear`, `busy`, unavailable `later`, and project action `open-workspace`. [Local and worker action tests](../../server/internal/api/actions_test.go), [generic status tests](../../packages/app/tests/connections.test.mjs) | The second fixture's `close` reports its availability on the observation. [Local and worker action tests](../../server/internal/api/actions_test.go) |

Claude Code and Codex also declare the session reason `owner_exited`.
Stage validates it at [observation ingest](../../server/internal/coordinator/apply.go).
The [generic action renderer](../../packages/app/src/components/DeclaredActions.tsx),
the task toolbar's [session action buttons](../../packages/app/src/components/SessionToolbarActions.tsx)
and [action dispatch](../../server/internal/api/handlers_actions.go) consume
session and project declarations through the same boundary.

## Tests that can falsify the boundary

| Layers | Boundary | Evidence of a violation |
| --- | --- | --- |
| 02, 04 | A Stage task and its acceptance are Stage records; harness internals remain harness-owned. | An observation releases a dependent without a Stage acceptance record, history loses recorded evidence at migration, or core issues a Git command for a session. |
| 03, 04, 09 | Modules compose the same way in both placements. | Equivalent local and worker fixtures produce different supported behavior, or an unsupported channel trait appears as an offline machine. |
| 04, 05, 06 | A kind-neutral feature uses declarations and preserves each harness's extras. | Boundary tests find a kind branch in a core surface, a control invokes unavailable Stop, or an agent can invoke a user-only action. |
| 07, 08 | Project identity has one resolver over read-only evidence. | Import and preview disagree with the same inputs, equal paths on unrelated hosts join, an ambiguous move rebinds, or a migrated project ID is lost. |
| 09, 10 | Observation is independent of command RPC and stays beside the adapter. | A disconnected worker stops observing its watch set, or replaying a batch creates a second transition. |

## Delivery

The baseline describes shipped behavior. Layers 02–10 implement the
target contracts below and layer 11 verifies the integrated result.
Unspecified implementation details are resolved in their owning layers:
layer 03 defines handshake encoding, layer 05 defines the action wire
surface, layer 07 defines persistence and discovery timing, layer 08
shapes review, and layer 10 defines cursor and outbox mechanics.

| Layer | Independent PR to main |
| --- | --- |
| 01 | Accept this ADR, add [discovery](../discovery.md) and [harness authoring](../harnesses.md), and correct stale documentation. |
| 02 | API v9 vocabulary and removals. Read link schemes and provenance labels from descriptors. |
| 03 | Worker handshake and local/remote parity. One trait table. Destinations leaves the adapter. |
| 04 | Harness modules, generic settings and secrets, declared identity policy and stronger boundary tests. |
| 05 | Actions, facts, status phase and fix, hosting split, API, MCP, CLI and worker proxy. |
| 06 | Connection and task harness UI, shared primitives, generic panel, action and fact rendering, and operation gates. |
| 07 | Project evidence, one resolver, multiple workspaces, moves, merge and split, and removals. |
| 08 | Projects and discovery UI, workspaces table, review, merge and workspace choice. |
| 09 | Split coordinator, observe and discovery roles and rewrite the execution contract. Preserve behavior in this layer. |
| 10 | Observer beside the adapter, watch set, cursor, outbox and cached worker settings. |
| 11 | Finish the authoring guide, recheck Amp evidence, review all eleven PRs and validate the integrated series. [Closeout record](../harness-closeout.md) |
