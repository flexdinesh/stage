# Adding a harness

Stage owns logical project groups, tasks, trees, schedules, journals,
acceptance and handoff. Each harness connection owns its workspace catalog
and sessions. The harness owns worktrees, branches, archive,
models, permissions and processes. Stage records a session as
`{connectionId, externalSessionId}`. See [ADR 0007](adr/0007-harness-boundary.md),
[ADR 0008](adr/0008-harness-foundation.md) and the [execution contract](execution.md).

## Minimum authoring footprint

A new kind using the generic surfaces touches **eight files in five groups**
in the layout below. Add seven files and edit the shared module list once.
Split larger implementations or suites into more files as needed.

| Group | Count | Files and responsibilities |
| --- | ---: | --- |
| Adapter package | 2 new | `server/internal/<kind>/adapter.go` implements execution. `server/internal/<kind>/descriptor.go` declares the kind and its policies. |
| Setup package | 2 new | `server/internal/<kind>setup/module.go` declares the module, typed settings and validator. `server/internal/<kind>setup/connector.go` implements setup and installs the adapter. Larger modules can put their settings in a separate `settings.go`. |
| Registration | 1 edit | Import the setup package and add its module once in [`server/internal/harnesses/modules.go`](../server/internal/harnesses/modules.go). The entry feeds both placements and the descriptor registry. |
| Tests | 2 new | `server/internal/<kind>/adapter_test.go` for native and execution contracts and `server/internal/<kind>setup/connector_test.go` for setup and identity parity. Use the [existing suites](#verification) as templates. |
| Guide | 1 new | `docs/<kind>.md` for setup, troubleshooting, supported operations and placement limits. |

Use the module's `Owners` field to name the adapter and setup package paths
relative to the server root. Core consumers resolve the descriptor and roles
by connection. The generic paths already supply storage, API, worker
dispatch, setup UI and CLI commands.

## Descriptor and identity

The [descriptor](../server/internal/harness/harness.go) declares the kind,
display name, setup fields and actions, link schemes, web-address support,
session and discovery hints, allowed state reasons, attention policy and any
closure signals. It declares `LocalProcesses` separately from `Sessions`
(`machine` or `cloud`). The first controls machine eligibility and the second
describes where sessions live.

- **Launch identity.** Use `LaunchReserved` when launch reserves a session ID
  before effects and can reconcile a replay. Persist each checkpoint before
  the effect it guards. Use `LaunchCorrelated` for a harness-assigned ID when
  Stage must dispatch once and then find the session by its launch marker
  through `Correlator`. An unresolved match stays unconfirmed.
- **Connection identity.** One connection per kind per machine is active
  after setup. Replacement retires the old connection and preserves its
  history. Declare `StableIdentity` when an instance key reliably names the
  same instance and can revive its connection. If an address is ambiguous,
  declare `ContinuityProof` and use the `Identity` port's stored workspace
  and session evidence to choose a retired connection. A stable module can
  declare `MovedRetire` for a confirmed move between reachable machines.
- **Caller identity.** Give the agent a way to supply its Stage connection
  and external session IDs. The generic channels are `callerSession`,
  `Stage-Caller-Session` for HTTP MCP and `STAGE_CALLER_SESSION` for the CLI.
  The header and environment variable take `<connectionId>:<externalSessionId>`.
  Declare native MCP `_meta` keys or a native session environment variable
  where the harness supplies them. A wrapper declares `Wraps.Origins`.
  The wrapped adapter reports the native origin. Stage then gives attribution
  to the wrapper and refuses duplicate tracking on the wrapped connection.
  See [caller attribution](execution.md#ownership-stage-and-the-harness).

## Setup ports and placement

Implement [`harness.Module`](../server/internal/harness/module.go) with the
descriptor, public settings validator and `Compose(ctx, ports)`. Return a
[`Connector`](../server/internal/harness/connector.go) and optional cleanup
for Stage's client resources. The connector provides an in-memory summary,
status with a bounded refresh, configuration, actions and disconnect. Map
status to a shared phase with action availability, display facts and an
optional fix. Custom slots can read the connector's `Details`.

Keep typed settings, validation, client construction and credential formats
in the setup package. `Settings` reads public JSON per machine and kind
and applies a top-level patch. Omitted fields keep their values and a null
resets a field to its default. `Secrets` stores opaque values by kind and key.
`Identity` supplies the machine's active and retired identities and continuity
evidence. Use `Install(old, next, adapter)` to move or replace the active
adapter. The ports also supply the machine's profile, data directory,
environment and executable lookup, logging, test overrides and a change
callback. The local placement can atomically save settings with a secret
when `SaveSettingsWithSecret` is supplied.

| Placement | Composition and call sites |
| --- | --- |
| Local | [`serve/localmachine.go`](../server/internal/serve/localmachine.go) composes the modules that the profile allows over machine-scoped store ports and installs into `machine.Local`. Connector and adapter calls are in process. [`observe.Service.Run`](../server/internal/observe/placement.go) starts the local observer. |
| Worker | [`workersetup`](../server/internal/workersetup/workersetup.go) composes the same allowed modules from negotiated kinds. Public settings and identity go through [`remote.ServeSetup`](../server/internal/remote/setupserver.go), with cached setup inputs for offline restore. Secrets stay in the worker credential file. Main-server [connector](../server/internal/remote/connector.go) and [adapter](../server/internal/remote/adapter.go) proxies dispatch over the authenticated channel to [`remote.Host`](../server/internal/remote/host.go). The [worker observer](../server/internal/worker/observation.go) runs beside the adapter. |

The [worker channel](../server/internal/workerchan/protocol.go) is protocol **3**,
independent of API **v9**. Negotiation covers the protocol range, harness
kinds, channel kinds and per-connection traits. Commands use request/response.
The main server sends `observe.watch`. Workers poll or subscribe and send
cursor-bearing `observe.batch` records, replayed from a bounded outbox after
an outage. The main server ingests them and retains the task state and
acceptance. External bridges use
`POST /api/connections/{id}/observations` to reach the same observation
service. See [worker setup and offline behavior](execution.md#remote-machines).

## Adapter roles and traits

[`execution.Adapter`](../server/internal/execution/types.go) embeds four roles.
[`machine.Connections`](../server/internal/machine/connections.go) resolves
the role on the connection's machine.

| Role | Methods and consumers |
| --- | --- |
| `Catalog` | `Health` reports reachability and instance identity. `Features` declares launch choices and operation availability for setup and task controls. |
| `Inventory` | `Workspaces` reports harness workspace IDs, paths, availability and provenance. [Discovery](../server/internal/discovery/inventory.go) enriches local paths with read-only evidence from the file host's `machine.Ops.Inventory`. A failed read preserves the last inventory. A row the harness will not launch in sets `BlockedReason`; folder trust that depends on Git layout declares a `TrustRule` that `machine.Ops` settles. See [workspace evidence](discovery.md) and [eligibility](discovery.md#eligibility). |
| `Sessions` | `Observe` supplies normalized snapshots, including replies when available. `Opening` reads the first prompt and `Attention` lists sessions surfaced to Stage. The observer uses these snapshots and the coordinator applies them to tracked tasks. |
| `Launcher` | `Launch` applies the frozen intent and saves durable checkpoints. `Stop` interrupts a scoped session when available. The coordinator owns dispatch and reconciliation. |

Use [`execution.As[T]`](../server/internal/execution/optional.go) for
`LinkResolver`, `SessionSearcher`, `SessionNamer`, `WorkspaceFeatures`, `WorkspaceAdder`,
`Correlator`, `EventSource`, `Interactive`, `ActivityReader` and `Actor`. The adapter implements the Go interface.
A partial adapter such as a worker proxy must also advertise the trait in
its ready snapshot. The trait says whether the interface is callable.
Check the corresponding operation, policy or action availability for the
requested scope. Polling reconciles missed `EventSource` events.

### Session interaction and activity

Follow [ADR 0014](adr/0014-session-surface.md) and the
[interaction contract](execution.md#interaction). Declare a harness-specific
reason for each unavailable operation.

| Operation | Adapter contract | Worker kind |
| --- | --- | --- |
| `steer` | `Interactive.Send` with `DeliverySteer` injects into the running turn. Return `Busy` with guidance if the turn ended. | `adapter.deliver` |
| `queue` | `Interactive.Send` with `DeliveryQueue` admits a message into the harness's native queue during a running turn. The harness delivers it as the next turn. | `adapter.deliver` |
| `activity` | `ActivityReader.Activity` reads a scoped session ref, including past attempts, and returns recent items oldest first with the plan and changed files. Use the requested limit and set `Truncated` when older items exist. | `adapter.activity` |

Return `Invalid` if a caller supplies an unsupported delivery. Keep the
session data in the harness and expose harness-specific activity details
through its own UI slot.

### Workspace and session selection

[ADR 0010](adr/0010-harness-workspaces-and-logical-projects.md) defines
the workspace and grouping contract. Report
meaningful catalog rows even when they lack Git evidence or would be
ineligible for automatic import. Supply the stable native ID and the
discriminator for each exact launchable workspace, including distinct
roots under one ID. Attest locality only when the files live on the
connection's machine. Report trust, archive, disappearance and
unavailability through the adapter contract. Stage handles grouping.

A complete empty report is authoritative. Return an error for an absent,
malformed or partial source so cached rows remain visible with their last
successful refresh time. Stage emits a stable selector scoped to the
owning connection. Worker adapters receive `adapter.workspaces` and
their file hosts receive `ops.inventory` through the negotiated channel.
Keep trust and exact location checks in the adapter when it launches.
[API contract](api.md#workspace-discovery-and-project-identity)

Implement session rename with `execution.SessionNamer` and declare
`OpSessionRename` when the harness can rename a session with its own
interface; otherwise declare it unavailable with a reason. Stage calls it
only after a task title change for a session it launched. See
[session rename](execution.md#session-rename).

Implement roster search with `execution.SessionSearcher` and declare
`OpSessionSearch` on the owning connection. Return bounded pages of
harness-listed metadata, including eligible completed sessions still
listed by the harness. Supply known origin for the Stage wrapper policy.
The Claude adapter checks only a bounded transcript prefix for origin
on matches from the roster. Declare an unavailable reason when the
harness or worker cannot safely enumerate. Keep collection browsing
behind the human route, independent of Attention, the watch set and
named lookup and adoption. Keep provider pagination and query rules in
the adapter. Preserve a continuation when filtering leaves a short or
empty native page. [Session search API](api.md#session-search) ·
[Current harness scope](execution.md#session-search)

### Adding workspaces

[ADR 0012](adr/0012-harness-workspace-add.md) lets a person or agent ask a
connection to add a workspace. Declare `OpWorkspaceAdd` and
`Capabilities.WorkspaceSources`. Each source is one way your harness adds a
project, in its own words: a stable ID, label, optional help, typed input
fields (`path` for a folder on the harness's machine), optional
confirmation text and availability. Implement `execution.WorkspaceAdder`
with the harness's own API inside the adapter or setup package:

- Validate what the harness needs beyond the declared fields (for example an
  absolute path) and return `*execution.Invalid` with a reason.
- Be idempotent per target. Return `Existing: true` when the workspace is
  already there.
- Return the native ID and root the next `Workspaces` call will list. Stage
  refreshes inventory and refuses a row the harness does not list.
- Return `*execution.OutcomeUncertain` when the harness may have acted but
  did not confirm. Use `requestId` to reconcile a retry. When the harness
  takes no request ID, list its workspaces before the call and again after a
  lost reply, and return the match (T3 does this).
- Put irreversible or trust-granting effects behind `Confirm`. Stage requires
  `confirmed: true` per request. A project action that performs the same
  effect also declares `Confirm` and refuses an `ActionRequest` without
  `Confirmed` (Claude Code's `trust`).
- Return `*execution.Busy` when the harness refused for now and nothing
  changed (409 `harness_busy`).

Never create directories, run git or edit harness files from Stage core.
Claude Code's documented trust key is the only file write ADR 0012
allows, and only in its adapter ([trust.go](../server/internal/claudecode/trust.go)).
Until a harness supports it, declare
`execution.WorkspaceAddNotSupported(name)`. Workers relay the call as
`adapter.workspaceAdd` behind the `workspaceAdder` trait.

The Add workspace UI (Projects, a project's Workspaces panel, and the task
and schedule form) renders only from these declarations: your source labels
and help, your fields (`path` in monospace), and your confirmation as a
checkbox the person must tick. One source shows no Source choice; two to
four show joined buttons (a dropdown on phones); more show a dropdown.
Write labels short enough to read in joined buttons.
[Workspace add API](api.md#add-a-workspace) ·
[Execution contract](execution.md#workspace-add)

### Browsing folders for path fields

A `path` field takes a typed absolute path. If your harness can list
folders on its host, declare `OpDirectoryBrowse` and implement
`execution.DirectoryBrowser` with its own read-only API. Then every `path`
input on that connection (workspace sources and connection, project and
session action forms) offers Browse…: an inline browser with the current
folder and Up, folder rows, Show hidden and Use this folder. Harness setup
fields, which have no connection yet, stay typed.

- Return folders only, through `execution.Listing`. It hides dot-folders,
  sorts the rest and stops at 500.
- An empty `Path` is your default start: somewhere a person recognizes,
  such as the host user's home (Codex) or the parent of a listed project
  (OpenCode).
- Return `*execution.Invalid` with the harness's words for a missing,
  unreadable or non-folder path. Return `*execution.BrowseUnavailable` with
  a reason when you cannot browse right now.
- Never persist anything while listing. OpenCode names an existing project
  as its location because a request without one saves the service's
  working directory as a project.

Without a listing API, declare the operation unavailable with a reason that
tells the person to type the path (T3, Claude Code). Only people browse;
agents type paths. Workers relay the call as `adapter.directoryBrowse`
behind the `directoryBrowser` trait.
[Browse folders API](api.md#browse-folders) ·
[Execution contract](execution.md#folder-browsing)

## Choose an extension

Use [ADR 0008 P4](adr/0008-harness-foundation.md#p4) to decide which contract
fits. Stage-wide behavior uses descriptor policy or typed operations and
degrades through declared availability.

| Extension | Use it for | Contract |
| --- | --- | --- |
| Display fact | Show a harness detail such as an endpoint or extra usage information. | `execution.Fact` carries a label, value and tone in status, an observation or an action result. |
| Scoped action | Relay a harness-specific operation such as labelling a session. | Declare the target, inputs, confirmation, caller authorization and availability. Connection setup uses the connector. Session and project actions use `execution.Actor` and capability declarations. The owner validates opaque input. |
| Typed operation | Let Stage decide whether it can launch, stop, adopt, read a reply or opening, list attention, search for sessions, launch with images, respond, deliver messages, read activity, add a workspace or browse folders. | Declare each [`execution.Op`](../server/internal/execution/ops.go) with availability and a reason when unavailable. Use `WorkspaceFeatures` for a workspace-specific declaration. |
| Launch option | Pass a choice, boolean or text value at launch, such as a harness worktree option. | Declare the field in capabilities and validate and translate it from `execution.Config`. |
| Descriptor policy | Apply a Stage invariant such as launch recovery, attribution, identity or completion from a closure signal. | Declare it in the descriptor. Stage interprets the policy for every kind that uses it. A harness whose `respond` lists only some request kinds declares `unlistedRequests` with where to answer the rest (T3: permission requests; OpenCode: forms with conditions, hidden or external fields; Codex: permission requests, MCP elicitations and secret questions). |

An agent can invoke an action only when the declaration allows agents,
and never a session action on its own task's session.
Project actions use an exact workspace scoped to its owning connection.
A session action unavailable for one session (already archived, still
running) is reported on that session's observation as `actions` states in
the harness's own words, never by a Stage rule
([execution](execution.md#actions-and-facts)).
See the [action and facts API](api.md#declared-actions-and-facts).

### Connect local OpenCode

1. The [descriptor](../server/internal/opencode/descriptor.go) declares
   `connect-local` and its `newInstance` boolean. The connector's status
   reports current availability and a reason.
2. The [connector](../server/internal/opencodesetup/connector.go) decodes
   the input and its setup service reads the OpenCode service registration
   on the selected machine. The [module](../server/internal/opencodesetup/module.go)
   composes that connector in both placements. A remote setup action goes
   through the worker proxy.
3. The [action handler](../server/internal/api/handlers_actions.go) validates
   the declaration, caller, availability and input. The setup route is
   `POST /api/harnesses/{kind}/actions/{action}`, also available under
   `/api/machines/{id}/harnesses/` for a selected machine.
4. The [OpenCode panel](../packages/app/src/harnesses/opencode/Setup.tsx) calls
   the route through its scoped client. `stage harness opencode run connect-local`
   uses the same setup contract. With a connection ID, `stage action ls --connection ID`
   lists actions and `stage action run --connection ID connect-local` dispatches it.

## Optional UI and CLI slots

- **UI, two additional files at minimum.** Add
  `packages/app/src/harnesses/<kind>/index.tsx` and edit the
  [UI registry](../packages/app/src/harnesses/index.ts) to register it and
  call its status hook in the fixed order. Supply the
  [`HarnessSlots`](../packages/app/src/harnesses/types.ts) setup panel,
  connection row, status hook and query key, and live chip. Declare which
  setup actions the panel handles. Manage and the connection row expose
  the other declared actions. A kind without a custom slot uses the
  [generic renderer](../packages/app/src/harnesses/generic.tsx) for settings,
  action fields and confirmation, availability, facts and fixes.
- **CLI, one additional file.** Add `server/internal/cli/slots_<kind>.go`
  and register a status printer in `init`, following the
  [OpenCode slot](../server/internal/cli/slots_opencode.go). The generic
  [`stage harness` commands](../server/internal/cli/harness.go) already
  use the setup schema. They use generic output when custom details are
  absent or the printer returns an error.

### Open items in generic surfaces

- The custom UI registry also lists each status hook explicitly for React's
  fixed hook order. Adding a custom slot touches both places in that file.
  The UI boundary test currently derives kind literals from slot directory
  names and display names from Go descriptors. A generic-only kind's
  literal needs coverage without adding a custom slot directory.
- `stage setup` for installing agent-side MCP, skills and instructions
  still has per-agent CLI wiring and flags in
  [`setup.go`](../server/internal/cli/setup.go) and
  [`setup_agents.go`](../server/internal/cli/setup_agents.go). Those files
  are outside module registration and exempt from the Go harness-name
  check. Automatic agent-side installation for a new kind still requires
  edits there.
- Generic CLI status text prints settings, unavailable actions and an error
  reason. Rendering the declared phase, facts and fix in that text view
  remains open. They already travel in the status response.
- Desktop native links still use a fixed scheme and fallback. A second native
  scheme needs a desktop navigation policy extension in
  [`desktop/app/nav/nav.go`](../desktop/app/nav/nav.go). The current fallback
  also constructs the T3 web link from the local status in
  [`desktop/app/launch/launch.go`](../desktop/app/launch/launch.go) and
  [`desktop/app/shell.go`](../desktop/app/shell.go). Browser links and resume
  commands use the supplied destinations directly.

## Verification

- **Adapter.** Test every declared operation, reasons for unavailable
  operations, connection-scoped refs, normalized states and replies, wrapper
  origins if declared, and workspace evidence. Test create, lost reply and
  restart, including a lost checkpoint response or a correlation miss.
  See the [execution contracts](../server/internal/engine/contract_v2_test.go)
  and [remote launch tests](../server/internal/engine/contract_remote_test.go).
- **Setup.** Exercise first connect, replacement, retirement and reconnect
  through local serve and the [authenticated worker setup rig](../server/internal/workersetup/workersetup_test.go).
  Verify settings validation, credential placement and machine ownership.
  See the [OpenCode continuity tests](../server/internal/opencodesetup/instance_test.go).
- **Discovery and observation.** Test inventory failures, file-host
  provenance, polling and optional events, and worker replay after a
  disconnect. See the [inventory tests](../server/internal/engine/inventory_test.go),
  [identity tests](../server/internal/engine/identity_test.go) and
  [worker observation contract](../server/internal/engine/observer_channel_test.go).
- **Consumers.** Exercise both offered and absent traits, blocked operations,
  action scopes and caller authorization in the relevant API, CLI, MCP and
  UI paths. See [trait tests](../server/internal/execution/as_test.go),
  [action tests](../server/internal/api/actions_test.go) and
  [attribution tests](../server/internal/agenttools/harness_env_test.go).
  Run `mise run check`, which covers the
  [Go](../server/internal/harness/boundary_test.go) and
  [UI](../packages/app/tests/boundaries.test.mjs) harness boundaries.

## Amp mapping

Rechecked against Amp's first-party docs on **2026-11-27** for
[ADR 0008 D13](adr/0008-harness-foundation.md#d13). This is a design case.
The gaps below need runtime evidence before a Stage module can rely on them.

| Facility | Documented evidence, Stage mapping and gaps |
| --- | --- |
| Account and machine | The CLI lists saved accounts per Amp server and can select one by user ID. Running processes keep the account they started with. A runner can be given a stable, operator-selected `--runner-id`. REST thread metadata can include the CLI or runner hostname. **Inference:** key a CLI or runner connection by account and Stage machine, with server and runner identity where needed. Declare local process requirements for CLI or runner hosting and cloud session locality for Amp's synced threads. **Unverified:** how to prove the active principal for the actual credential, bind an operator-selected runner ID to one Stage machine, and prove continuity after replacement or a move. [CLI accounts](https://ampcode.com/docs/cli#manage-cli-accounts), [Runners](https://ampcode.com/docs/cli/runners#start-a-runner), [External API](https://ampcode.com/api/external), [Threads](https://ampcode.com/docs/threads) |
| CLI and SDK launch | Local `amp -x --stream-json` starts its JSON output with a `type: system`, `subtype: init` object whose `session_id` is the `T-…` thread ID. Streamed `-ox` uses the same format. Detached `-ox` and `-x --executor runner:<id>` print the thread URL and exit while the turn runs remotely. Streamed execute can archive a new thread after the turn unless configured otherwise. The SDK can execute on a selected runner and served directory. **Inference:** capture the ID under the Stage connection and verify recovery after launch succeeds but before Stage saves the ID. A reserved ID, idempotency key and reliable marker lookup across modes are **unverified**. [Streaming JSON](https://ampcode.com/docs/cli/streaming-json), [Execute Mode](https://ampcode.com/docs/cli/execute-mode), [Spawning Orbs](https://ampcode.com/docs/cli/spawning-orbs), [SDK](https://ampcode.com/docs/sdk) |
| REST launch, observation and reply | `POST /api/v2/threads` creates a project orb thread and its first message, returning `thread.id`. It requires a user-scoped Sign in with Amp token. Only M2M applications are generally available, and M2M cannot create threads. REST lists scoped threads and reads metadata and messages by ID with appropriate scopes. Metadata can lag and reports `working`, `idle`, `awaiting_approval` and `error`. REST follow-ups are limited to the token owner's own orb threads. The experimental list SSE stream covers a limited subset of recent threads for eligible Sign in with Amp tokens. **Unverified:** usable credentials and coverage for a Stage integration across local, runner and orb execution, plus authoritative turn completion or replay after a gap. [External API](https://ampcode.com/api/external) |
| Approval and input | The documented machine-readable waiting state covers tool approvals. Amp normally runs tools without approval, while remote MCP policy and plugins can request approval. Amp clients also display questions and plugin input dialogs. **Inference:** map a verified approval wait to `needs_input` with a declared approval reason. **Unverified:** a distinct general question or free-text wait in the external state contract, and a supervisor API to enumerate and answer either kind of pending request. Keep Stage Respond unavailable until the relevant contract is verified. [External API](https://ampcode.com/api/external), [MCP approvals](https://ampcode.com/docs/customize/mcp#controlling-remote-tools), [Plugin API](https://ampcode.com/docs/plugin-api), [Threads](https://ampcode.com/docs/threads) |
| Stop by execution mode | Local execute exits on SIGINT or SIGTERM. The TypeScript SDK accepts an abort signal. Inside a plugin, the thread handle from `amp.threads.get(threadID)` exposes `cancel()` to stop a current turn. **Unverified:** that local cancellation yields a durable cancelled result after process exit, that closing an attached stream or aborting the SDK stops the remote turn, and that Stage can cancel an already-detached runner or orb thread by ID with an authorized call. REST documents thread deletion. Orb archive pauses the orb. Gate Stop on a verified turn-cancellation path for the selected mode. [Execute Mode](https://ampcode.com/docs/cli/execute-mode), [TypeScript SDK](https://ampcode.com/docs/sdk/typescript), [Plugin API](https://ampcode.com/docs/plugin-api), [External API](https://ampcode.com/api/external), [Orb lifecycle](https://ampcode.com/docs/orbs/sizes-and-costs#when-an-orb-pauses) |
| Workspace inventory | Amp matches projects by Git remote URL and aliases. REST lists workspace-owned projects. Runners serve selected directories or discover bounded Git checkouts, and `amp runner dirs list` lists served directories from the runner machine. **Inference:** use that list as a candidate inventory and enrich paths on their owning machine with Stage's read-only repository evidence. **Unverified:** a stable machine-readable list contract, full coverage for local CLI and remote runners, and authoritative deletion, repository subpath and file-host provenance across modes. [Projects](https://ampcode.com/docs/projects), [External API](https://ampcode.com/api/external), [Runners](https://ampcode.com/docs/cli/runners#change-directories-while-the-runner-runs) |
| Caller attribution | A plugin has `ctx.thread.id` and local tool events carry a thread ID. Amp's hosted remote MCP definitions can send a signed workload identity token with `thread_id` and the owner's `user_id`. Orbs can also send an Amp ID token to configured HTTP MCP servers. Runner variables can be shared per served directory. **Unverified:** a trusted per-call stdio MCP field or CLI process variable for the current thread, and an end-to-end mapping from verified Amp claims or a plugin invocation to the right Stage connection and external session across modes. Thread ownership identifies the thread even when another person sent the latest message. [Plugin API](https://ampcode.com/docs/plugin-api), [MCP workload identity](https://ampcode.com/docs/customize/mcp#amp-workload-identity), [Orb identity](https://ampcode.com/docs/orbs/handling-secrets), [Runners](https://ampcode.com/docs/cli/runners) |
| Extras | Amp documents archive, sharing, labels and native handoff. REST usage has credential and retention limits. **Inference:** use declared session actions for share, label and Amp-native handoff, and facts for display-only usage details. Stage retains its own durable, accepted handoff. [Threads](https://ampcode.com/docs/threads), [External API](https://ampcode.com/api/external) |
