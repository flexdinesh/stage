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
- **Worker-ready placement.** Remote machines run a thin worker with no
  database ([ADR 0002](adr/0002-machines-and-workers.md)). The main server
  keeps the store, coordinator and acceptance, and drives each machine through
  the machine-runtime seam: in process for the local machine, through a
  remote adapter proxy over the worker channel for a remote one. So adapters
  and connection setup take narrow ports (settings, credentials, checkpoint
  save, event sink, identity), never the store; contract types stay
  JSON-serializable; connections, workspace paths, machine-local fs work and
  hosting policy belong to a machine; and pushed observations are the shape
  the worker channel carries.

## Machine-runtime seam

`internal/machine` is the per-machine host of harness code; it names no
harness. A `machine.Machine` has an id, `Online`, a hosting `Profile`,
`Allows(descriptor)`, the adapters of its active connections, a connector
per kind and `Ops`. `machine.Set` holds the server's machines:
`sync.Service` resolves every connection's adapter through it
(`Adapter(id)`; a retired or unhosted connection answers
`connection_retired` or `connection_unavailable` as before, and a
connection whose kind its machine does not allow answers
`connection_unavailable` with that reason), `GET /api/harnesses` reads the
machine's profile and `Allows`, and `harness.Connectors` is built from the
machines' connectors, leaving out a connector for a kind its machine does
not allow.

- **Hosting decision.** `Machine.Allows` is the one place a machine decides
  whether it may host a harness kind, with the reason when not. The local
  machine answers with its profile's policy (`harness.Profile.Allows`); a
  remote machine allows connected kinds only, whatever its worker's
  profile, until workers set up hosted harnesses (#374). The API refuses a
  disallowed kind's setup routes with that reason (409
  `harness_unavailable`) without reaching its connector, the connectors
  index leaves it out, and a launch on a connection of that kind answers
  `connection_unavailable` with the reason. `serve` composes a hosted
  harness's setup and adapters only when the machine allows it, so a
  profile without local processes never starts the harness's processes.

- **LocalMachine.** `machine.Local` is the in-process machine (machine 1). `serve/localmachine.go` composes it: the T3 client
  (fake or MCP) and adapter, the T3 and OpenCode setups and their
  connectors. Setup code installs adapters through `Local.Install(old, next,
  adapter)` (the install port; nil disconnects); its change hook moves the
  coordinator (`sync.Service.ConnectionsChanged`) and resyncs the runners.
- **Ops.** `machine.Ops` is the harness-neutral fs and process work that
  must run where the harness runs: `Inventory` fills `WorkspaceInfo.Repo`
  and `RepoPath` through the connection's shared roots (`gitmeta.Checkout`,
  never walking above a root), and `Checkout` returns
  `machine.CheckoutEvidence` (the Git common directory as a device:inode
  key, the subproject and its slash path, and the normalized remote) for
  workspace matching. The
  main server loads shared paths and compares evidence; only evidence from
  the same machine proves a checkout. `machine.LocalOps` runs them here.
- **Setup ports.** `opencodesetup.Ports` and the `t3setup.Options` ports
  replace the store. Settings: OpenCode's `config.OpenCodeSetup`
  (non-secret) and `config.OpenCodeCredential` (secret), loaded and saved
  together in one call; T3's URL setting and environment. Credentials: the
  OpenCode credential, and the T3 token through `t3.TokenStore`. Identity:
  ensure, identity stamps, instance switches by kind (the main server
  applies its own registry's policy), retired connections, stored evidence.
  `machine.Identity` is the harness-neutral identity port (active
  connection of a kind, instance switch, connection read) that
  `machine.StorePorts` and the worker's ports both serve; a hosted
  harness's setup takes it with the install port (`machine.Install`).
  `machine.Connect` is a connect action's core: `SwitchInstance` (the first
  connect creates the machine's connection row, the same identity keeps
  it, another replaces it under the replaced-instance rules), then install
  the adapter on the new connection; its result names the replaced
  connection.
  Event sink: the install port, `OnSwitched`/`OnPaired`. Harness-specific
  machine-local work is its own port: `opencodesetup.Local` (service.json and
  its probe) and `t3setup.Local` (find and run the t3 CLI for auto-pair).
  `machine.StorePorts` backs settings, credentials and identity with the
  store for the local machine. Checkpoint save stays the `Launch` callback.
