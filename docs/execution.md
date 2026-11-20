# Stage execution boundary

Stage owns task identity, dependencies, acceptance, journals and durable handoff.
`internal/execution` describes the operations Stage retains. `internal/sync`
coordinates those operations; `internal/t3.Adapter` translates T3 protocol,
authentication, models, permission modes, workspace strategies and states.
Composition is a fixed connection-to-adapter map in the server. There is no
plugin registration or service installation framework.

## Design rules

- **Lean.** The harness keeps what it owns: model catalog, workspaces and
  worktrees, archive, its UI. Stage owns something only when the harness
  cannot, on a best-effort basis.
- **Generic code, explicit policies.** Product code reads the harness
  descriptor, capabilities and operations, never a harness name. Behavior one
  harness needs sits behind a typed optional interface or a registered UI
  slot, so a harness without a feature never limits the app for the others.
  `internal/harness/boundary_test.go` fails on harness names outside the
  adapters, their connection setup and the server composition (its pending
  list only shrinks).
- **Worker-ready placement.** A future deployment runs Stage in the cloud and
  a worker on each machine that syncs its harnesses upstream. So adapters and
  connection setup take narrow ports (settings, checkpoint save, event sink),
  never the store; contract types stay JSON-serializable; connections,
  workspace paths and hosting policy belong to a machine; and pushed
  observations are the shape a worker's upstream channel will take.

## Harness registry

Each harness package declares a `harness.Descriptor`: kind, name, hosting,
whether its instance identity is stable, and the closure signals it reports
with their copy. `serve` composes a fixed `harness.Registry` (T3, OpenCode)
and builds one release and schedule runner per kind from it. The database
stores kinds as text; the registry, not a schema CHECK, decides which exist.

