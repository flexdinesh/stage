# Stage execution boundary

Stage owns task identity, dependencies, acceptance, journals and durable handoff.
`internal/execution` describes the operations Stage retains. `internal/sync`
coordinates those operations; `internal/t3.Adapter` translates T3 protocol,
authentication, models, permission modes, launch options and states.
Composition is a fixed connection-to-adapter map in the server. There is no
plugin registration or service installation framework.

## Design rules

The boundary is [ADR 0007](adr/0007-harness-boundary.md); these rules
apply it.

- **Lean.** The harness keeps what it owns: sessions, model catalog,
  workspaces, worktrees and branches, archive, harness projects, its UI and
  its processes. Core code does no harness work (no git for sessions, no
  worktree naming, no creating harness projects). Stage owns tasks and their
  launch and observation, plus best-effort project identity across harnesses
  (ADR 0007 decision 11).
- **Generic code, explicit policies.** Product code reads the harness
  descriptor, capabilities, operations and launch options, never a harness
  name. Behavior one harness needs sits behind a typed optional interface,
  a declared operation or launch option, or a registered UI slot, so a
  harness without a feature never limits the app for the others. An adapter
  may translate its own launch options into harness calls; core code
  computes nothing from them. `internal/harness/boundary_test.go` fails on
  harness names outside the adapters, their connection setup and the server
  composition (its pending list only shrinks, to empty).
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
  remote machine with the profile its worker last described (the same
  policy, with the fix in the worker's terms: `stage worker run
  --local-harnesses` or the worker service, or the worker outside Docker).
  Once the worker's setup is ready, a hosted kind it did not set up is not
  allowed either: workers set up Claude Code and Codex where their profile
  allows local harnesses, so on an older worker without one of them it
  reads unavailable ("Update Stage on that machine"), never as a connect
  that fails. Until its worker described it since this server started, a remote
  machine's profile is unknown and every kind is allowed, so the calls
  themselves answer offline or still starting. The API refuses a
  disallowed kind's setup routes with that reason (409
  `harness_unavailable`) without reaching its connector, the connectors
  index leaves it out, and the coordinator answers
  `connection_unavailable` with the reason for a connection of that kind,
  before any installed adapter (a worker restarted without local harnesses
  names the fix). `serve` and `workersetup` compose a hosted harness's
  setup and adapters only when their machine allows it, so a profile
  without local processes never starts the harness's processes.

- **LocalMachine.** `machine.Local` is the in-process machine (machine 1). `serve/localmachine.go` composes it: the T3 client
  (fake or MCP) and adapter, the T3 and OpenCode setups and their
  connectors, and the Codex setup (`codexsetup`) when the profile allows it. Setup code installs adapters through `Local.Install(old, next,
  adapter)` (the install port; nil disconnects); its change hook moves the
  coordinator (`sync.Service.ConnectionsChanged`) and resyncs the runners.
- **Ops.** `machine.Ops` is the harness-neutral fs and process work that
  must run where the harness runs, and the only place Stage reads Git
  evidence ([ADR 0007](adr/0007-harness-boundary.md) decision 11; adapters
  report harness facts only, and a test keeps `gitmeta`'s readers inside
  `machine`). Both read a workspace through `machine.RootsFor`: the
  connection's shared roots, then, for a workspace the adapter marks
  `Local` (a hosted harness's folders, a local OpenCode service's
  projects), the path itself. `Inventory` fills `WorkspaceInfo.Repo` and
  `RepoPath` without a git binary (`gitmeta.Checkout`, never walking above
  a root; a Repo the harness reported stays) and decides `Importable` for
  a harness that imports Git checkouts (`ImportIf`: `checkoutTop` for
  Claude Code, `inCheckout` for Codex). `Checkout` returns
  `machine.CheckoutEvidence` (the Git common directory as a device:inode
  key, the subproject and its slash path, and the normalized remote) for
  the match action. The main server loads shared paths and walks the
  project identity ladder (`projectmatch`); only evidence from the same
  machine proves a checkout. `machine.LocalOps` runs them here.
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
  its probe), `t3setup.Local` (find and run the t3 CLI for auto-pair) and
  `codexsetup.Options.Daemon` (run `codex app-server daemon version|start`).
  `codexsetup.Settings` loads and saves `config.CodexSettings` (CLI path,
  connected).
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
  from `machine.describe`; all assumed until the worker describes that connection). Product
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
  worker with the shared roots the main server loads (a `Local` workspace,
  such as a worker's Claude Code folder, is read in place there); evidence
  is still compared on the main server, within one machine.
- **Errors.** `execution.ErrUnavailable`, `ErrNotFound`, `ErrNoReply`,
  `Rejected` and `Refused` keep their meaning (`unavailable`, `not_found`,
  `no_reply`, `rejected`, `refused`). Offline, `not_hosted`, `unsupported` and an older worker
  (`unknown_kind`) are unavailable. A lost reply, a worker timeout or any
  other error stays uncertain; only the worker adapter's own rejection is
  `Rejected`. The removed `adapter.registerWorkspace` (an older main
  server's call) answers `unknown_kind`. A session's `origin` crosses the
  channel raw; the main server's registry decides a wrapped refusal.
- **An offline worker is an offline harness.** Health is unavailable, polls
  and streams idle, task states and acceptance do not change, a backlog
  launch is refused as `connection_unavailable`, and an attempt already
  dispatched stays unconfirmed and retries until the worker is back.
- **Setup.** A remote machine's connection setup runs on its worker
  (`server/internal/workersetup`): the same `t3setup`, `opencodesetup`,
  `claudecodesetup` and `codexsetup` constructors as the local machine
  (Claude Code and Codex only when the worker's profile allows local
  harnesses), composed once
  the channel is first online, over ports that call the main server
  (`setup.*` worker-to-main kinds, `remote.ServeSetup`, always scoped to
  the token's machine; a connection of another machine is `not_found`).
  The main server keeps the connection rows, identity, status, non-secret
  settings (T3 URL and Browser URL, OpenCode addresses and auth mode,
  Claude Code's command, connected config directory and disconnect:
  `setup.claude.get` and `setup.claude.save`; Codex's CLI path and
  connected: `setup.codex.get` and `setup.codex.save`, the machine's
  `codex_settings` row) and sync bookkeeping; Claude Code and Codex have
  no credentials (Claude's own login, `codex login`, on that machine). A
  worker whose main server is older (it answers `setup.claude.*` or
  `setup.codex.*` with `unknown_kind`) composes the other harnesses
  without that one and logs why; a worker older than its main server
  answers that kind's connector calls `not_hosted`, which the main server
  reports as 503 `machine_needs_update`. Stage never stops a worker's
  Codex daemon either (ADR 0007 decision 14). Harness credentials (the
  T3 token and admin token, OpenCode Basic credentials or paired token) stay in the
  worker's `credentials.json` (`0600`, deleted on join and leave) and never
  cross the channel. The main server's `remote.Connector` per machine and
  kind forwards `Status`, `Configure`, `Run` and `Disconnect` as
  `connector.*` calls; `Summary` is memory only (the main server's active
  connection plus the worker's last report from `machine.describe`, which
  carries connector summaries once `setupReady`). Offline, needs update and
  a worker still composing its setup are `remote.UnavailableError`
  (503 `machine_offline`, `machine_needs_update`,
  `machine_setup_starting`). Every registered kind has a connector proxy
  on every remote machine (`remoteSetupHooks`); `remote.Machine.Allows`
  decides which ones the machine offers. A remote machine's connection row is created
  on first connect (T3 pairing, OpenCode connect or pair, Claude Code or
  Codex connect), and a switch on
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
  to the caller's machine, Claude Code against the fake `claude` CLI and
  Codex against the fake daemon (`codextest`): connect, a task through the
  coordinator whose stored observation carries the resume command,
  disconnect; refused without local harnesses; left out against an older
  main server).
- **Resume command.** `Destinations` makes no call, so a remote session's
  `links.command` comes from its stored observation (every observation
  carries its links). A launch whose session Stage saw but could not
  record keeps that session's resume command on the attempt
  (`execution_attempts.observed_session`, command only; destinations are
  still resolved when read), and the API falls back to it. On task rows and task detail, with more than
  one machine, the UI names the machine the command must run on ("Copy
  resume command for laptop").

## Harness registry

Each harness package declares a `harness.Descriptor`: kind, name, hosting,
whether its instance identity is stable, the closure signals it reports
with their copy, and an optional session hint (how a user names one of its
sessions in Track session). `serve` composes a fixed `harness.Registry` (T3, OpenCode, Claude Code, Codex)
and runs one release and one schedule runner per active connection, on
every machine (`serve/runners.go` follows instance switches). The database
stores kinds as text; the registry, not a schema CHECK, decides which exist.

- **Hosting.** `connected`: Stage connects to a service (T3, OpenCode), on any
  machine profile. `hosted` (Claude Code, Codex): the harness runs as processes on its machine
  (the server's or a worker's) and is available only where that machine's profile allows local processes
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
  panel registered for the kind (UI `packages/app/src/harnesses/` slot registry, CLI `slots_*.go`).
- **Closures.** Completion is inferred only from a closure the harness's
  descriptor declares; the descriptor supplies the stored provenance, the
  journal event, the auto-complete toggle label and the accepted-result copy.
  A harness that declares none has no auto-complete.
- **Attention.** What a session from the adapter's attention feed that no
  task owns may produce. `notify` (the default; T3, OpenCode): a silent
  waiting row, plus an inbox event and push for transitions into
  `needs_input|done|failed` after startup. `silent` (Claude Code): the
  waiting row and the needs-input count only, never an event or push.
  Either way discovery never tracks a session: adoption does.
- **Wrapping** ([ADR 0007](adr/0007-harness-boundary.md) decision 13). A
  harness that runs other harnesses' sessions declares `wraps: {origins}`:
  the session origins it leaves in them, as the wrapped harness records
  them (T3: `t3-code`, Claude Code's entrypoint; `T3 Code`, Codex's
  originator). An origin belongs to one harness (the registry rejects a
  second). Adapters report a session's `origin` raw (`Observation.origin`)
  and never name another harness (`harness` `boundary_test.go`
  `TestAdaptersDoNotNameOtherHarnesses`). The coordinator refuses to track
  a session whose origin another registered harness wraps (adoption, track
  preview, link repair, polling: `session_refused`, "Track it through
  <wrapper>"), and leaves it out of discovery's waiting rows. A harness
  that records origins declares `attribution.origin` (`{env?}`: the
  variable holding a session's origin in its agents; Claude Code
  `CLAUDE_CODE_ENTRYPOINT`, Codex none); among several harness session
  variables in one process, such a harness's yields when a wrapper's
  variable is set or its origin variable holds a wrapped origin (unless
  its precedence owns), so the wrapper's variable names the caller.

## Contracts for roadmap tasks 5 and 6

- A session is `{connectionId, externalSessionId}`. Neither a directory string
  nor an external session ID identifies another connection. Project bindings
  and launch destinations retain the verified connection, external project,
  directory, binding ID and revision.
- `Config.model` is an opaque adapter key. Options are named string/boolean
  values; permission and interaction mode are opaque adapter selections.
  `Features` supplies models, options (launch options included),
  permissions, modes and operation availability with reasons. Operations
  are typed (`execution.Op`, UI `OperationId`): `launch`, `stop`, `adopt`,
  `reply`, `opening`, `attention`, `images`, `respond`, `send`. Every adapter declares each one, with a reason when unavailable
  (`Capabilities.DeclaresOperations`, its contract tests); the remote proxy
  passes them through. Unsupported operations return actionable
  errors. Adapters must validate the requested semantics before dispatch.
  Product code reads policy from these, never a harness name: `images`
  gates the image field, the API and MCP (images on a connection without
  it are refused with its reason), and `ModeLabel` names the mode choice
  in the harness's words. Tasks and schedules store only an execution
  config (migration 0027 backfilled T3 rows saved before configs); API v8
  removed the legacy T3 model inputs (400 naming `executionConfig` with
  `connectionId`) and `workspace` (400 naming `executionConfig.options`).
- **Launch options** (#420). Where and how a session starts (a new
  worktree, its base ref, its branch) is a harness-wide option the adapter
  declares and translates; Stage keeps no workspace of its own and makes
  no worktree. An `Option` has an `id`, a `kind` (`choice`, `boolean` or
  `text`; omitted in older declarations: `boolean` when every value is a
  boolean, else `choice`), a `label`, its `values` (none for `text`), a
  `default` and an optional `dependsOn` (`{option, value}`: it applies only
  when that option, set or by default, has that value). `Capabilities.Valid`
  rejects malformed declarations; `ValidateConfig` checks a selection:
  a boolean takes `true` or `false`, a text a non-empty trimmed string
  (leave it out for the harness default), a choice one of its values, and
  a dependent option only when its dependency holds. Per harness: T3
  `worktree` (boolean), `baseRef` and `branch` (text, with `worktree`
  `true`), translated to `workspaceStrategy` (`root`, or `worktree` with
  `baseRef`, `branch` and `startFromOrigin` when the ref starts with
  `origin/` or `upstream/`; T3 requires a base ref, so a worktree without
  one is `Rejected` with the reason); Claude Code `worktree`
  (`claude --bg -w stage-<requestId>`); OpenCode `worktree` (its
  `POST /api/worktree` flow); Codex none. A dependent no longer starts
  from its predecessor's branch: the handoff names the branch the
  predecessor reported (migration 0033 moved stored workspaces into
  options, a T3 predecessor's stored fallback base ref and branch
  included, and journaled where each predecessor-based task now starts).
- `Observation` is a validated snapshot: session/project, normalized state,
  activity, top-level/archive eligibility, directory and branch, links, bounded reply and
  available artifact links. States are running, needs_input, done, failed,
  interrupted, cancelled or idle. Idle does not overwrite a known task state.
  Unknown/malformed snapshots and missing/offline sessions never imply execution
  failure or completion. Connection health is independent. `closed`
  (`{signal}`, omitted when open) reports a closure in the harness: `settled`
  (T3) or `archived` (OpenCode archive, Claude Code `rm`). It says nothing
  about success. An idle observation may carry a closure (the harness closed
  the session and dropped its state with it): the task keeps its state and
  inference reads the stored `done`.
- `usage` (`execution.Usage`, omitted when the harness reports none) is what
  the session consumed over its whole life: `turns`, `inputTokens`
  (uncached), `cacheReadTokens`, `cacheWriteTokens`, `outputTokens`,
  `workingMs` (finished turns; 0 unknown) and `costUsd`. Cumulative, best
  effort, harness-neutral. `costUsd` is only a cost the harness itself
  reports (null otherwise); Stage never prices tokens. The store keeps the
  last usage when an observation carries none (an unreadable transcript).
  Task detail shows the current session's usage.
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
- `Refused` (`execution.Refused{reason}`) is Stage declining to track a
  session another registered harness wraps (its `origin` among that
  harness's `wraps`: a Claude Code session or Codex thread T3 started). The
  coordinator decides it from the descriptors, never an adapter by name. It
  is a decision, not a failure: the coordinator answers 409
  `session_refused` with the reason ("This Claude Code session was started
  by T3. Track it through T3."), and nothing about the session changes.
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
  worker's upstream channel will take. An external bridge or hook (no
  in-process `EventSource`) may push observations of an existing connection
  to `POST /api/connections/{id}/observations`
  ([api.md](api.md#harness-connections-api-v6), ADR 0007 decision 8): the
  same `sync.Service.Ingest`, after `CheckObservation` with the
  connection's kind, so a malformed one is reported `invalid` and changes
  nothing; each result says `ingested`, `ignored` or `invalid`. A push for
  a connection on an offline worker (or one that needs an update) is 409
  `connection_unavailable` and changes nothing. Polling still reconciles.
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
  operations `respond` and `send`. A `Request` is an `approval` (pick an
  option; `textOption` names the option that may carry text, e.g. a denial's
  reason), a `question` (its `questions` answered together through
  `Response.answers`, keyed by question id: option labels, or one custom
  text when `custom`) or `input` (free text); `preview` is the command,
  path, URL or input it acts on. An answer that does not fit is
  `*execution.Invalid` (400; the worker channel carries it as
  `invalid_answer`); a request no longer pending is `ErrNotFound` (404).
  API: [session interaction](api.md#session-interaction-api-v6); the app
  answers on task detail and needs-input rows, never MCP or the CLI (only
  the user answers). A harness without it answers `operation_unavailable`.
  A session that can't take a message now is `*execution.Busy` (409
  `session_busy` with the reason; the worker channel carries it as `busy`);
  a sent message records "Follow-up sent from Stage" on the task's timeline.
  Send, like respond, is the user's only (agent callers are refused).
  Claude Code implements it (respond and send); T3, OpenCode and Codex do not.

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
an observation's parent and a task event's journal row take the session of
that observation or task, never the local machine's T3 (which may be retired
once T3 moved to a worker). A bare session id names no connection, so it
records no session.
A per-task lock serializes reconciliation and manual attachment; database
revision checks reject stale checkpoints and attachments. The server data-dir
lock prevents concurrent owners of the same database.

T3 checkpoints pin the OAuth authentication session namespace, environment,
expected deterministic thread ID and upload references. Registration client ID
alone is insufficient. Re-pairing cannot replay an old namespace. A known thread
may still be read in the same verified environment after re-pairing. A conflicting
launch reply is held for explicit linking. Uploaded images stay with uncertain
attempts; only certified rejection permits discarding them.

## Compatibility and verification

There is no default harness (ADR 0007 decision 12, API v8): no connection is
seeded, nothing outside a harness's adapter and setup branches on its kind, and
a request names its connection (`connectionId`) unless the project's bindings
or the default connection choose one. `executionConfig` is the only
task/schedule model field; the legacy T3 model inputs, `/api/catalog`,
`/api/connections/t3/projects`, the T3 thread read and the `threads` mirror are
removed. Historical settlement/run/PR records remain readable. Newly observed
execution uses Stage states for every harness, from `execution_observations`
(run span, last word, origin title, tree activity). The T3 adapter decides
T3's states: idle after a run (a latest run id) or settled reads `done`, idle
that never ran stays `idle` (an adopted never-ran T3 session is `backlog`);
T3 settlement never hides active work and accepts a task only through
inferred completion. T3 reports a thread's newest run; a cancelled or
rolled-back run that never started (a queued follow-up) did no work, so the
newest other run decides the observed state and end time. Full-sync progress
and the one-sync-at-a-time guard cover every connection; T3's
`lastFullSyncAt` is recorded by `t3setup.RecordFullSync` (the sync service's
`OnConnectionSynced` hook).

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
Stage `projectId` to resolve that binding. Its launch option `worktree`
(boolean, default off) asks OpenCode for its selected strategy and default
base. Stage verifies the resulting inventory entry, project and directory.
OpenCode declares no base ref or branch option; permission overrides and
image delivery report an actionable reason.

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
`correlated`, closure `archived` (`claude rm`). Setup (`internal/claudecodesetup`): the command
setting, a connect action and disconnect ([api.md](api.md#harness-connections-api-v6)),
stored per machine in `claude_settings` with the config directory the
connection was made with; `serve` composes it only where the machine allows
hosted harnesses. On a worker (`workersetup`) it runs on that machine, with
that machine's `claude`, login and daemon, when the worker allows local
harnesses (the worker service, or `stage worker run --local-harnesses`);
its settings live on the main server's `claude_settings` row for that
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
  images, else none. `hooks-settings-head.json`, the predecessor-base
  variant earlier versions wrote, is rewritten with the hooks file's
  content when it exists (sessions launched with it read it again when
  they wake) and never created. A config the features do not offer,
  an image type other than PNG, JPEG, GIF or WebP, a missing folder or
  executable, images without an image root, and Claude's `Workspace not
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
  unavailable with a reason. Earlier versions wrote images into
  `<project>/.claude/stage-images`: each launch in a project, and the first
  workspace discovery of each adapter for every known project, remove its
  `stage-*` folders and Stage's `.gitignore` there, then the folder if
  empty (best effort; nothing else under `.claude`).
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
  The adapter refuses nothing by origin: the coordinator refuses a session
  another harness wraps ([harness registry](#harness-registry)).
- **Stop** is `claude stop <short>`, for background sessions only.
  **Attention** lists every session Claude lists as waiting for its user:
  a `blocked` background session (also one the daemon retired after an hour
  idle; attach wakes it) and a terminal, IDE or SDK session with status
  `waiting`; the coordinator leaves out a session another harness wraps
  (its origin; T3 surfaces those). The descriptor's attention policy is `silent`: they show as waiting rows in the
  inbox and in the needs-input count, never as events or pushes, and Stage
  tracks one only when the user adopts it (the row's Track). A finished
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
  wins). The coordinator ingests it as a poll. Polling continues: adopted
  sessions, copies, a woken session after the listener moved, and hooks a
  policy blocks (`allowedHttpHookUrls`, `disableAllHooks`,
  `allowManagedHooksOnly`) are polled only. Replacing or removing the
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
`internal/sync` with the real adapter in process and behind a worker
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
server's `codex_settings` row for that machine
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
  archive and turn completion for every thread; Stage observes each and
  ingests it like a poll. `Attention` lists loaded threads waiting on input.
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
socket; `internal/sync` `contract_codex_test.go` runs the coordinator
contract on it in both placements, and `codex/testdata` holds sanitized
fixtures from the live gates.
