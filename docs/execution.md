# Stage execution contract

Stage launches tasks through harness connections and records their execution.
`server/internal/execution` defines the shared contract. Harness adapters
translate their native protocols, options and session state.

## Ownership: Stage and the harness

- **Keep Stage lean.** Stage owns logical project groups, tasks, trees,
  schedules, journals, acceptance and durable handoff. Each harness
  connection owns its workspace catalog and sessions. The harness also owns
  worktrees, branches, archive, models, UI and processes. Stage uses read-only
  evidence to group workspaces for navigation. [Harness boundary](adr/0007-harness-boundary.md)
  · [Workspace ownership and transition](adr/0010-harness-workspaces-and-logical-projects.md)
- **Use generic code and declared policy.** Stage reads descriptors and
  capabilities to make its decisions. Each adapter validates and translates its
  own options. Harness-specific operations and display data use declared actions
  and facts. The boundary tests enforce where harness names can appear.
  [Harness foundation](adr/0008-harness-foundation.md)
- **Scope every session.** A session is `{connectionId, externalSessionId}`.
  Parents, adoption, task events and journal provenance use the connection of
  the session they describe. A bare external ID carries no connection identity.
- **Pin exact destinations.** The [target contract](adr/0010-harness-workspaces-and-logical-projects.md#ownership-and-exact-destinations)
  pins a workspace on the selected connection and validates it before
  launch. Grouping cannot supply a launch destination. During the
  transition, [shipped bindings](discovery.md#bindings-and-review-07-backend-08-ui)
  can still supply the primary workspace under the legacy selection rules.
- **Attribute the caller independently of the destination.** Agent tools
  accept `callerSession`. A harness can declare a native per-call metadata
  key (OpenCode supplies its session ID in MCP metadata) or a session
  environment variable. HTTP MCP can use `Stage-Caller-Session` and the CLI
  can use `STAGE_CALLER_SESSION`. The launch's journal
  instructions carry the descriptor's caller hint. Stage records the caller's
  session on created work and journal entries.
  [Agent interface](api.md#agent-interface)

The main server composes `coordinator`, `observe` and `discovery` in
`internal/engine`. The coordinator owns launch, link repair, handoff and
`PollTask` and its apply path. Observation selects the watch set, runs beside
the adapters and ingests samples on the main server. Discovery reads workspace
inventory and filesystem evidence and owns full sync.

Stage records the admitted actor when it claims a launch. Handoffs and
adapter outcomes link to the recorded attempt, including across process
restarts. Dependency release links to the latest current prerequisite
acceptance. Scheduled launches derive their source from the latest start
time edit or creation event. Harness observations record automation with
the verified connection and session. Notifications receive that same
attribution from the committed transition. [Work attribution](api.md#work-attribution)

## Modules and the registry

The ordered module list in `internal/harnesses/modules.go` supplies the
registry and API list. Each `harness.Module` provides a descriptor,
`Compose(context.Context, harness.Ports)`, a public settings validator and
any environment or legacy secret declarations. Serve and the worker compose
the same modules in registry order, subject to the machine's hosting policy.

### Descriptor policies

The descriptor declares process requirements, session locality, launch
identity, caller attribution, wrapper origins, state reasons, closure
signals and attention policy. It also supplies connection fields and actions,
disconnect availability, session and discovery hints, and link policy.

`localProcesses` requires a profile that permits those processes (Claude
Code and Codex). T3 and OpenCode connect to services. All four declare
machine-local sessions. Cloud session locality is independent of process
requirements. [Machine profiles](runtime.md#machine-profile-and-hosted-harnesses)

`linkSchemes` lists the harness's native URL schemes, such as T3's
`t3code`. The registry checks them for valid lowercase syntax, uniqueness
and reserved names. Reply Markdown allows standard web, FTP, email, phone,
Stage and relative links. Harness links gain a destination after the host
loads and validates the descriptors. Script URLs stay blocked.
[Descriptor API](api.md#harness-connections-api-v6)

### Connection setup and ports

A setup package implements `harness.Connector` for its machine and kind.
A connector supplies an in-memory summary, status, configure, run and
disconnect. Status includes a common phase, facts and an optional fix,
plus harness details. The API attaches full-sync time, error and progress as
`sync`. Hosts render declared settings and actions or use a registered setup
panel. The API exposes the same setup routes on each machine.
[Connection API](api.md#harness-connections-api-v6)

`Compose` takes machine-bound ports. `Settings.Get` reads a public JSON
object by kind and `Settings.Save` applies a partial update. Omitted fields
keep their values. Null resets a setting to its module default. `Secrets`
addresses opaque values by kind and key. The local placement can commit
a settings patch and secret edit together. Setup owns settings types and
credential formats. `Identity` reads, switches or stamps an instance and
supplies the evidence Stage stored for continuity checks. The module also
receives its data directory, profile, environment, binary lookup and logger.
It installs adapters through the install port and returns the connector and
optional cleanup for its client resources.

### Connection identity

Stage seeds no default harness or connection. A connector is available
before a connection exists. The first successful pair or connect creates
the connection through the identity port. Automatic-completion preferences
belong to that connection.

An identity is unique per machine and kind. A stable identity such as a T3
environment ID revives its retired connection on the same machine. When an
address is insufficient, as with OpenCode, the module proves continuity from
stored evidence and selects the connection to revive. A stable-identity
module may also declare a moved-retire predicate. Stage evaluates it using
both connector summaries when the old machine is reachable.

Replacing an instance retires its connection. Tasks, sessions and bindings
remain on it as history. Unlaunched backlog tasks and schedules follow the
new active connection. Bindings change through workspace discovery or a
user action. Serve follows connection changes and runs release and schedule
runners per active connection.
[Connection storage and lifecycle](architecture.md#project-and-session-identity)

## Adapter roles

`execution.Adapter` embeds four roles. `machine.Connections` resolves the
full adapter or a narrow role from the connection's machine. The local
adapter and remote proxy implement the same contract.

### Catalog

`Health` reports availability, a reason, instance identity and the check
time. It is independent of session outcomes. `Features` describes models,
options, permissions, modes and supported operations with reasons when
unavailable. `ModeLabel` names the mode selector in the harness's words.
A workspace-scoped catalog uses `WorkspaceFeatures` with the selected
binding's external project and directory.
[Capabilities API](api.md#get-apiconnectionsconnectionidcapabilities)

### Inventory

`Workspaces` reports harness project IDs, directories, timestamps,
availability and import evidence. The adapter marks paths on its machine's
filesystem. Discovery uses that machine's `Ops.Inventory` to enrich those
paths, refreshes stored workspaces and runs project import. Failed inventory
reads leave the last stored inventory in place. Full sync guards against
concurrent runs per connection, reports progress and records successful sync
for the active connection. [Workspace discovery](discovery.md)

### Sessions

`Observe` returns the current snapshot for a scoped session.
`Opening` reads its first prompt, including the item ID. `Attention` lists
sessions the harness surfaces to Stage, such as sessions waiting on the user. Stage adds a task through explicit adoption.

### Launcher

`Launch` starts execution from a frozen intent and reconciles a prior
dispatch when the launch policy permits it. `Stop` interrupts a session
when its operation is available. The harness performs the native command
against the scoped session.

### Optional traits

Use `execution.As[T](role)` to reach a trait and check the corresponding
operation or descriptor policy before dispatch. `execution/optional.go`
registers `LinkResolver`, `WorkspaceFeatures`, `Correlator`,
`EventSource`, `Interactive` and `Actor`. A local adapter implements
the Go interface. A partial adapter, such as a worker proxy, must also
declare that trait in the connection's current ready snapshot. Unknown traits
are ignored. Channel negotiation also scopes each request and harness kind.

### Errors

| Error | Meaning |
| --- | --- |
| `Rejected` | The launch certified that no execution started. Stage returns the task to Backlog for repair. Other launch failures leave the attempt unconfirmed. |
| `Refused` | Stage declines to track a session that a registered harness wraps. The API returns 409 `session_refused` with where to track it. |
| `Invalid` | The answer does not fit a pending interaction request. Nothing was sent. The API returns 400 and the worker channel uses `invalid_answer`. |
| `Busy` | The session cannot take a message now. Nothing was sent. The API returns 409 `session_busy` with the reason and the channel uses `busy`. |

`ErrUnavailable`, `ErrNotFound` and `ErrNoReply` preserve their meaning
over the channel (`unavailable`, `not_found`, `no_reply`). An absent
trait or operation returns `operation_unavailable`. Only an adapter that
can certify rejection may return `Rejected` after dispatch.

## Launch

Manual starts, schedules and automatic tree starts share the coordinator.
A launch claim pins the verified destination and accepted dependency inputs.
Reconciliation verifies the pinned workspace on the same connection.

### Intent, options and checkpoints

`execution.Intent` carries the request ID, connection, external project,
directory, title, prompt, execution configuration and images. Tasks and
schedules store `executionConfig`. Model, permission and mode are opaque
adapter keys. The selected model's options and the harness-wide options
share one namespace. Option kinds are `choice`, `boolean` and `text`.
A choice takes a declared value, a boolean takes true or false, and text takes
a nonempty trimmed string. Omit an option to use its default. A `dependsOn`
condition must hold for a dependent selection. `Capabilities.Valid` checks
the declarations and `ValidateConfig` checks selections.

The adapter owns worktree creation. T3 declares `worktree`, `baseRef` and
`branch`, with the last two applying to a worktree launch. OpenCode and
Claude Code offer a worktree option and choose the base themselves. Codex
runs in the selected checkout. Handoff carries a predecessor's reported
branch as context.

`Launch` receives the immutable intent and an adapter-owned, versioned JSON
checkpoint. Save the checkpoint durably before each external effect and
stop if the save fails. Checkpoints contain references and non-secret identity
only. For multi-step creation, persist the identifiers needed to reconcile
each step before dispatch. Supply images through the intent. On a worker,
`launch.image` fetches each image by index and `checkpoint.save` calls the
coordinator's `SaveCheckpoint`. After the launch call stops waiting, the
channel refuses further saves with `call_ended`.

### Launch identity

- **Reserved** (default) uses a deterministic or checkpointed identity to
  reconcile the same session and input on replay.
- **Correlated** covers a harness that assigns the session ID and whose
  create operation cannot safely be repeated. Stage appends
  `execution.LaunchMarker(requestId)` to the prompt, records the dispatch
  and calls `Launch` once. Recovery uses `execution.Correlator` to find the
  session by that marker. If none is found, the attempt stays unconfirmed
  and supports explicit linking. The correlator is required for launch.

### Durable attempt lifecycle

The claim transaction creates a prepared attempt. Stage freezes the prompt,
journal instructions, config, images and destination before calling the
adapter. Interrupted preparation with no frozen intent returns the task to
Backlog. Attempts without enough identity for safe recovery require linking.

A lost reply or timeout after dispatch leaves the attempt **Launch
unconfirmed**. Recovery retries with backoff (5 seconds to 1 minute),
including after restart, through the checkpoint or correlation policy.
When the harness returned a session but Stage could not persist the
observation or confirmation, the attempt retains the error, backoff and
reported session. Task detail can open that session or display its resume
command. Once storage recovers, a retry can record the session.

A per-task lock serializes reconciliation and manual attachment. Database
revision checks reject stale checkpoints and attachments. The server
data-dir lock prevents concurrent owners of the same database.

### Link repair

An optional `LinkResolver` translates a pasted ID, URL or command.
Preview verifies the pinned destination, current environment, top-level
session and ownership. Attachment rechecks those facts and the attempt
revision. The repair attaches the existing session without dispatching
launch or granting acceptance.
[Task API](api.md#tasks)

### Handoff and journal instructions

Only acceptance releases dependencies. When handoff is enabled the claim
pins each need's immutable accepted result, even if the source harness is
offline or the source task is later reopened. The coordinator builds the
blocks from Stage storage and records them per attempt. They include the
accepted evidence, cached reply excerpt, journal references and any reported
branch, worktree or session link. Excerpts preserve truncation and
run provenance. The budget per need is `min(4000, max(1000, 8000 / sources))`
Unicode characters. Retries reuse the stored blocks. A storage error aborts
launch preparation.

Every launch adds separate journal instructions, including launches without
handoff. They identify the task and tree at launch, the connection, the
agent transport and caller hint. They ask the agent to refresh task and tree
context, read relevant notes and journal progress and outcomes. Retries
reuse the instruction block. [Handoff API](api.md#handoff)

## Observation

### The snapshot

`Observation` contains scoped session and project identity, activity, child and
archive flags, the reported directory and branch, links and a bounded reply. A normalized state is `running`, `needs_input`, `done`,
`failed`, `interrupted`, `cancelled` or `idle`. The descriptor must declare
any `stateReason`. Only `running` and `needs_input` are active. For example,
`owner_exited` requires `interrupted` and means that an execution owner exited without reporting a
result. A missing, offline or malformed snapshot provides no evidence of
failure or completion.

A `Reply` has `itemId`, `runId`, `text`, `truncated` and `updatedAt`.
Acceptance uses the IDs to identify the cached reply and its execution.
`Links` can carry a web address, a native address and a resume command.
Hosts choose where to open a session through `Links.executionDestination`.
Without a destination, the task UI offers to copy `Links.command` and names
the machine when more than one is configured. Both placements read links
from the stored observation. An unconfirmed attempt can retain the reported
command when that write fails. Acceptance and handoff retain their captured
session link (web first, then native).

`Usage` is optional, cumulative and best effort. It carries turns, uncached
input tokens, cache read and write tokens, output tokens, finished-turn
working time (0 when unknown) and the harness's reported cost (null if
absent). Stage keeps the last reported usage when an update omits it and
does not estimate cost. Task detail shows the current session's usage.

### Polling, streams and attention

The main server builds the desired watch set per machine. It lists each
connection's attention eligibility and the external session IDs to read with
their cadence. It sends the whole set at attachment and whenever the set
changes. The latest generation in a server epoch replaces the previous set.
A server restart starts a new epoch. Removing a watch cancels its work and
discards late results from that watch.

The same runner serves the local machine in process and runs inside each
worker. Each connection has independent observation work. Fast polling
and attention discovery use a five-second interval. Stale sessions,
including unaccepted done tasks awaiting a late closure, are read every
minute. Polling reconciles missed events. Full sync still refreshes
inventory. Launch reconciliation and explicit fresh reads keep their
request/response paths.
[ADR 0008 D11](adr/0008-harness-foundation.md#d11)

An `EventSource` streams observations to the connection's local runner.
The stream retries after five seconds, with the last error exposed in
status until recovery. The attention loop reads sessions from the adapter
and refreshes previously watched ones. It also follows discovered active
sessions after they leave the feed, including active attention samples in
the outbox when the worker restarts. For unowned sessions, the default
`notify` policy (T3 and OpenCode) surfaces waiting rows, and after startup
can send an inbox event and push for transitions into `needs_input`, `done`
or `failed`. Claude Code's `silent` policy surfaces waiting rows and the
needs-input count. Adoption creates the tracked task.

### Ingest and transitions

Local samples, worker batches and external pushes converge on the main
server's observation ingest rules. Ingest looks up the task by scoped session
and delegates to the coordinator's apply path, also used by `PollTask`.
Unowned sessions follow attention policy, and launches reconcile their own
sessions. Apply verifies the session and descriptor policy, stores the
observation and applies the transition. The harness's `updatedAt` orders
snapshots first. When it ties, Stage uses the sample capture time or the
time the main received a bridge push. Bridge task events still use the
harness timestamp. An idle observation leaves a known task state in place.

The worker seals records with contiguous cursors and sends one batch at a
time. The server commits each record with its durable effects or permanent
disposition and acknowledges the committed prefix. Replay after a lost
acknowledgement leaves task transitions, journals and durable notifications
recorded once. Replay keeps the sample times, and transient notifications
follow commit. A reported gap asks for a fresh sample of the watch set.
[Worker channel](runtime.md#worker-channel) · [Worker observation files](runtime.md#worker-observation-files)

### Closures and inferred completion

A closure carries a signal from the harness, such as T3 `settled` or
OpenCode, Codex or Claude Code `archived`. A descriptor maps a declared
signal to acceptance provenance, a journal event and the toggle and result
copy. With no declared closure, the connection has no auto-complete option.

Inference requires per-connection `autoComplete` and a successful execution
recorded as `done`. An idle observation can still carry a closure, in which
case inference checks the stored task state. Stage infers completion once
per execution. Acceptance snapshots are durable and release dependencies.
[Inferred completion](api.md#user-acceptance-api-v4)

### Wrapping and refusal

A wrapper declares `wraps.origins` for the markers it leaves in sessions
run through it (T3 leaves `t3-code` in Claude Code and `T3 Code` in
Codex). Each origin has one owner in the registry. Adapters report the
raw `Observation.origin`, including across the worker channel. Stage
refuses tracking through the wrapped harness in adoption, preview, link
repair and polling, and omits those sessions from attention. The reason
directs the user to the wrapper.

A harness that records origin can also declare an attribution origin
variable. When resolving a caller, a wrapped harness's session variable
yields to the wrapper's variable or a wrapped origin, unless a declared
precedence condition owns the session.

### External push

A bridge or hook may push snapshots for an existing connection to
`POST /api/connections/{id}/observations`. The handler checks the
connection and sends each observation to `observe.Service.Ingest` for the
same validation and descriptor policy as adapter events. Each result
is `ingested`, `ignored` or `invalid` with a reason where applicable.
A pushed snapshot needs `updatedAt`. Timestamps more than five minutes
ahead of the server are invalid. A bridge can push for any active
connection while its worker is offline or incompatible. Polling continues
to reconcile lost events.
[Observation API](api.md#harness-connections-api-v6)

## Extension kinds

[The harness authoring guide](harnesses.md) covers where to implement each
extension. Stage-wide features work across harnesses or degrade through
declared capabilities.

### Typed operations

Use a typed operation when Stage branches on availability.
`execution.Op` (UI `OperationId`) covers `launch`, `stop`, `adopt`,
`reply`, `opening`, `attention`, `images`, `respond` and `send`.
Every adapter declares each operation and a reason when unavailable.
For example, `images` gates image fields and image delivery in the API
and MCP. The remote proxy passes the declarations through.

### Interaction

The `Interactive` trait exposes requests, responses and messages behind
`respond` and `send`. An approval takes an option and may accept text
for a designated option. A question group takes answers keyed by question
ID, using option labels or custom text when allowed. An input takes free
text. A preview can describe the command, path, URL or input the request
acts on. A request that is no longer pending returns `ErrNotFound` (404).
A successful follow-up records a task timeline entry. Only users can send
and respond. Claude Code supplies both operations, with responses requiring
its hook receiver. [Interaction API](api.md#session-interaction-api-v6)

### Actions and facts

A declared action is a harness-owned operation Stage relays. `execution.Action`
carries the target, stable ID, caller policy, availability, confirmation
and input schema. Connectors own connection actions, including setup.
`Capabilities.Actions` describes session and project actions. Project
actions use the selected binding's connection and workspace. Stage resolves
that scope, checks caller policy and schema, and reaches the owner through
`execution.As[execution.Actor]`. The owner validates semantic input.
Workers advertise the `actor` trait and receive `adapter.act`.
A lost reply leaves the outcome uncertain. Stage does not replay the action.

A successful session action records a redacted journal event. Dispatch runs
outside the journal transaction, so a storage retry cannot repeat the action.
`execution.Fact` carries display data in status, observations or action
results. Stage filters malformed or duplicate facts and clears unsafe links.
[Actions and facts API](api.md#declared-actions-and-facts)

## Machines

### The machine seam

`internal/machine` hosts adapters, connectors and local operations. A machine
has an ID, online status, a hosting profile and `Ops`. `machine.Set`
tracks the local machine and joined workers. `machine.Connections` resolves
an adapter or role using the connection's machine and its registered kind.
A retired connection returns `connection_retired`. An unhosted or
disallowed connection returns `connection_unavailable` with the reason.

`Machine.Allows` is the hosting decision shared by setup, the connector
index and adapter lookup. The local machine uses its profile. Remote
machines use the worker's advertised profile and available setups. A worker
that allows local processes but lacks a setup reports the reason to update
it. Before the first profile arrives, availability is unknown and requests
report offline or starting as appropriate. A disallowed setup route returns
409 `harness_unavailable` with the fix. Serve and workers compose only
allowed modules.

`machine.Local` hosts machine 1 in process. `serve/localmachine.go`
composes its modules. Setup uses `Local.Install(old, next, adapter)` to
install or move a connection's adapter (nil uninstalls). Connection change
hooks wake the engine and resync runners.

`machine.LocalOps` enriches filesystem-local inventory with absolute
canonical paths, Git metadata including the common directory for linked
worktrees, and checkout import eligibility. Claude Code uses
`checkoutTop` and Codex `inCheckout`. Stage stores the evidence and resolves
project identity on the main server. Raw `WorkspaceInfo.OriginRemote` can
contain credentials and is kept out of client responses.
[Workspace evidence](discovery.md)

### Remote machines

A joined machine is a `remote.Machine` on the main server. Its worker
serves `remote.Host` over the authenticated channel, dispatching calls to
a `machine.Local` and its ops. The worker has no Stage database. The
server keeps the store, task state and acceptance.
[Worker channel](runtime.md#worker-channel)

- **Adapter and ops calls.** The proxy scopes calls to ready connections
  and advertised traits. It forwards inventory enrichment as
  `ops.inventory` with `{workspaces}`, and caches health for five seconds
  within a ready snapshot. The launch intent travels without image bytes.
  The worker calls back for images and checkpoint saves.
- **Observation.** A worker with both negotiated observation kinds accepts
  `observe.watch` and sends `observe.batch` independently of command calls.
  Its runner polls and subscribes beside the adapters. Stream errors are
  visible in status and logs.
- **Setup.** `remote.Connector` proxies `connector.*` status and mutations.
  The worker composes only kinds the server advertises that its profile
  allows. `setup.snapshot` supplies public settings and active connection
  identity for the selected kinds. `setup.settings.get` (`{kind}`),
  `setup.settings.save` (`{kind, patch}`), `setup.identity.*` and
  `setup.status.error` use the authenticated machine. Active identity reads use
  `setup.identity.activeIdentity` and connection checks use
  `setup.identity.exists`. The main server stores public settings,
  identity and sync status. Credentials stay in the worker's
  `credentials.json` (mode `0600`, deleted on join and leave). A secret
  entered during setup can reach the worker through the intended configure
  or run call. The worker restores adapters from its local public settings
  cache and reconciles with the main server on reconnect.
  Pair and connect use the same retirement and revival rules as local
  setup and wake sync and scheduling when ready.

### Offline

Offline, negotiating and incompatible workers report availability reasons
immediately. A worker with a valid cache and watch set keeps observing across
a channel outage and worker restart. The main server keeps the last applied
state until it ingests new evidence. A new launch is refused with
`connection_unavailable`.
A dispatched attempt stays unconfirmed and retries through its saved
checkpoint or correlation policy when the worker returns. Only a worker
adapter's certified rejection can return a dispatched task to Backlog.

### Verification

The machine JSON round-trip tests cover the adapter contract, setup ports
and ops. The `internal/engine` contract suite exercises local adapters
and remote proxies through a real `worker.Run` and a loopback channel
(`remote/remotetest`). Cases cover lost replies, restarts during launch,
offline reads and acceptance, failed and late checkpoint saves, images and
traits. Fake harnesses cover deterministic two-step launch, a correlated
harness with assigned IDs and a hosted harness that pushes, loses its
owner and answers interaction. API tests cover immutable handoff and
journal context, destination repair and link ownership and revisions.

`workersetup` tests run over a real channel and store. They cover T3 pairing,
OpenCode credentials on the worker, caller machine scoping, and Claude Code
and Codex setup and launch against their fakes, including stored resume
commands and disconnect. Profile tests cover workers without local
processes and harnesses an older server omits.

## T3 adapter

The T3 adapter maps a T3 environment's projects and threads to a connection.
The adapter sends a root `workspaceStrategy` by default and a worktree
strategy with `baseRef` and `branch` when `worktree` is enabled. A ref
starting with `origin/` or `upstream/` sets `startFromOrigin`. A worktree with no base ref is rejected.

Checkpoints pin the OAuth authentication session namespace, environment,
deterministic thread ID and upload references. Re-pairing cannot replay
the old namespace. A known thread can still be read in the same verified
environment after re-pairing. A conflicting launch reply is held for
explicit linking. Uploaded images stay with uncertain attempts and may be
discarded on certified rejection. T3 tests cover namespace rotation and
failed checkpoint saves.

Settlement or an idle thread with a prior run reads `done` when no active
work takes precedence. A thread that has never run stays `idle` and adopts
as Backlog. The newest run supplies the observed state and end time, skipping
cancelled or rolled-back runs that never started, such as queued follow-ups.
Settlement reports a `settled` closure and grants acceptance only through
inferred completion.

## OpenCode adapter

Stage connects to an existing OpenCode V2 service using its URL and optional
HTTP Basic credentials. It checks the required operations and worktree, session
and prompt request schemas in `/openapi.json`. If a requirement is missing,
the error names the reported version and operation. Any version that meets
those requirements can connect. The live suite below records the tested
binary and should run again when a release changes behavior Stage relies on.

OpenCode owns project inventory, workspace strategy, model selection and agent policy.
An empty selection uses its defaults. A bound directory provides the model catalog,
variants and visible primary agents. The capabilities endpoint accepts an optional
Stage `projectId` to resolve that binding. Its launch option `worktree`
(boolean, default off) asks OpenCode to create the worktree using its
default base. Stage verifies the resulting inventory entry, project and
directory. Permission overrides and image delivery report a reason when
unavailable.

### Launch and recovery

Stage saves the frozen intent, reserved IDs and an attempt marker before dispatch.
The three external effects are worktree creation, session creation and prompt admission.
The adapter performs only the steps the launch options need.

| Lost response | Recovery |
| --- | --- |
| Worktree creation | Hold the attempt for explicit session linking. OpenCode can suffix the requested name and records the directory before running setup. An inventory entry alone cannot prove success. |
| Session creation | Read the reserved ID and verify the attempt marker, project, directory and top-level status. A missing or conflicting identity stays unconfirmed. |
| Prompt admission | Check the pending inbox, then the delivered message by ID, text and marker. Confirmed admission waits for execution evidence. |

The live suite found that duplicate session creation returned the first session,
even with different arguments. Concurrent submissions of the same message ID produced
one pending input. After delivery, resubmission returned the original input and woke
a new execution interval with no additional model call in the tested case. Recovery
therefore reads the original admission instead of sending the prompt again.

Stage polls active execution, pending permissions and forms, and the session's
busy and idle times. A new active execution takes precedence over the previous
outcome. Inconsistent snapshots are retried. Terminal replies use completed assistant
text from that execution, bounded to 4,000 characters. A pasted ID or a
matching OpenCode web-client session URL supports explicit adoption. Browsers and
desktop hosts use the configured web-client destination. User acceptance and offline
handoff use Stage's stored evidence.

Adopting an idle OpenCode session before its first execution creates an attached
Backlog task with no start or finish time. Stage polls it and records execution
when work begins in OpenCode. Stage start, scheduling and tree readiness apply
only to work Stage can launch.

### Reproduce the live gate

Run `STAGE_TEST_OPENCODE=/absolute/path/to/opencode mise run opencode:live`. Include `STAGE_TEST_T3` (T3 executable) and `STAGE_TEST_FX` (upstream `scripts/mobile-behavior/fake-fx.ts`) to exercise a real T3 to OpenCode to T3 tree with both predecessor services disconnected in turn.
The test starts foreground loopback servers with temporary homes, repositories and
databases. A scripted local model supplies success, failure and interruptible runs.
The standalone CLI requires Basic authentication. The suite also tests an existing
service exposed through a no-auth loopback proxy.

The verified Linux amd64 binary reported `2.3.0`, SHA-256
`2e0e4656521ca56e5ba16a00d48429958b8105b3fa6466710a4350e68759381f`.
The source reference was `df46f6931da0c934a59d375ac44525846f21c718`.
Fault injection covers every launch checkpoint, lost HTTP responses, racing Stage
retries, failed worktree setup, session identity collisions and server reconnect.
The suite also checks adoption, input requests, the web-client route and offline
acceptance. No provider account or production service is required.

`STAGE_TEST_OPENCODE=/absolute/path/to/opencode mise run opencode:workflow` starts a scratch Stage server and OpenCode with a local scripted model. It verifies connection setup, model selection, native MCP task and journal provenance, explicit adoption, a clock-driven schedule and restart recovery through the public interfaces. It requires Python 3.9 or newer.

## Claude Code adapter

`internal/claudecode` drives the `claude` CLI; Claude's per-user daemon owns
the sessions, worktrees, models and login ([ADR 0005](adr/0005-claude-code-harness.md)).
Claude Code has no HTTP API. Kind `claude`, local processes required, machine sessions, stable identity
(the `configDirectory` from `claude auth status --json`), launch
`correlated`, closure `archived` (`claude rm`). Setup (`internal/claudecodesetup`): the command
setting, a connect action and disconnect ([api.md](api.md#harness-connections-api-v6)),
stored per machine in `harness_settings` with the config directory the
connection was made with; `serve` composes it only where the machine allows
hosted harnesses. On a worker (`workersetup`) it runs on that machine, with
that machine's `claude`, login and daemon, when the worker allows local
harnesses (the worker service, or `stage worker run --local-harnesses`);
its settings live on the main server's `harness_settings` row for that
machine ([Remote machines](#remote-machines)). The resume command then has
to run on that machine.

- **Running claude.** Stage clears the variables a Claude or T3 process sets
  for its own session (`CLAUDECODE`, `CLAUDE_PID`, `CLAUDE_AGENT_SDK_VERSION`,
  `CLAUDE_CODE_SESSION_*`, `CLAUDE_CODE_CHILD_*`, `CLAUDE_CODE_ENTRYPOINT`,
  `CLAUDE_CODE_EXECPATH`, `CLAUDE_CODE_MESSAGING_*`, `CLAUDE_JOB_DIR`,
  `CLAUDE_BG_BACKEND`, `T3CODE_*`, `T3_*`) and keeps Claude's settings
  (`CLAUDE_CONFIG_DIR`, provider variables). It adds the usual tool folders
  to `PATH`: the first launch starts Claude's daemon, and every background
  session inherits that launch's environment. On Linux, when
  `systemd-run --user --scope` works (probed once), a launch runs in its own
  transient scope, so the daemon it may start is not in Stage's service
  cgroup and outlives a Stage restart.
- **Launch.** In the project folder: `claude --bg -n <title> [--model M]
  [--effort E] [--permission-mode P] [-w stage-<requestId>] [--settings
  <file|json>] -- <prompt>` (the prompt carries the launch
  marker; `--` keeps a prompt starting with `-` from reading as a flag).
  `-w` follows the launch option `worktree` (boolean, default off); Claude
  makes the worktree from its own default base, and the per-attempt name
  keeps a retry from reusing an earlier attempt's worktree. Claude takes
  one `--settings`: the hooks file, which carries the image root's read
  rule (Images). Without hooks it is inline JSON with the read rule for
  images, else none. An existing `hooks-settings-head.json` also receives the
  current hook settings because sessions using that file read it on wake.
  An unsupported config, an image type outside PNG, JPEG, GIF or WebP,
  a missing folder or executable, images without an image root, and Claude's `Workspace not
  trusted` refusal are `Rejected` (the task returns to backlog with the
  reason; trusting a folder is done in Claude). Exit 0 prints
  `backgrounded · <short>`; the session list maps the short id to the
  session within 5 seconds. Anything else is uncertain.
- **Images** are files Claude reads with its Read tool, kept out of the
  user's repository ([ADR 0007](adr/0007-harness-boundary.md) decision 10).
  Before running claude, Stage writes them to the image root,
  `<data dir>/claude/images/stage-<requestId>/` (a worker's own data dir on
  a worker; the setup passes the root to the adapter and the hook receiver;
  folders `0700`, files `0600`, named `<n>-<name>.<ext>` with the name kept
  to `[A-Za-z0-9._-]` and the extension from the type). The launch's
  settings allow `Read(//<image root>/**)` (`permissions.allow`; `//` is an
  absolute path), so Claude reads them without a permission prompt in any
  mode, from the project folder or a worktree, and appends `Images attached
  to this task (open each with the Read tool):` with one `<n>. <name>:
  <path>` line each, after the marker. No `--add-dir`: Claude refuses an
  added folder it does not trust (ADR 0005 E8). A rejected launch removes
  its folder; an uncertain one keeps it. Each launch with images removes
  image folders older than 30 days (best effort); Claude keeps what it read
  in the transcript. Without an image root (no data dir) `images` is
  unavailable with a reason. The adapter cleans up the legacy
  `<project>/.claude/stage-images` directory on launch and first workspace
  discovery. Cleanup removes
  Stage's `stage-*` folders and `.gitignore`, then the directory if empty.
- **Correlate.** Background sessions listed in the launch folder (or its
  per-attempt worktree, `<folder>/.claude/worktrees/stage-<requestId>`) and
  the transcripts of those folders, matched by the marker in the first
  prompt. Several matches are uncertain; none is not found.
- **Observe.** One `claude agents --json --all` read per tick, shared by
  every session of the connection (reused for 2 seconds), plus the
  transcript (`<projectsDirectory>/<cwd with / and . as ->/<uuid>.jsonl`,
  else any project folder). Background sessions map by `state`: `working`
  running, `blocked` needs_input, `done` done, `failed` failed, `stopped`
  interrupted; a terminal, IDE or SDK session by `status`. Not listed:
  idle with its reply when the transcript exists, else not found. An
  unreadable list is an error, never a state.
- **Removal closure.** `claude rm` drops a session from the list and keeps
  its transcript; the hourly idle reap keeps the entry and its state. A
  session not listed whose transcript's last turn ran in the background
  (`sessionKind: "bg"`) and ended (`turn_duration`) is `closed: archived`
  (provenance `harness_archived`; toggle "Complete tasks when removed in
  Claude Code"), so a done task completes ([inferred completion](api.md#user-acceptance-api-v4)).
  An adopted terminal session leaving the list, `rm` mid-turn and a refused
  `rm` are not closures. A removed session stays adoptable and linkable
  (`archived` false): it can be resumed. The project is the session's
  cwd, or its repository for a Claude worktree. The reply is the last
  assistant text of the current turn, bounded to 4,000 characters, shown
  once the session is not running; best effort. `executionId` is the
  current turn's prompt.
- **Origin.** An observation's `origin` is the entrypoint of the
  transcript's first typed prompt (`cli`, `sdk-cli`, `t3-code`, ...), raw.
  The adapter reports this value for Stage's wrapping check.
  [Wrapping policy](#wrapping-and-refusal)
- **Stop** is `claude stop <short>`, for background sessions only.
  **Attention** lists every session Claude lists as waiting for its user:
  a `blocked` background session (also one the daemon retired after an hour
  idle; attach wakes it) and a terminal, IDE or SDK session with status
  `waiting`. The attention loop filters out wrapped sessions by origin.
  The `silent` policy shows waiting rows and the needs-input count. The
  row's Track action adopts the session. A finished
  discovered background session stays watched until `claude rm`; the
  adapter reuses a parsed transcript while its size and modification time
  are unchanged. **Opening** is the first typed prompt. **Links**: `command`
  is `claude attach <short>` while Claude lists the background session; none
  for a terminal, IDE or SDK session whose process runs (it is answered where
  it runs: a resume would open a second client on it); else `cd <cwd> &&
  claude --resume <uuid>`. **Links** to resolve: a UUID or the 8-character id, also inside a
  pasted `claude attach` or `--resume` command.
- **Push** ([ADR 0005](adr/0005-claude-code-harness.md) decision 16): the
  adapter is an `execution.EventSource`. The machine's hook receiver
  (`claudecode.Hooks`, opened with the first adapter, kept until shutdown)
  listens on `127.0.0.1` and writes `<state dir>/hooks-settings.json`
  (`<data dir>/claude` on the main server, `<worker dir>/claude` on a
  worker; [runtime.md](runtime.md#claude-code-hooks)): one Claude HTTP hook
  per event (`Stop`, `StopFailure`, `Notification`, `PermissionRequest`,
  `SessionEnd`, `UserPromptSubmit`) with a bearer token. Every launch passes
  the file with `--settings`. A hook is a wake-up: Stage answers 204 at
  once and reads only `session_id`, `hook_event_name` and
  `notification_type` (a `PermissionRequest` naming a tool is also held
  for an answer: Approvals and questions, below); Events then reads the session list (one read taken
  after the hook, shared) and transcript, again after 250 ms, 500 ms, 1 s
  and 2 s until the list shows what the event announces (Stop, StopFailure,
  SessionEnd: not running; PermissionRequest, a `permission_prompt`
  Notification: needs input; UserPromptSubmit: running), and pushes the
  last observation (one session at a time, four at once, the newest hook
  wins). The local runner receives it for Stage ingest.
  Polling covers adopted sessions, copies, sessions that wake after the
  listener moves, and sessions whose hooks are blocked by policy
  (`allowedHttpHookUrls`, `disableAllHooks`, `allowManagedHooksOnly`). Replacing or removing the
  adapter (connect, the command setting, disconnect) ends its
  subscription; hooks meanwhile are answered and dropped. The setup status
  reports `details.events` ([api.md](api.md#harness-connections-api-v6)).
- **Usage** from the transcript ([ADR 0005](adr/0005-claude-code-harness.md)
  decision 17): turns are the typed user prompts on the main chain (the
  current one included; `origin.kind` `human`, or no origin in `-p` and SDK
  sessions), not a background subagent's `task-notification` or a `peer`
  message, though each starts model work; tokens count every assistant
  message once (deduped by message id plus request id, the largest value of
  each channel), sidechains and `<transcript without .jsonl>/subagents/*.jsonl`
  included, `<synthetic>` messages skipped; working time sums the main chain's
  `turn_duration` records (finished turns). No cost: Claude records none for
  a background session. None before the first turn or without a transcript.
- **Inventory** is the `projects` of Claude's state file (`~/.claude.json`,
  or `.claude.json` in a `CLAUDE_CONFIG_DIR`), local to the machine; a Git
  checkout's top is importable (the machine's ops decide,
  `ImportCheckoutTop`), worktrees under `.claude/worktrees` are left out. No file is
  an empty inventory; an unreadable or malformed one is an error, so
  discovery keeps the last one. Run Claude in a folder once to list it.
- **Features.** Models: Claude's default, `opus`, `sonnet`, `haiku`,
  `fable` (aliases for the latest). Harness-wide option `effort` (`low` to
  `max`) and launch option `worktree` (boolean, default off; Claude
  starts the worktree from origin's default branch and names the branch,
  so there is no base ref or branch option). Permissions: `manual`,
  `acceptEdits`, `auto`, `plan`, `dontAsk`, `bypassPermissions`. Images
  and send are available. Unavailable with reasons: registering a folder,
  and respond without the hook receiver (no state folder, or it could not
  open).
- **Follow-ups** ([ADR 0005](adr/0005-claude-code-harness.md) decision 18,
  E12). **Send** reads the session list fresh (one send at a time) and
  takes a message only for a listed background session that is `done`, or
  `blocked` on a finished turn (no `waitingFor`, status not `waiting`, the
  transcript's turn over). Else `Busy` with what to do: working, a
  permission prompt or question, stopped, failed, removed or interactive
  (with the resume command where one helps), or a folder that is gone. A
  live process is stopped first (`claude stop <short>`), and Send waits
  until the list shows no pid and the process is gone; then
  `claude --bg --resume <uuid> -- <text>` in the session's folder, with no
  other flag, wakes the same session with its saved options (hooks
  included), and Send waits briefly for it to run. A copy (another id
  printed, or "started a copy") is stopped and removed and the send is
  `Busy`; a failed wake after the stop is an error (the session stays
  resumable). While a send delivers (at most 15 s) the session's `stopped`
  entry observes as running, so a blocked session stopped to wake records
  no interrupted. Text only, at most 32 KiB.
- **Approvals and questions** ([ADR 0005](adr/0005-claude-code-harness.md)
  decision 8, E11). The push receiver (`claudecode.Hooks`, above) also holds
  each `PermissionRequest` hook naming a tool (its hook timeout is 3600 s,
  the others 5 s) after nudging, with its tool, input and suggestions in
  memory; every other hook still gets 204 at once. With no receiver,
  respond is unavailable ("Answer in Claude Code"). A hook request stays open until the
  user answers in Stage, the session list shows the session no longer
  waiting (answered in Claude), a newer request of the session supersedes
  it, Claude closes it (timeout, process end) or the receiver closes; each
  of those answers 204 (no decision) and Claude keeps its own prompt, which the user
  can always answer with the resume command. Requests are memory only.
  **Requests** lists a waiting session's open hooks: `AskUserQuestion` as a
  question (every question `custom`, Claude's "Other"), any other tool as
  an approval titled by tool (Bash: "Run a command" with the command;
  Edit/Write: the file path; WebFetch: the URL; else the JSON input)
  with options `allow`, `always:<i>` for each suggestion Stage can describe
  (allow rules, directory access, accept edits; the label names the rule
  and where Claude writes it) and `deny` (`textOption`). **Respond**
  re-reads the session list first (not waiting: `ErrNotFound`, the hook is
  released), then answers: `{"behavior":"allow"}`, the suggestion verbatim
  as `updatedPermissions`, `{"behavior":"deny","message": "Denied in Stage: <reason>"}` (no
  reason: "Denied in Stage."), or for a question `allow` with `updatedInput` =
  the tool input plus `answers` (`{<question>: <labels joined by ", ">}`).
  A late answer on a hook Claude already settled is ignored by Claude.
- **Health** is `claude auth status --json`, reused for 30 seconds:
  unavailable when logged out or when Claude reports another config
  directory than the connection's.
- **Attribution.** Agents read `CLAUDE_CODE_SESSION_ID`. Its precedence
  owns a background session (`CLAUDE_CODE_SESSION_KIND=bg`, which may carry
  a stale wrapper variable from the daemon). Its `origin` variable is
  `CLAUDE_CODE_ENTRYPOINT`: inside a Claude process a wrapping harness runs
  (its variable set, or the entrypoint one of its `wraps` origins) the
  wrapper's variable names the caller.

Tests: parser tests on the captured fixtures (`claudecode/testdata`,
including `hooks/`), the hook receiver and requests with the fake sending
hooks as Claude does (`claudetest` `Ask`), the adapter and setup against a
fake claude CLI (`claudecode/claudetest`, a
real executable with a state file), and `TestContractClaude*` in
`internal/engine` with the real adapter in process and behind a worker
channel. `STAGE_TEST_CLAUDE=/absolute/path/to/claude mise run claude:live`
runs the adapter against the real CLI with your login and haiku: it marks
two scratch folders trusted in Claude's state file, launches a reply,
sends it a follow-up from Stage (same session, no copy, the new reply),
then removes it with `claude rm` and checks the closure, an image it must name
the color of (manual permissions, no prompt), a permission prompt it allows
from Stage through the hook, another it stops,
a worktree session (launch option `worktree`), then removes the sessions
newest first (`claude rm`; when Claude keeps a worktree for its commit,
it deletes the scratch worktrees and retries) and purges the folders
(`claude purge`). Claude
processes rewrite the state file, so a trust entry removed by hand can come
back; the purge is what removes them.

## Codex adapter

`internal/codex` drives the Codex app-server daemon over WebSocket on its
unix control socket; `internal/codexsetup` is its connection setup. Design
and live gate evidence: [ADR 0006](adr/0006-codex-app-server-harness.md).
Codex owns threads, models, login and project trust. Stage stores no Codex
credentials and never writes Codex's config. On a worker (`workersetup`) the
setup runs on that machine, with that machine's `codex`, login and daemon,
when the worker allows local harnesses; its settings live on the main
server's `harness_settings` row for that machine
([Remote machines](#remote-machines)), and the resume command runs there.

- **Client.** One connection per adapter, opened lazily. When the daemon is
  down the client runs `daemon start` (at most once per 30 s), else reports
  the daemon down. Every failure is `execution.ErrUnavailable` with the
  problem; an unknown method names the method and asks for a Codex update.
- **Inventory.** Workspaces are the projects Codex trusts
  (`config/read`), local to the machine; a directory inside a Git checkout
  is importable (the machine's ops decide, `ImportInCheckout`; trust a
  project in Codex to list it). No launch options: a session runs in the
  project checkout.
- **Launch (`reserved`).** Validation rejects before any effect (an
  unknown option such as `worktree`, missing directory, non-image attachment, invalid option or
  permission). `thread/start`, checkpoint the thread id, best-effort
  `thread/name/set`, then `turn/start` with `clientUserMessageId` set to the
  request id. Replay reads the checkpointed thread: a first turn carrying
  the client id (or the prompt) is the admission, no turn starts it, any
  other turn fails. Stage then unsubscribes; the turn keeps running.
- **Observation.** `thread/read`: `active` is running or needs_input (the
  waiting flags); the last turn's outcome is done, failed or interrupted;
  an in-progress turn on a thread the daemon unloaded, or a system error,
  has no terminal evidence and is an error. Archive closes the session
  (`archived` closure). The reply is the final answer, 4,000 characters.
- **Push.** `execution.EventSource`: the daemon broadcasts thread status,
  archive and turn completion for every thread. Stage reads each thread and
  passes the observation to the local runner. `Attention` lists loaded
  threads waiting on input.
- **Interaction.** Respond and send are unavailable: the user answers in
  Codex. Links carry `command` (`cd <dir> && codex resume <id>`), which the
  task list offers as Copy resume command.
- **Adoption.** `ResolveLink` takes a thread id or a `codex resume` command
  and refuses ephemeral and subagent threads. An observation's `origin` is
  the thread's originator, raw; the coordinator refuses a thread another
  harness wraps (T3's `T3 Code`: track it through T3).
- **Attribution.** Agents read `CODEX_THREAD_ID`. Codex declares an
  `origin` (no variable): inside a Codex process a wrapping harness runs,
  the wrapper's variable names the caller.

The fake daemon (`codex/codextest`) speaks the same subset over a temp
socket; `internal/engine/contract_codex_test.go` runs the execution
contract on it in both placements, and `codex/testdata` holds sanitized
fixtures from the live gates.