- **Hosting.** `connected`: Stage connects to a service (T3, OpenCode), on any
  machine profile. `hosted`: the harness runs as processes on Stage's machine
  and is available only where the machine profile allows local processes
  ([runtime.md](runtime.md#machine-profile-and-hosted-harnesses)); elsewhere
  `GET /api/harnesses` reports it unavailable with the reason.
- **Stable identity.** When the identity names one instance for good (a T3
  environment id), pairing a retired connection's identity revives it, and
  identities are unique per machine and kind. Otherwise (an OpenCode address)
  the connector proves continuity itself and passes the connection to revive.
- **Connection setup.** A connected harness's descriptor carries a setup
  schema (settings fields with their locking environment variable, typed
  actions, disconnect). Its setup package implements `harness.Connector`
  (memory-only summary, status with harness details, configure, run,
  disconnect) over narrow ports; `serve` composes one per kind
  (`harness.Connectors`, checked against the registry). The API exposes them
  generically under `/api/harnesses/{kind}/...`; health, sync skipping and
  connection lists read connector summaries. Hosts render the schema, or a
  panel registered for the kind (UI `CONNECTION_PANELS`, CLI `slots_*.go`).
- **Closures.** Completion is inferred only from a closure the harness's
  descriptor declares; the descriptor supplies the stored provenance, the
  journal event, the auto-complete toggle label and the accepted-result copy.
  A harness that declares none has no auto-complete.

## Contracts for roadmap tasks 5 and 6

- A session is `{connectionId, externalSessionId}`. Neither a directory string
  nor an external session ID identifies another connection. Project bindings
  and launch destinations retain the verified connection, external project,
  directory, binding ID and revision.
- `Config.model` is an opaque adapter key. Options are named string/boolean
  values; permission and interaction mode are opaque adapter selections.
  `Features` supplies models, options, permissions, modes, workspaces and
  operation availability with reasons. Unsupported operations return actionable
  errors. Adapters must validate the requested semantics before dispatch.
  Product code reads policy from these, never a harness name: `images`,
  `predecessorBase` and `worktreeBase` (unavailable when the harness picks the
  worktree base and branch itself) gate form fields, and `ModeLabel` names the
  mode choice in the harness's words. A harness whose tasks predate execution
  configs supplies `Descriptor.LegacyConfig`, the only reader of those columns.
- `Observation` is a validated snapshot: session/project, normalized state,
  activity, top-level/archive eligibility, workspace, links, bounded reply and
  available artifact links. States are running, needs_input, done, failed,
  interrupted, cancelled or idle. Idle does not overwrite a known task state.
  Unknown/malformed snapshots and missing/offline sessions never imply execution
  failure or completion. Connection health is independent. `closed`
  (`{signal}`, omitted when open) reports a closure in the harness: `settled`
  (T3) or `archived` (OpenCode). It says nothing about success.
- `Launch` receives an immutable intent plus versioned adapter JSON checkpoints.
  It must call the durable save callback before each external effect, stop if
  saving fails, and reconcile the same operation on replay. Checkpoints contain
  references and non-secret identity only. An adapter with multi-step creation
  must reserve deterministic session/message identifiers before sending either
  step and reconcile their existence after interruption.
- `Rejected` certifies that no execution started. It returns the task to backlog
  for repair. Every other launch error keeps the attempt unconfirmed. A harness
  must never use rejection merely because it is offline, timed out or returned
  malformed data.
- Optional link resolution supports explicit repair. Preview verifies the pinned
  destination, current environment, top-level session and ownership. Attachment
  rechecks those facts and the attempt revision. It sends no launch and grants
  no Stage acceptance.
- Schedules, manual starts and automatic tree starts share the coordinator.
  Completion snapshots and handoff read Stage storage. A source harness can be
  offline. Only acceptance releases dependencies: user completion, or
  completion Stage infers from a closure of a done session after polling it
  (once per execution, per-connection `autoComplete`; see
  [inferred completion](api.md#user-acceptance-api-v4)). Unaccepted done tasks
  stay in the throttled stale poll so a late closure is seen.
- Hosts choose native versus web destinations through
  `Links.executionDestination`. Product code does not inspect browser location
  or dispatch native URLs directly.

Agent transports use the same session ref for per-call metadata, ownership, adoption and journal provenance. The caller and execution destination are independent. A harness names the caller through its descriptor's attribution (OpenCode attaches its native session ID to each MCP call) or the generic `Stage-Caller-Session` header and `STAGE_CALLER_SESSION` variable; the launch instructions carry the descriptor's caller hint. Stage records the connection-scoped origin on child tasks and journal entries. See [agent interface](api.md#agent-interface) for inputs and compatibility.

## Execution contract v2

Optional parts of the adapter contract (`internal/execution/optional.go`).
Product code type-asserts the interface and reads the operation's
availability; the descriptor states the policies the coordinator enforces.
The contract tests prove them on fake harnesses (`internal/sync`
`contract_v2_test.go`): `assigned` (server-assigned ids, non-idempotent
create) and `hosted` (owns its processes, pushes, loses its owner, answers
interaction).

- **Launch identity.** The descriptor's `launch` is `reserved` (default): the
  adapter reserves the session identity before any effect, so replaying
  `Launch` reconciles the same session (T3's deterministic thread id,
  OpenCode's checkpointed session). `correlated`: the harness assigns the id
  and creation is not idempotent. Stage appends `execution.LaunchMarker(requestId)`
  to the prompt (`Intent.marker`), records the dispatch before calling
  `Launch` exactly once, and after an uncertain outcome only searches for the
  marker (`execution.Correlator`). Not found keeps the attempt **Launch
  unconfirmed** until the user links the session; Stage never launches it
  again. A correlated harness without a correlator cannot launch.
- **Lost outcome.** `Observation.stateReason` qualifies the state. Only a
  harness that owns its processes (`hosted`) may report `interrupted` with
  `owner_exited`: its process died without an outcome. The coordinator rejects
  the reason from any other harness as malformed (nothing changes).
- **Push.** An adapter implementing `execution.EventSource` streams
  observations of its connection; the coordinator runs the stream per
  connection and ingests each through the same path as polling (validation,
  policies, transition, inferred completion). Unowned sessions are left to
  discovery, launching tasks to their launch. Polling continues as
  reconciliation, so a lost event only delays; the stream is the shape a
  worker's upstream channel will take.
- **Resume command.** `Links.command` is a copyable shell command that
  resumes the session in the harness's own client, for a harness without a web
  or app destination. The platform's `ExecutionLinks.command` carries it; hosts
  never open it, and the task page offers Copy resume command when no
  destination exists.
- **Harness-wide options.** `Capabilities.options` apply whatever the model,
  beside each model's own. A config may set the union
  (`Capabilities.OptionsFor`); an id reused between them is malformed
  (`Capabilities.Valid`), so a selection always names one option.
- **Interaction.** `execution.Interactive` (requests, respond, send) behind
  operations `respond` and `send`. API: [session interaction](api.md#session-interaction-api-v6);
  no UI or MCP yet. A harness without it answers `operation_unavailable`.

## Durable launch lifecycle

The launch claim transaction pins acceptance inputs and destination and creates a
prepared attempt. Prompt, journal instructions, configuration, images and chosen
base are persisted before the adapter runs. A crash during preparation has made
no harness call and returns to backlog. Historical attempts without safe identity
remain unconfirmed and require linking.

After dispatch, recovery retries automatically with backoff (5 seconds to one
minute), including after restart. UI labels the state **Launch unconfirmed**.
A per-task lock serializes reconciliation and manual attachment; database
revision checks reject stale checkpoints and attachments. The server data-dir
lock prevents concurrent owners of the same database.

T3 checkpoints pin the OAuth authentication session namespace, environment,
expected deterministic thread ID and upload references. Registration client ID
alone is insufficient. Re-pairing cannot replay an old namespace. A known thread
may still be read in the same verified environment after re-pairing. A conflicting
launch reply is held for explicit linking. Uploaded images stay with uncertain
attempts; only certified rejection permits discarding them.

Predecessor bases require an available branch in the same harness/project.
Unavailable, ambiguous, merged or deleted predecessors block launch. Choosing
the configured base is an explicit edit. Historical fallback records remain.

## Compatibility and verification

Existing T3 request fields and routes remain. `executionConfig` is an additive
task/schedule field; do not combine it with legacy model/permission fields in a
create or update request. Existing responses and historical settlement/run/PR records
remain readable. Newly observed execution uses Stage states; T3 settlement
never hides active work and accepts a task only through inferred completion.
The T3 thread mirror's `settled` follows the observation.

Contract tests use a second fake harness with document IDs, a non-T3 model,
two-step creation, optional capabilities, lost replies, restart, offline reads,
malformed states and offline acceptance. T3 tests cover namespace rotation and
checkpoint failures. API tests cover compatibility, frozen handoff/journals,
explicit base repair and link-preview-attach ownership/revision checks.
These contract tests use isolated harnesses. The OpenCode live suite is described below.

## OpenCode V2 adapter

Stage connects to an existing
service using its URL and optional HTTP Basic credentials. It accepts OpenCode V2
(tested with 2.3.0); any version whose API has the operations Stage uses. Stage does
not pin the reported version: it checks the required V2 operations and worktree, session
and prompt request schemas in `/openapi.json`, and refuses to connect when one is missing
(the error names the version and the operation). Version-specific handling, when a
release needs it, branches on the reported version. The server does not advertise a
binary digest. Repeat the live suite when a release changes behavior Stage relies on.

OpenCode owns project inventory, workspace strategy, model selection and agent policy.
An empty selection uses its defaults. A bound directory provides the model catalog,
variants and visible primary agents. The capabilities endpoint accepts an optional
Stage `projectId` to resolve that binding. A worktree launch asks OpenCode for its
selected strategy and default base. Stage verifies the resulting inventory entry,
project and directory. Explicit Git refs, branch creation, predecessor bases,
permission overrides and image delivery currently report an actionable reason.

### Launch and recovery

Stage saves the frozen intent, reserved IDs and an attempt marker before dispatch.
The three external effects are worktree creation, session creation and prompt admission.
The adapter performs only the steps needed for the chosen workspace.

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
text from that execution, bounded to 4,000 characters. Available HTTP(S) artifact
links come from the optional session-scoped artifact inventory. A pasted ID or a
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