- **RemoteMachine.** A joined machine is hosted as `remote.Machine`
  ([Remote machines](#remote-machines)); `machine.Set` gains and loses
  remote machines as they join and are removed.
- **JSON.** Every type that crosses the seam round-trips through JSON
  (`machine/contract_json_test.go`): the adapter contract, connector setup
  states, port payloads and ops. `WorkspaceInfo.OriginRemote` may carry
  credentials; it is never returned to a client. Harness descriptors are
  static and identical on both sides, so a port names a harness by kind.

## Remote machines

`server/internal/remote` is a joined machine over its worker channel
([runtime.md](runtime.md#worker-channel), ADR 0002). The main server hosts a
`remote.Machine` per machine that is not removed (`remote.Hosts`: at boot,
on join, gone on remove or leave); the worker serves a `remote.Host` (an
in-process `machine.Local` whose adapters setup installs, plus
`machine.LocalOps`). The coordinator, sync, the runners and the contract
suite cannot tell a remote machine from the local one.

- **Adapters.** The machine's adapters are proxies (`remote.Adapter`) of the
  connections the main server records as active on it, so an offline
  machine's connections still answer, as offline. Every `execution.Adapter`
  call and optional interface is a channel call (`adapter.*`, the
  connection in the envelope). `Destinations` makes no call: task lists fall
  back to the stored observation's links.
- **Optional interfaces.** The proxy implements all of them in Go and
  reports which ones the worker's adapter has (`execution.Partial`, traits
  from `machine.describe`; all assumed until the first describe). Product
  code reaches them only through `execution.As[T]`, never a type assertion
  (`execution/as_boundary_test.go`).
- **Launch.** The intent goes down without image bytes. While the call
  waits, the worker calls back: `checkpoint.save` runs the coordinator's
  `SaveCheckpoint` before each external effect (a failure stops the
  worker's adapter; after the call stopped waiting the save is refused with
  `call_ended`), and `launch.image` returns one image by index. A lost reply
  leaves the attempt unconfirmed and the replay reconciles from the saved
  checkpoint, as for any uncertain launch.
- **Push.** `Events` is one long-running `adapter.events` call (10 minute
  window): each pushed observation is an `events.observation` callback,
  ingested (`sync.Service.Ingest`) before the worker continues; polling
  reconciles anything missed.
- **Health.** Offline answers at once with the reason (offline, or needs
  update); an online answer is reused for 5s.
- **Ops.** `ops.inventory` and `ops.checkout` run `machine.LocalOps` on the
  worker with the shared roots the main server loads; evidence is still
  compared on the main server, within one machine.
- **Errors.** `execution.ErrUnavailable`, `ErrNotFound`, `ErrNoReply`,
  `Rejected` and `Refused` keep their meaning (`unavailable`, `not_found`,
  `no_reply`, `rejected`, `refused`). Offline, `not_hosted`, `unsupported` and an older worker
  (`unknown_kind`) are unavailable. A lost reply, a worker timeout or any
  other error stays uncertain; only the worker adapter's own rejection is
  `Rejected`.
- **An offline worker is an offline harness.** Health is unavailable, polls
  and streams idle, task states and acceptance do not change, a backlog
  launch is refused as `connection_unavailable`, and an attempt already
  dispatched stays unconfirmed and retries until the worker is back.
- **Setup.** A remote machine's connection setup runs on its worker
  (`server/internal/workersetup`): the same `t3setup` and `opencodesetup`
  constructors as the local machine, composed once the channel is first
  online, over ports that call the main server (`setup.*` worker-to-main
  kinds, `remote.ServeSetup`, always scoped to the token's machine; a
  connection of another machine is `not_found`). The main server keeps the
  connection rows, identity, status, non-secret settings (T3 URL and Browser
  URL, OpenCode addresses and auth mode) and sync bookkeeping; harness credentials (the
  T3 token and admin token, OpenCode Basic credentials or paired token) stay in the
  worker's `credentials.json` (`0600`, deleted on join and leave) and never
  cross the channel. The main server's `remote.Connector` per machine and
  kind forwards `Status`, `Configure`, `Run` and `Disconnect` as
  `connector.*` calls; `Summary` is memory only (the main server's active
  connection plus the worker's last report from `machine.describe`, which
  carries connector summaries once `setupReady`). Offline, needs update and
  a worker still composing its setup are `remote.UnavailableError`
  (503 `machine_offline`, `machine_needs_update`,
  `machine_setup_starting`). Remote machines are connected-only for now:
  workers set up connected kinds only (`remoteSetupHooks`), and
  `remote.Machine.Allows` reports a hosted kind unavailable ("can't run on
  a worker machine yet"); #374 lifts both. A remote machine's connection row is created
  on first connect (T3 pairing, OpenCode connect or pair), and a switch on
  the worker follows the connections on the main server (runners, sync).
  No environment variable sets a connection.
- **Verification.** Every `TestContract*` in `server/internal/sync` runs
  twice, in process and behind a real worker channel
  (`remote/remotetest`: the hub and its HTTP serving on a loopback server
  and a real `worker.Run`), plus remote-only tests: a lost launch reply, a
  worker restart mid-launch, an offline machine, refused and late saves,
  images, traits and ops. Setup runs end to end over a real channel and
  store in `workersetup` (T3 pair creating the machine's connection,
  OpenCode credentials kept on the worker, calls scoped
  to the caller's machine).

## Harness registry

Each harness package declares a `harness.Descriptor`: kind, name, hosting,
whether its instance identity is stable, and the closure signals it reports
with their copy. `serve` composes a fixed `harness.Registry` (T3, OpenCode, Claude Code)
and runs one release and one schedule runner per active connection, on
every machine (`serve/runners.go` follows instance switches). The database
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
- **Connection setup.** A harness's descriptor, connected or hosted,
  carries a setup schema (settings fields with their locking environment
  variable, typed actions, disconnect). Its setup package implements `harness.Connector`
  (memory-only summary, status with harness details, configure, run,
  disconnect) over narrow ports ([machine-runtime seam](#machine-runtime-seam));
  each machine hosts one per kind (`harness.Connectors`, built from the
  machines and checked against the registry, leaving out kinds a machine
  does not allow). A hosted harness has no connection row until its
  connect action creates one through the identity port (`machine.Connect`).
  The API exposes them generically under `/api/harnesses/{kind}/...`; health, sync skipping and
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
  mode choice in the harness's words. Tasks and schedules store only an
  execution config: legacy T3 model inputs are translated on write
  (`store.LegacyT3Config`, equal to `t3.LegacyConfig`) and migration 0027
  backfilled T3 rows saved before configs.
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
- `Refused` (`execution.Refused{reason}`) is a harness declining to track a
  session another client owns (Claude Code refuses a session T3 started).
  It is a decision, not a failure: the coordinator answers 409
  `session_refused` with the reason, and nothing about the session changes.
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
When the harness returned a session but Stage failed to record it (its
observation or the confirmation write), the attempt keeps the cause, the backoff
and that session; task detail leads with "Launched in <harness>; Stage can't
record it" and opens the session, and the next retry records it once the cause is
gone. A revision check that no longer matches reports a conflict, never a silent
no-op.

Session identities are always scoped by the observed session's own connection:
a mirrored thread's parent and a task event's journal row take the session of
that thread or task, never the local machine's T3 (which may be retired once T3
moved to a worker).
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

Existing T3 request fields and routes remain. `executionConfig` is the
task/schedule field; legacy model/permission fields are translated into it on
write. Do not combine the two in a create or update request. Existing responses and historical settlement/run/PR records
remain readable. Newly observed execution uses Stage states; T3 settlement
never hides active work and accepts a task only through inferred completion.
The T3 thread mirror's `settled` follows the observation. T3 reports a thread's
newest run; a cancelled or rolled-back run that never started (a queued
follow-up) did no work, so the newest other run decides the observed state and
end time.

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

## Claude Code adapter

`internal/claudecode` drives the `claude` CLI; Claude's per-user daemon owns
the sessions, worktrees, models and login ([ADR 0005](adr/0005-claude-code-harness.md)).
Claude Code has no HTTP API. Kind `claude`, hosting `hosted`, stable identity
(the `configDirectory` from `claude auth status --json`), launch
`correlated`, no closures. Setup (`internal/claudecodesetup`): the command
setting, a connect action and disconnect ([api.md](api.md#harness-connections-api-v6)),
stored per machine in `claude_settings` with the config directory the
connection was made with; `serve` composes it only where the machine allows
hosted harnesses.

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
- **Launch.** In the workspace folder: `claude --bg -n <title> [--model M]
  [--effort E] [--permission-mode P] [-w stage-<requestId>] -- <prompt>`
  (the prompt carries the launch marker; `--` keeps a prompt starting with
  `-` from reading as a flag). Images, a base or branch, a workspace other
  than the project folder or a Claude worktree, a config the features do not
  offer, a missing folder or executable, and Claude's `Workspace not
  trusted` refusal are `Rejected` (the task returns to backlog with the
  reason; trusting a folder is done in Claude). Exit 0 prints
  `backgrounded · <short>`; the session list maps the short id to the
  session within 5 seconds. Anything else is uncertain.
- **Correlate.** Background sessions listed in the launch folder (or its
  per-attempt worktree, `<folder>/.claude/worktrees/stage-<requestId>`) and
  the transcripts of those folders, matched by the marker in the first
  prompt. Several matches are uncertain; none is not found.
- **Observe.** One `claude agents --json --all` read per tick, shared by
  every session of the connection (reused for 2 seconds), plus the
  transcript (`<projectsDirectory>/<cwd with / and . as ->/<uuid>.jsonl`,
  else any project folder). Background sessions map by `state`: `working`
  running, `blocked` needs_input, `done` done, `failed` failed, `stopped`
  interrupted; an adopted terminal or SDK session by `status`. Not listed:
  idle with its reply when the transcript exists, else not found. An
  unreadable list is an error, never a state. The project is the session's
  cwd, or its repository for a Claude worktree. The reply is the last
  assistant text of the current turn, bounded to 4,000 characters, shown
  once the session is not running; best effort. `executionId` is the
  current turn's prompt.
- **Refusal.** A session whose transcript starts with entrypoint `t3-code`
  is T3's: Observe, Stop and link resolution refuse it ("Track it through
  T3").
- **Stop** is `claude stop <short>`, for background sessions only.
  **Attention** is empty: Stage watches the sessions it launched or adopted
  from its own records and does not discover others. **Opening** is the
  first typed prompt. **Links**: `command` is `claude attach <short>` while
  Claude lists the background session, else `cd <cwd> && claude --resume
  <uuid>`. **Links** to resolve: a UUID or the 8-character id, also inside a
  pasted `claude attach` or `--resume` command.
- **Inventory** is the `projects` of Claude's state file (`~/.claude.json`,
  or `.claude.json` in a `CLAUDE_CONFIG_DIR`); a Git checkout's top is
  importable, worktrees under `.claude/worktrees` are left out. No file is
  an empty inventory; an unreadable or malformed one is an error, so
  discovery keeps the last one. Registering a folder is unavailable: run
  Claude there once.
- **Features.** Models: Claude's default, `opus`, `sonnet`, `haiku`,
  `fable` (aliases for the latest). Harness-wide option `effort` (`low` to
  `max`). Permissions: `manual`, `acceptEdits`, `auto`, `plan`, `dontAsk`,
  `bypassPermissions`. Workspaces: the project folder or a Claude worktree.
  Unavailable with reasons: images, predecessor and worktree bases (Claude
  branches from the checkout's HEAD), registering a folder, and respond and
  send (answer in Claude: attach or resume).
- **Health** is `claude auth status --json`, reused for 30 seconds:
  unavailable when logged out or when Claude reports another config
  directory than the connection's.
- **Attribution.** Agents read `CLAUDE_CODE_SESSION_ID`. Its precedence
  owns a background session (`CLAUDE_CODE_SESSION_KIND=bg`, which may carry
  a stale `T3CODE_THREAD_ID` from the daemon) and yields to T3 inside a
  Claude process T3 runs (`T3CODE_THREAD_ID`, or `CLAUDE_CODE_ENTRYPOINT=t3-code`).

Tests: parser tests on the captured fixtures (`claudecode/testdata`), the
adapter and setup against a fake claude CLI (`claudecode/claudetest`, a
real executable with a state file), and `TestContractClaude*` in
`internal/sync` with the real adapter in process and behind a worker
channel. `STAGE_TEST_CLAUDE=/absolute/path/to/claude mise run claude:live`
runs the adapter against the real CLI with your login and haiku: it marks
two scratch folders trusted in Claude's state file, launches a reply, a
permission prompt it stops and a worktree session, then removes the
sessions (`claude rm`) and purges the folders (`claude purge`). Claude
processes rewrite the state file, so a trust entry removed by hand can come
back; the purge is what removes them.
