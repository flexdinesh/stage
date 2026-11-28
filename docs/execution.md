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
  · [Workspace ownership and routing](adr/0010-harness-workspaces-and-logical-projects.md)
- **Use generic code and declared policy.** Stage reads descriptors and
  capabilities to make its decisions. Each adapter validates and translates its
  own options. Harness-specific operations and display data use declared actions
  and facts. The boundary tests enforce where harness names can appear.
  [Harness foundation](adr/0008-harness-foundation.md)
- **Scope every session.** A session is `{connectionId, externalSessionId}`.
  Parents, adoption, task events and journal provenance use the connection of
  the session they describe. A bare external ID carries no connection identity.
- **Pin exact destinations.** Select a connection and one exact workspace
  under [ADR 0010](adr/0010-harness-workspaces-and-logical-projects.md#ownership-and-exact-destinations).
  Stage revalidates the pair before launch. Planning backlog can omit the
  workspace. Group membership supplies project context and navigation.
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

Display probes add facts to a connected status from the harness's own API,
inside the setup package. Each call is bounded, and the result is cached
for `harness.FactsTTL` (one minute): status reads reuse it, refreshes
included, and the `check` action probes again. A failed or slow probe becomes a `bad` fact, never a status
error. The `Stage MCP` fact reports the server `stage setup` registers
(`harness.StageMCPName`): Codex `mcpServerStatus/list` for that server,
OpenCode `GET /api/mcp` without a location, and Claude Code
`claude mcp get stage` from the home folder, beside its version and login
checks. `mcp list` is not used because it health-checks every server.
`Connected` is `ok`. `Not configured` is `warn`, and when the status has no
other fix, the fix names the matching setup command for the machine's CLI
(`harness.Ports.CLI`). The phase does not change. T3 has no such fact,
because it configures MCP for each provider. Codex also reports usage from
`account/rateLimits/read` for a ChatGPT login: primary and secondary windows
in Codex's words (`5h limit`, `Weekly limit`), the percent used and the
reset time in the machine's local time. The fact is `warn` at 80% and `bad`
at 100%. A failed usage read omits the usage facts.

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

Replacing an instance retires its connection. Tasks, sessions and workspace records
remain on it as history. Edit a backlog task or schedule and select an
exact workspace on the new connection to run future work there. Serve
follows connection changes and runs release and schedule runners per
active connection.
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
catalog row's native workspace ID and directory.
[Capabilities API](api.md#get-apiconnectionsconnectionidcapabilities)

### Inventory

`Workspaces` reports harness project IDs, directories, timestamps,
availability and import evidence. The adapter marks paths on its machine's
filesystem. Discovery uses that machine's `Ops.Inventory` to enrich those
paths, refreshes stored workspaces and organizes new rows into project groups.
A row the harness will not start a session in carries `BlockedReason` in the
harness's words. A harness whose folder trust depends on Git layout declares
a `TrustRule` (its trust records and reason) instead, and `Ops.Inventory` sets
`BlockedReason` when no record covers the folder: a record on the checkout's
main root (the main working tree of a linked worktree), else on the folder or
an ancestor up to the checkout top, or up to the filesystem root outside a
checkout. A blocked row stays listed and grouped but is not an eligible
destination (see [discovery](discovery.md#eligibility)). Failed inventory
reads leave the last stored inventory in place. Full sync guards against
concurrent runs per connection, reports progress and records successful sync
for the active connection. [Workspace discovery](discovery.md)

### Sessions

`Observe` returns the current snapshot for a scoped session.
`Opening` reads its first prompt, including the item ID. `Attention` lists
sessions the harness surfaces to Stage, such as sessions waiting on the user. Stage adds a task through explicit adoption.

Stage records one scoped session per confirmed or linked launch attempt
and the session attached by adoption. A newly adopted session that has
never run uses attempt 0. Restarting clears the current pointer while the
launch is pending and preserves earlier rows. Session history reads the
latest stored observation for each recorded ref. All task interaction
targets the current session.

### Launcher

`Launch` starts execution from a frozen intent and reconciles a prior
dispatch when the launch policy permits it. `Stop` interrupts a session
when its operation is available. The harness performs the native command
against the scoped session.

### Optional traits

Use `execution.As[T](role)` to reach a trait and check the corresponding
operation or descriptor policy before dispatch. `execution/optional.go`
registers `LinkResolver`, `SessionSearcher`, `SessionNamer`,
`WorkspaceFeatures`, `Correlator`, `EventSource`, `Interactive`, `Actor`,
`WorkspaceAdder` and `DirectoryBrowser`. A local adapter implements
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
does not estimate cost. The Session tab shows the usage of the selected attempt.

Task detail opens on the Task tab. The Session tab shows the current session
and links to recorded attempts. Only the current session can receive requests
or messages. Its composer selects the declared `steer` and `queue` operations
while a turn runs and `send` after it ends. The Recent activity panel reads
bounded harness activity and polls while the current turn runs or waits for
input with the tab visible. Past attempts use the recorded observation and read
activity by that attempt's reference.

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
`execution.Op` covers `launch`, `stop`, `adopt`, `reply`, `opening`,
`attention`, `sessionSearch`, `images`, `respond`, `send`, `steer`, `queue`,
`activity`, `sessionRename`, `workspaceAdd` and `directoryBrowse`.
Every adapter declares each operation and a reason when unavailable.
For example, `images` gates image fields and image delivery in the API
and MCP. The remote proxy passes the declarations through.

### Session search

`execution.SessionSearcher` is a separate optional role gated by the
`sessionSearch` operation. It returns a bounded page of metadata for
one connection's harness-listed sessions. The adapter owns the matching
and pagination rules. Stage admits each HTTP request, scopes the cursor,
applies the wrapper policy and looks up existing task ownership by the
full session reference. Explicit preview and adoption revalidate the
session through their own path.

| Harness | Metadata and scope |
| --- | --- |
| T3 | `t3_thread_list` over all projects in the selected environment, with T3's newest-first offset cursor and settled threads included. Search matches the thread title or ID and the project name, ID or root. The workspace label is the T3 project name when available. Listed subagents are ineligible and forks retain their existing eligibility. |
| Codex | App-server `thread/list` in the connected Codex home, newest creation first. Includes saved unarchived CLI, IDE, exec, app-server and unknown-source threads exposed by the daemon. Search matches name, working directory or thread ID. The roster supplies the originator for wrapper eligibility. Prompt previews, turns and attachments stay out of search results. |
| OpenCode | The V2 service's connection-wide `GET /api/session` listing in native newest-first order. Search matches title, directory or session ID. The service must advertise a bounded, cursor-paginated `session.list` in OpenAPI. Services without that contract retain explicit session lookup. |
| Claude Code | Roster entries for the selected configuration, including listed completed sessions. Search matches title, directory, UUID or short ID, newest start first. Only a bounded transcript header of a listed match supplies wrapper origin. Transcript-only historical sessions stay outside the roster. |

T3, Codex and OpenCode filter one native page at a time. A filtered page
can be short or empty while a continuation remains. Each page reflects
the live roster, so changes during browsing can affect later pages.

On workers the operation requires both the `sessionSearcher` trait on
the connection and the `adapter.sessionSearch` request kind. These use
the existing worker channel negotiation. An older worker can still
serve its other operations. [Session search API](api.md#session-search)

### Session rename

`execution.SessionNamer` is an optional role gated by the `sessionRename`
operation: `RenameSession(ctx, ref, title)` gives a session a new name in
its harness. Stage calls it only after a task's title changes (`PATCH
/api/tasks/:id`, whatever the caller: UI, MCP or CLI), and only for a
session Stage launched; an adopted session keeps the name its harness
gave it. The coordinator runs the rename in the background with a 10 s
timeout. Renames of one task run in order, each with the title current
when it runs, so the last edit names the session. A declared-unavailable
operation is skipped. Any other failure (the harness or worker is
unreachable, its declaration cannot be read, or the rename fails) appends
a system journal note, "Could not rename the <harness> session: <reason>",
and never fails the update. The harness owns the name; Stage stores
nothing about it.

Each harness names a session at launch from the task title, and renames
it with its own interface:

| Harness | Name at launch | Rename |
| --- | --- | --- |
| T3 | `t3_thread_launch` `title`. | `t3_thread_update {threadId, action: "rename", title, clientRequestId}` (fresh key per rename, retried with the same key). |
| Codex | `thread/name/set` right after `thread/start`; a failure leaves Codex's preview. | `thread/name/set {threadId, name}`. |
| OpenCode | `POST /api/session` `title`. | `PATCH /api/session/{id} {title}` (204). Available when OpenAPI advertises `session.update` with a `title`; otherwise unavailable with a version reason. |
| Claude Code | `claude --bg -n <title>`. | Unavailable: "Claude Code has no rename interface." |

On workers the operation requires both the `sessionNamer` trait on the
connection and the `adapter.sessionRename` request kind. An older worker
declares it unavailable ("Update Stage on the worker machine...") and
keeps its other operations. [Task update](api.md#patch-apitasksid)

### Workspace add

`execution.WorkspaceAdder` is the optional role behind the `workspaceAdd`
operation ([ADR 0012](adr/0012-harness-workspace-add.md)). The adapter
declares `Capabilities.WorkspaceSources`; each source has typed input
fields (`text`, `choice`, `boolean`, `path`), optional confirmation text
and availability. `ValidateWorkspaceSources` rejects duplicate or unstable
IDs, missing labels, unavailable sources without a reason and an available
operation without an available source. Stage then drops the sources and
reports the operation unavailable.

`AddWorkspace` receives the source ID, input already validated against the
source fields, the caller's `requestId` and the confirmation. It returns
the added workspace's native ID and root, `Existing`, a message and facts.
`*execution.Invalid` is a 400. `*execution.Busy` (the harness changed
nothing; retry) is a 409 `harness_busy`. `*execution.OutcomeUncertain`, a lost worker
reply or the 5-minute deadline is a 504 `outcome_uncertain`; the caller
retries with the same `requestId`. After success Stage refreshes the
connection's inventory, resolves the row by native ID and root, and records
a `workspace_added` project event.

On workers the operation requires the `workspaceAdder` trait and the
`adapter.workspaceAdd` request kind, negotiated on the existing channel.
T3 ([T3 workspace sources](#t3-workspace-sources)) adds folders, clones and
new projects. OpenCode adds existing folders ([Adding folders](#adding-folders)).
Codex trusts folders ([Codex adapter](#codex-adapter)). Claude Code declares
the source `trust` ([Trust in Claude Code](#claude-code-adapter)).
[Workspace add API](api.md#add-a-workspace)

### Folder browsing

`execution.DirectoryBrowser` is the optional role behind the
`directoryBrowse` operation. It lets a person pick a declared `path` field's
value from the harness host's folders instead of typing it. `Browse` takes
`BrowseRequest{Path, ShowHidden}`, where an empty `Path` means the adapter's
default start. It returns `BrowseResult{Path, Parent, Entries, Truncated}`:
the cleaned folder, its parent (`""` at `/`), and child folders only, each
with its name and absolute path. Files are never listed.
`execution.Listing` gives every adapter the same result: dot-named folders
only with `ShowHidden`, sorted by name ignoring case, at most 500
(`MaxDirectoryEntries`, then `Truncated`). Browsing reads only. It never
persists a project, writes a harness file, or runs on behalf of an agent.

`*execution.Invalid` (a missing folder, a file, an unreadable folder) is a
400 with the harness's words. `*execution.BrowseUnavailable` is a 409
`operation_unavailable` with the adapter's reason. Any other failure is a
502 `directory_browse_failed`. On workers the operation needs the
`directoryBrowser` trait and the `adapter.directoryBrowse` request kind;
`BrowseUnavailable` crosses the channel as `unsupported` and keeps its
reason. An older worker without the kind declares the operation
unavailable ("Update Stage on the worker machine…").

| Harness | `directoryBrowse` |
| --- | --- |
| OpenCode | `fs.list`, anchored to a listed project ([OpenCode folder browsing](#opencode-folder-browsing)) |
| Codex | `fs/readDirectory`; the default start is the host user's home ([Codex adapter](#codex-adapter)) |
| T3 | Unavailable: "T3 does not expose folder browsing to Stage; type the path." |
| Claude Code | Unavailable: "Type the path." |

The route is `GET /api/connections/{id}/directories` and only people may
use it ([API](api.md#browse-folders)). In the UI, a `path` input offers
Browse… when its connection declares the operation
([harnesses](harnesses.md)).

### Interaction

[ADR 0014](adr/0014-session-surface.md) defines the session surface.
The `Interactive` trait exposes pending requests and answers behind
`respond` and delivers messages behind `send`, `steer` and `queue`.
An approval takes an option and may accept text
for a designated option. A question group takes answers keyed by question
ID, using option labels or custom text when allowed. An input takes free
text. A preview can describe the command, path, URL or input the request
acts on. A request that is no longer pending returns `ErrNotFound` (404).
A successful follow-up records a task timeline entry. Users and agents can
send and respond, never from the task's own session
(`self_action_forbidden`, [ADR 0011](adr/0011-agent-operation-parity.md)). Claude Code supplies both operations, with responses requiring
its hook receiver. T3 supplies both for its questions and messages
([T3 interaction](#t3-interaction)). OpenCode supplies both for its
permissions, the forms Stage can show and messages
([OpenCode interaction](#opencode-interaction)). Codex supplies both for
command and file-change approvals, its `requestUserInput` questions and
messages to an idle thread ([Codex adapter](#codex-adapter)). A harness that can wait on requests
it can't list declares `unlistedRequests` on its descriptor with where to
answer them; the app then shows that instead of a Reply composer when a
needs-input session lists none. [Interaction API](api.md#session-interaction-api-v6)

`Interactive.Send` receives a `Message.Delivery`. `send` (also empty)
starts a new turn after the previous one ends. T3 and OpenCode currently
also accept a follow-up into their native queues while running. `steer`
injects into a running turn. `queue` admits a message during a running
turn for the harness to deliver as the next turn. The coordinator checks
the corresponding operation. Adapters return `Invalid` for an unsupported
delivery and `Busy` with guidance when the turn state does not fit
(409 `session_busy`). A permanent refusal is 409 `session_refused`.
A successful delivery records its mode and attribution in a timeline
event. Stage keeps the message text in the dispatch only. Workers relay
`send` on `adapter.send` and `steer` and `queue` on `adapter.deliver`.
The legacy send channel accepts only the send mode.

`ActivityReader` reads bounded recent activity for the current session or
a recorded attempt behind that session connection's `activity` operation.
Stage passes the recorded ref and keeps no activity items. The adapter returns the
last N items in chronological order, the current plan, changed files,
model and optional harness details. The coordinator validates kinds and
statuses and enforces the item, title, text and details limits.
Workers relay the query through `adapter.activity`, bounding the result
before encoding it. Older workers without the channel kinds report their
update guidance in the connection operations.

First answer wins ([ADR 0013](adr/0013-tree-steering.md#first-answer-wins)):
`tasks.state_revision` counts real state transitions, and a send or
respond with `expectedStateRevision` is guarded. The coordinator takes the
task's lock, re-reads it and requires `needs_input` at that revision, not
yet answered (`answered_revision`), else 409 `already_answered`. It holds
the lock through the harness call and the record, which sets
`answered_revision` in the same write as the event, then observes the
session after unlocking. Without the revision nothing is locked or
required. The user, the steerer and other agents race only through Stage:
an answer typed directly in the harness cannot be prevented, and the
loser's later send queues behind it or is `Busy`. Send and respond record
the caller session as `Cause.Session` and the target as `Cause.TaskID`.

Steering delivery ([ADR 0013](adr/0013-tree-steering.md#delivery)) sends
as Stage itself (no caller session) through `SendRecorded`: the same send,
unguarded, with `Message.RequestID` `steering-<binding>-<through>` so a
redelivery is one message on harnesses that deduplicate (T3, OpenCode,
Codex), and its own record (the binding's delivery and the steerer event
"Steering update sent (through #N)") in place of "Follow-up sent from
Stage". `Busy` leaves the update pending for the next pass; other errors
back off. A crash between the harness call and the record sends the same
request again (at least once). [Steering delivery](api.md#steering-delivery)

### Actions and facts

A declared action is a harness-owned operation Stage relays. `execution.Action`
carries the target, stable ID, caller policy, availability, confirmation
and input schema. Connectors own connection actions, including setup.
`Capabilities.Actions` describes session and project actions. Project
actions use the exact workspace on the selected connection, including a
listed row its harness will not launch in (an action may fix that) or has
archived (restoring it is one), and Stage refreshes the connection's
inventory after one succeeds or ends uncertain (best effort). A harness
whose project actions depend on the workspace (T3's archive state)
declares them through `WorkspaceFeatures`. Project actions change the
harness's project only: Stage group titles stay as they are. Stage resolves that scope, checks caller policy and schema, and
reaches the owner through `execution.As[execution.Actor]`. The owner
validates semantic input. `ActionRequest.confirmed` tells the owner the
caller sent `Stage-Action-Confirmed: 1` (MCP `run_action` `confirmed`); an
owner may require it, as Claude Code's `trust` does.
Workers advertise the `actor` trait and receive `adapter.act`.
A lost reply leaves the outcome uncertain. Stage does not replay the action.

A session's observation can carry `actions`: `{id, available, reason}` per
declared session action, in the harness's vocabulary (already archived,
still running). Stage applies them (`execution.ApplyActionStates`): the task's
action list reads the last stored observation, and dispatch reads the fresh
observation it takes before `Act`, refusing with 409
`operation_unavailable` and the harness's reason. A state only makes an
action unavailable; an older worker that omits states leaves the
declarations as they are, and the harness still refuses an invalid request.
A session action from the task's own session is 403
`self_action_forbidden` ([ADR 0011](adr/0011-agent-operation-parity.md#never-on-its-own-work)):
closing a session can infer completion.

Each session action is user-triggered, by a person or an agent, never
automatic. Archiving or settling a session in its harness flows through
observation and [inferred completion](#closures-and-inferred-completion)
unchanged: the action itself never completes a task.

| Harness | Session actions | Harness call | Unavailable when |
| --- | --- | --- | --- |
| T3 | `settle` "Settle in T3", `archive` "Archive in T3" | `t3_thread_organize {threadId, action}` | settle: settled, archived, running or waiting for an answer; archive: archived |
| Codex | `archive` "Archive in Codex" | `thread/archive {threadId}` | archived |
| OpenCode | `archive` "Archive in OpenCode" | `PATCH /api/session/{id} {archived: true}` (`session.update`) | archived; the service's spec lacks `session.update` with `archived` |
| Claude Code | none | | |

| Harness | Project actions | Harness call | Unavailable when |
| --- | --- | --- | --- |
| T3 | `rename` "Rename in T3" (`title`), `archive` "Archive in T3", `restore` "Restore in T3", `remove` "Remove from T3" | `t3_project_update {projectId, title}` or `{projectId, archived}`; `t3_project_delete {projectId}` | archive: archived; restore: not archived; all: T3 no longer lists the project or its list fails |
| OpenCode | `rename` "Rename in OpenCode" (`name`) | `PATCH /api/project/{id} {name}` (`project.update`) | the `global` project; the service's spec lacks `project.update` with `name` |
| Claude Code | `trust` "Trust in Claude Code" | described with its [adapter](#claude-code-adapter) | |
| Codex | none | | |

Renames, archives and restores are open to people and agents. T3's remove
is destructive and user-only, so it needs confirmation.

Each confirms first. T3's archive does not settle a thread, so only Settle
in T3 can infer completion; its confirmation says so. The harness's own
refusals (T3 orchestration errors, Codex RPC errors) return 409
`action_busy` with its message.

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
inferred completion. Settle in T3 and Archive in T3 relay
`t3_thread_organize` ([Actions and facts](#actions-and-facts)); the
observation's action states mirror T3's own refusals.

### T3 project actions

T3's project actions relay its project management over MCP
(`server/internal/t3/project_actions.go`) on the T3 project behind a
workspace row. The Stage group keeps its title.

- **Rename** sets T3's title (`t3_project_update {projectId, title}`).
- **Archive and restore** set `archived` (`t3_project_update`). T3 keeps the
  threads and files. Archive confirms first. An archived row stays a
  project action target, so it can be restored from Stage.
- **Remove** calls `t3_project_delete {projectId}` and never sends `force`.
  It is destructive and user-only, and it confirms: "Remove this project
  from T3? T3 keeps the files on disk. A project with threads can't be
  removed; archive it instead." T3 refuses a project with threads
  (archived threads included); Stage relays that as "T3 can't remove a
  project with threads. Archive it instead." (400), since T3's own wording
  names the force flag.
- **Availability.** The adapter implements `WorkspaceFeatures`: it lists
  T3's projects and marks Archive unavailable on an archived project
  ("Already archived in T3.") and Restore on any other ("Not archived in
  T3."). When T3 no longer lists the project, or the list fails, all four
  are unavailable, and the capabilities still answer, since launches read
  them too. Rename and remove work on archived projects, as in T3.
- **Refusals.** T3's other refusals are 400 with its message; "The project
  was not found." is 404.
- **Lost replies.** `t3_project_update` with only a title or `archived`
  sets values, so the client retries it once on a closed connection. A
  transport failure on either call lists again: an update the listing
  shows, or a project it no longer lists after remove, succeeds. Otherwise
  the outcome is uncertain (502).

### T3 interaction

T3 implements `Interactive` over its MCP (`server/internal/t3/interaction.go`).

- **Requests.** `t3_pending_request` lists a thread's pending user-input
  requests; each is read and becomes one `question` request. Questions keep
  their ID, header, prompt and multi-select, are `custom` unless T3
  disallows free answers, and their options keep label, description and
  preview. A request answered while Stage reads it is left out. T3 reports
  no request time, so `createdAt` is when this Stage process first listed
  the request (in memory: a restart resets it to the next listing). T3's
  MCP neither lists nor answers permission requests: they are answered in
  T3, and the descriptor declares `unlistedRequests` ("Approve permission
  requests in T3."). So a T3 task can read `needs_input` with no
  answerable requests: it waits on a permission request, and Open in T3 is
  the way to approve it.
- **Respond.** Stage answers by option label or one custom text. The adapter
  sends T3's option value for a valued option and checks the answers by
  T3's client rules (every question, arrays only for multi-select, custom
  text only where allowed) before sending. A misfit answer, an option or
  text without answers, or T3 refusing the answer is `*Invalid` (400). A
  request already answered, expired, unknown, or on a missing thread is
  `ErrNotFound` (404).
- **Send.** `t3_thread_send` in mode `queue`, always ([ADR 0013](adr/0013-tree-steering.md#send-semantics)):
  an idle thread starts a new turn, and a running one takes the message as
  a new turn once its current turn ends. Stage never steers or restarts a
  turn. The message's `RequestID` is T3's `clientRequestId`: the coordinator
  sets a fresh `stage-send:<random>` per API call, a steering delivery
  passes its own stable ID, and T3 returns the first send's message and run
  for a repeat, so a send lost on a closed connection is retried once. A
  thread with any pending request takes no message (`*Busy`, 409
  `session_busy`: answer its questions first, or approve its permission
  request in T3). An archived thread, or any other thread T3 won't send to
  (`thread_not_sendable`: archived, or its worktree can't be restored), is
  `*Refused` (409 `session_refused`).
- **Live check** (2026-11-28, local T3 from source at `9f83ba58`, throwaway
  scratch thread): an idle send reported `delivery: started`; a send while
  a turn ran reported `queued` and ran as the next run only after that run
  completed; a repeat with the same `clientRequestId` returned the same
  message and run; a send while the thread waited on a question was
  accepted by T3 as `queued` (Stage refuses it first with `*Busy`, so the
  question is answered before the next turn); answering the question by
  option label (T3 takes the label when the option has no value) resumed
  the turn and the queued message ran after it; a send to the archived
  thread failed `thread_not_sendable` ("is archived and cannot receive
  messages"). T3 deduplicates per MCP
  client (its message id embeds the client), so the ID covers retries from
  one Stage connection.

### T3 workspace sources

T3 implements `WorkspaceAdder` over its MCP with Stage's mcp-client token
(`server/internal/t3/workspace_add.go`). It declares three sources:

| Source | Input | T3 calls |
| --- | --- | --- |
| `folder` "Existing folder" | `path` (required), `title` (defaults to the folder name), `createIfMissing` | `t3_project_create {title, workspaceRoot, createWorkspaceRootIfMissing}` |
| `clone` "Clone repository" | `remoteUrl` (required), `destination` (required, new or empty), `title` | `t3_project_clone {remoteUrl, destinationPath}`, then `t3_project_create` on the returned `cwd` |
| `new` "New project" | `title` (required) | `t3_project_create {title}`: T3 makes a Git repository in its projects folder |

- **Paths.** Paths must be absolute on the T3 host; `~` and relative paths
  are refused before T3 is called. Paths are compared cleaned, as T3
  resolves them.
- **Existing targets.** `t3_project_create` has no request ID, so every
  source lists projects first. A folder or clone destination T3 already
  registered returns `Existing: true` without a call. An archived T3
  project there is refused: restore it in T3. T3's duplicate error ("The
  workspace is already registered to a project.") lists again and returns
  the match.
- **Refusals.** A T3 tool error is `*Invalid` with T3's message. T3
  reports a missing folder only as "The operation could not be
  completed."; Stage adds a hint to create it. A clone into a non-empty
  destination suggests Existing folder, for a checkout an earlier request
  cloned. A register failure after a clone names the checkout.
- **Lost replies.** A transport failure lists again. A folder at the
  requested root is returned. After a clone, the register is retried once.
  For `new`, exactly one project with that title that was not in the first
  list is returned. Otherwise the outcome is uncertain (504). T3's clone has
  no timeout, so a clone that outlasts the 5-minute deadline is uncertain
  too. A retry then gets the non-empty destination refusal.
- **New projects.** A retry of `new` after a 504 can create a second
  project, because T3 adds a suffix to a taken folder name (`name-2`).
  `commitError` (T3 created the project but not its first commit) is a
  warn fact.

## OpenCode adapter

Stage connects to an existing OpenCode V2 service using its URL and optional
HTTP Basic credentials. It checks the required operations and worktree, session
and prompt request schemas in `/openapi.json`. If a requirement is missing,
the error names the reported version and operation. Any version that meets
those requirements can connect. The live suite below records the tested
binary and should run again when a release changes behavior Stage relies on.

OpenCode owns project inventory, workspace strategy, model selection and agent policy.
An empty execution config uses its defaults. An exact workspace provides the model
catalog, variants and visible primary agents. Pass `workspaceId` to scope the
capabilities endpoint on that connection. Its launch option `worktree`
(boolean, default off) asks OpenCode to create the worktree using its
default base. Stage verifies the resulting inventory entry, project and
directory. Permission overrides and image delivery report a reason when
unavailable. Archive in OpenCode relays `PATCH /api/session/{id}`
(`session.update` with `archived`, checked softly in `/openapi.json`: a
service without it keeps connecting and declares the action unavailable;
[Actions and facts](#actions-and-facts)).

Rename in OpenCode, a project action, sets the project's name with
`PATCH /api/project/{id} {name}` (`project.update`, checked softly in the
spec like `session.update`; `server/internal/opencode/project_actions.go`).
The Stage group keeps its title. OpenCode's catch-all `global` project
can't be renamed. A 404 is not found, a 400 relays OpenCode's message, and
a lost reply checks the project listing for the new name, else the outcome
is uncertain. An archived OpenCode project lists no actions: verification
refuses it with "Archived in OpenCode. Unarchive it there, then refresh."
(400).

Every location-scoped OpenCode request (`/api/location`, `/api/model`,
`/api/agent` with `location[directory]`) builds that location on first load,
and building it persists the directory as a project and clears its archive
(OpenCode 2.3.2, `Project.resolve` with persist). So Stage verifies a bound
directory with pure reads only: `GET /api/project?archived=true` (the project
must be listed and not archived) and `GET /api/worktree?projectID=` (the
directory must be the project's canonical directory or a listed worktree).
`Workspaces`, `Features` and verification never call `/api/location`. The
workspace catalog reads `/api/model` and `/api/agent` with the location only
after verification passes, when the project exists unarchived and the read
can neither create nor unarchive it. Launch passes the location for its own
directory. Only an explicit add calls `/api/location`.

### Adding folders

OpenCode declares one [workspace source](#workspace-add), `folder`
("Existing folder", a required `path`: "Absolute path on the OpenCode
host"). OpenCode v2 has no create-project endpoint. Its location side
effect is the add: `GET /api/location?location[directory]=<dir>` for an
existing directory resolves and persists the project, and clears an
archived time. This behavior is undocumented. It was read from OpenCode's
bundled source (the `@opencode/Location` and `@opencode/Project` modules
of `@opencode/cli` 2.3.2) and checked once against a throwaway
`opencode serve` with temporary `HOME` and XDG directories. The fake in
`server/internal/opencode/workspace_add_test.go` pins it.

The source and `workspaceAdd` are available when `/openapi.json` has
`location.get` (`project.list` is already required). Otherwise both say
"This OpenCode version cannot add folders (location.get is missing)."

The adapter requires an absolute path and cleans it. It lists projects
(`GET /api/project?archived=true`), calls the location, then lists them
again. The location's project must be listed, unarchived, and its resolved
checkout must be the project's canonical directory or a saved worktree
(`GET /api/worktree?projectID=`). Stage gets that project's row (its ID
and canonical root). `existing` is true when the project was listed and
unarchived before the call.

| Folder | OpenCode 2.3.2 | Stage |
| --- | --- | --- |
| Plain folder, no VCS | Its own project (`vcs` unset, canonical is the folder) | A row of its own. Not importable, so it groups standalone. |
| Repository checkout | The repository's project | The project's row |
| Subfolder, linked worktree, symlink | The repository's project; a linked worktree also gets a worktree row | The project's row at its canonical root. The message names the folder OpenCode keeps under it. |
| Git repository with no commit or remote | The catch-all `global` project | 400: "OpenCode files this folder under its global project. Give the repository a commit or a remote, then add it again." |
| Missing folder | 404 `LocationNotFoundError` | 400: "Folder not found on the OpenCode host." |
| A file | 500 | 502 |

A lost or unreadable location answer, or a failed listing after the
location answered, is an uncertain outcome (504): OpenCode may have saved
the project, and retrying with the same `requestId` is safe.

### OpenCode folder browsing

`directoryBrowse` is available when `/openapi.json` has `fs.list` (`GET
/api/fs/list`). Otherwise it says "This OpenCode version cannot list folders
(fs.list is missing). Type the path."

OpenCode 2.3.2 (checked on a throwaway `opencode serve` with temporary
`HOME` and XDG directories) lists the direct children of an absolute
`path`: files and folders, hidden ones included.
Entry paths are relative to the request's location. Folders end in `/`.
A request without `location[directory]` resolves the default location (the
service's working directory) and saves it as a project the first time, even
when it lists another path. A request naming an existing project's folder
saves nothing new. A listed path is never saved.

So every listing names a location: the most recently active listed project
that is not archived and not `global` (archived projects are skipped
because a location request restores them). The default start is that
project's parent folder. If its folder is gone (404
`LocationNotFoundError`), the next listed project anchors, up to five. With
no usable project, Browse is unavailable: "OpenCode lists no projects yet.
Type the path." A missing path or a file is 404 `FileNotFoundError`, and
Stage answers 400 "Folder not found on the OpenCode host: <path>." The
fake in `server/internal/opencode/directory_browse_test.go` pins this.

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

### OpenCode interaction

OpenCode declares the V2 session operations per workspace.
`respond` requires the OpenAPI permission `decision` and form `answer`
reply bodies. `steer` and `queue` require their respective prompt delivery
values. `send` requires the prompt delivery contract and uses its default
on an idle session. `activity` requires `session.diff` alongside the
session and message reads. Missing optional operations show an update
reason. The connection and its other operations stay available.

- **Requests.** `GET /api/session/{id}/permission` then
  `GET /api/session/{id}/form`, in OpenCode's order. A request whose
  session or ID prefix (`per`, `frm_`) does not match fails the read, and a
  missing session is `ErrNotFound`. OpenCode reports no request time, so
  `createdAt` is zero.
  - A permission is an `approval` with OpenCode's options: `once` "Allow
    once", `always` "Always allow" with the `save` patterns when supplied,
    and `reject` "Deny". OpenCode saves rules when it supplies patterns.
    The Deny text is sent as correction feedback and OpenCode rejects the
    run's sibling asks. Its title is OpenCode's description of the action
    ("Run shell commands"), or "Permission required: <action>" for an
    action OpenCode does not describe. Its detail is the request's message.
    Its preview is the resource list.
  - A form is a `question` request when Stage can show every field: a
    string (its options, custom text where allowed, free text without
    options), a multiselect, a boolean (Yes or No) or a number. Each field
    is a question keyed by its field key. The agent's question tool
    (`metadata.kind` `question`) is titled "OpenCode asks" or "OpenCode
    asks N questions"; other forms keep their title. A form with a
    condition (`when`), a hidden or an external field is not listed, and the
    descriptor's `unlistedRequests` says to answer it in OpenCode.
- **Respond.** The request ID prefix picks the endpoint, and Stage re-reads
  the session's list first: a request no longer listed is `ErrNotFound`. A
  permission takes one of its options, with
  text only on Deny
  (`POST .../permission/{id}/reply {decision, message?}`). A form takes
  answers by field key: option labels become option values, Yes or No a
  boolean, a number its value; fields left out stay unanswered and OpenCode
  checks required ones (`POST .../form/{id}/reply {answer}`). An answer
  that does not fit is `*Invalid` (400) before anything is sent. OpenCode
  answering 404 or 410, or 409 for a settled form, is `ErrNotFound` (404).
  A rejected answer (400 or 422) is `*Invalid` with OpenCode's message.
- **Send, Steer and Queue.** `POST /api/session/{id}/prompt` sends text.
  Stage checks active turns and pending permissions and forms before the
  prompt. Send omits `delivery` and starts a new turn while idle. Steer
  passes `delivery: "steer"` to join a running turn. Queue passes
  `delivery: "queue"` for the native next turn. A mismatched mode or pending
  request returns `*Busy` (409 `session_busy`). The OpenCode endpoint has
  no expected-turn field, so the session can change after the check.
  Stage derives the ID from the session and request ID for native
  deduplication. It checks the original ID in the inbox or messages if
  a retry arrives after the state changed.
- **Activity.** Stage reads recent `/message` pages (up to three pages of
  100 messages) and shows at most the requested number of supported items.
  Reasoning displays a title. Tool inputs supply previews, and the latest
  completed todo tool call in the window supplies the plan. `/diff` supplies the
  latest turn's file counts, grouped by path. `Session.Info` supplies the
  model, agent, variant and cumulative cost and token counts. The OpenCode
  header shows agent, model and variant, and the shared Usage panel shows
  the reported totals. Stage reads the view on demand.

Workers use the existing `adapter.requests`, `adapter.respond`, `adapter.send`,
`adapter.deliver` and `adapter.activity` channels. Queue management and
using inbox and usage events for refresh are follow-ups. Polling reconciles
activity and usage.

### OpenCode push

OpenCode is an `execution.EventSource` over its V2 event stream
(`server/internal/opencode/events.go`): `GET /api/event`
(`event.subscribe`), which streams events of every location. Stage
subscribes with one repeated `types` query per event: session status and
idle, execution started, succeeded, failed and interrupted, permission
asked and replied, form created, replied and cancelled, session archived
and unarchived, and `session.renamed` when advertised. The stream is
available when OpenAPI advertises `event.subscribe` and its `types`
parameter lists every one of those events except `session.renamed`.
Otherwise `Events` returns at once and the connection is polled only, with
no stream error.

- **Wake-ups.** An event is a wake-up, not evidence. Stage reads the
  session `data.sessionID` names (`data.form.sessionID` for a created form)
  through `Observe`, the same snapshot polling takes, and passes it to the
  local runner. Events of other types, events with no valid session ID,
  heartbeat comments and repeated `server.connected` frames are ignored.
- **Coalescing.** The stream reader never waits on a read: it marks the
  session, and one worker observes each marked session in turn, once however many
  events named it meanwhile (at most 256 sessions waiting; more are left to
  polling). A failed read is skipped.
- **Reconnects.** The stream is volatile: a slow consumer overflows and
  fails it, and events during a disconnection are missed. A failure frame
  (`event: effect/http-api/stream/failure`), any other named event, a first
  frame other than `server.connected`, invalid JSON, the end of the stream,
  45 seconds without a frame (OpenCode sends a heartbeat every 15 seconds)
  or no response within 20 seconds return an error. The runner calls again
  after five seconds, and polling reconciles what the stream missed.

Workers serve it through `adapter.events` and the `eventSource` trait.

### Reproduce the live gate

Run `STAGE_TEST_OPENCODE=/absolute/path/to/opencode mise run opencode:live`. Include `STAGE_TEST_T3` (T3 executable) and `STAGE_TEST_FX` (upstream `scripts/mobile-behavior/fake-fx.ts`) to exercise a real T3 to OpenCode to T3 tree with both predecessor services disconnected in turn.
The test starts foreground loopback servers with temporary homes, repositories and
databases. A scripted local model supplies success, failure and interruptible runs.
The standalone CLI requires Basic authentication. The suite also tests an existing
service exposed through a no-auth loopback proxy.

The verified Linux amd64 binary reported `2.3.0`, SHA-256
`2e0e4656521ca56e5ba16a00d48429958b8105b3fa6466710a4350e68759381f`.
The source reference was `df46f6931da0c934a59d375ac44525846f21c718`.
`TestLiveOpenCodeInteractive` (the interaction gate) was added later and
verified on `2.3.2` (Linux amd64, SHA-256
`e6543f6e5c39c40971ba38afd1819ac852c29ac735b5a89065d3d148d47f27ad`), with
the rest of the suite: a bash permission (asked by config) answered Allow
once, the question tool's form answered, and a message sent twice with one
request ID while a turn runs, delivered once after that turn ends.
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
  interrupted; a terminal, IDE or SDK session by `status`. A `working`
  session with `status: idle` whose transcript shows its turn over is
  done: Claude keeps `state: working` when the agent ends its turn still
  waiting on something (a steerer "waiting for Stage updates", seen live
  with Claude Code 2.1.332 in #551), and nothing runs. Not listed:
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
- **Inventory** uses the state file's project map and the exact folder paths
  on Claude's current native session roster. On the owning machine, Stage
  reads `~/.claude.json` by default or `.claude.json` inside an explicit
  `CLAUDE_CONFIG_DIR`. A fresh state object can omit the project map.
  Managed worktrees are excluded, and machine evidence still limits
  automatic import to Git checkout roots. Missing or malformed state and
  roster failures keep the last successful inventory. See [discovery](discovery.md#shipped-inventory).
  `claude --bg` refuses an untrusted folder ("Workspace not trusted"), and
  Agent SDK runs (T3, for example) record folders without trusting them, so
  every row carries Claude's trust as a `TrustRule`: the project keys whose
  `hasTrustDialogAccepted` is JSON `true`, matched verbatim as Claude looks
  them up. This mirrors Claude Code
  2.1.332's background trust check. Trust is keyed on the canonical
  repository root (a linked worktree's main checkout), then on the nearest
  record from the folder up to the checkout top, or up to the filesystem root
  outside a checkout. A `false` record does not stop a trusted ancestor.
  Untrusted rows stay listed with "Not trusted in Claude Code. Use Trust in
  Claude Code, or run claude in this folder once and accept the trust
  prompt." The home
  folder is trusted one interactive session at a time, so it gets its own
  reason unless a record above it covers it. A truthy
  `CLAUDE_CODE_SANDBOXED` (`1`, `true`, `yes`, `on`) trusts every folder.
  Not mirrored: Claude's fallback for a record reached through a symlinked
  launch path. Stage launches with the folder as the working directory, so
  only the real path matters.
- **Trust in Claude Code** ([ADR 0012](adr/0012-harness-workspace-add.md)
  decision 8). Claude has no project or trust API; its documented fix is
  `projects["<folder>"].hasTrustDialogAccepted: true` in the state file.
  Stage keeps `claude --bg` launches, which need it. The workspace source
  `trust` (input `path`, an absolute existing folder on the adapter's
  machine) and the project action `trust` on a row Claude does not trust
  share one code path; both are labelled "Trust in Claude Code", allow
  people and agents, and run only with the confirmation (`confirmed: true`,
  or `Stage-Action-Confirmed: 1` for the action, which reaches the adapter
  as `ActionRequest.confirmed`): "Claude Code will trust this folder. Its
  .claude settings, hooks and MCP servers will run in Claude sessions
  there." Nothing writes automatically.
  - Target: the folder's canonical path (symlinks resolved), the key Claude
    looks up. Refused: home (its trust is session-only), the filesystem
    root and home's ancestors (a record there trusts every folder below,
    home included), Claude-managed worktrees, and a missing state file.
  - Existing: Claude already trusts the folder (the inventory trust rule,
    settled by `machine.Ops` evidence the setup injects, or
    `CLAUDE_CODE_SANDBOXED`) and the folder has its own project entry, so
    Claude lists it. Nothing is written. A folder trusted only through an
    ancestor or its main checkout gets its own record, so it is listed.
  - The project action is available only on a row Claude does not trust
    (`FeaturesForWorkspace`); otherwise "Claude Code already trusts this
    folder."
  - Safe write, mirroring Claude Code 2.1.332: Claude saves its state under
    a proper-lockfile lock (the directory `<state file>.lock`, touched every
    5s, stale after 10s) and merges `projects` from disk under it. Stage
    takes the same lock (a stale one is taken over; a live holder past 12s
    answers busy), reads the raw bytes, and patches only that key: the
    value, or one inserted member laid out like its neighbors (a missing
    entry is Claude's minimal `{"hasTrustDialogAccepted": true}`, the shape
    its runner writes). A semantic check proves nothing else changed. It
    writes a temp file beside the real file (through a symlinked state
    file), fsyncs it, keeps the file mode, and renames only if the hash,
    size and mtime still match the read. Three changed reads answer busy
    (409 `harness_busy` from the add route, `action_busy` from the
    action); nothing was written, so a retry is safe.
  - The result names the folder; a person's response adds a "Trust record"
    fact naming the key and file.
- **Features.** Models: Claude's default, `opus`, `sonnet`, `haiku`,
  `fable` (aliases for the latest). Harness-wide option `effort` (`low` to
  `max`) and launch option `worktree` (boolean, default off; Claude
  starts the worktree from origin's default branch and names the branch,
  so there is no base ref or branch option). Permissions: `manual`,
  `acceptEdits`, `auto`, `plan`, `dontAsk`, `bypassPermissions`. Images
  and send are available. `workspaceAdd` offers the source `trust`, and the
  project action `trust` is scoped per folder (`FeaturesForWorkspace`);
  both are unavailable without the machine evidence the setup injects.
  Unavailable with reasons: respond without the hook receiver (no state
  folder, or it could not open).
- **Follow-ups** ([ADR 0005](adr/0005-claude-code-harness.md) decision 18,
  E12). **Send** reads the session list fresh (one send at a time) and
  takes a message only for a listed background session that is `done`,
  `working` but idle after its turn (as in Observe), or `blocked` on a
  finished turn (no `waitingFor`, status not `waiting`, the transcript's
  turn over). Else `Busy` with what to do: working, a
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
credentials and never edits Codex's files; it changes Codex's config only
through the app server, when a person or agent trusts a folder
([ADR 0012](adr/0012-harness-workspace-add.md)). On a worker (`workersetup`) the
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
- **Workspace add.** One source, `trust` ("Trust folder"): an absolute
  `path` on the Codex host and the confirmation "Codex will trust this
  folder: it applies the folder's Codex project config. This also applies
  to the Codex CLI." The adapter cleans the path and requires
  `fs/getMetadata` to report a directory. A folder `config/read` already
  trusts is `existing`. Otherwise `config/batchWrite` upserts key path
  `projects` with `{<path>: {trust_level: "trusted"}}`: the path stays one
  key (an unquoted `projects.<path>.trust_level` key path splits on dots),
  and other projects and fields are kept. A prior `untrusted` entry becomes
  trusted. The write sends the user layer's `expectedVersion` and retries
  once on `configVersionConflict`. `configLayerReadonly` and
  `configValidationError` are invalid input with Codex's message;
  `okOverridden` adds a warn fact; a lost reply is `OutcomeUncertain`.
  Stage re-reads `config/read`: a folder a higher layer keeps untrusted is
  invalid input naming the override. Codex's experimental `project/*` API
  (create, import, list) is a future option; Stage does not use it.
- **Folder browsing.** `directoryBrowse` lists a folder with
  `fs/readDirectory {path}` (read only; checked on a throwaway 0.172.0 app
  server). The default start is the host user's home: the daemon serves a
  local socket and runs as the adapter's user. Codex returns entry names
  with their resolved kind (a symlink to a folder is a folder; a broken one
  is left out). A missing folder, a file or an unreadable folder is invalid
  input with Codex's OS error. A daemon without the method declares Browse
  unavailable: "This Codex version cannot list folders (fs/readDirectory is
  missing). Type the path."
- **Launch (`reserved`).** Validation rejects before any effect (an
  unknown option such as `worktree`, missing directory, non-image attachment, invalid option or
  permission). `thread/start`, checkpoint the thread id, best-effort
  `thread/name/set`, then `turn/start` with `clientUserMessageId` set to the
  request id. Replay reads the checkpointed thread: a first turn carrying
  the client id (or the prompt) is the admission, no turn starts it, a
  first turn whose user message is not recorded yet stays uncertain, any
  other turn fails. Stage then unsubscribes; the turn keeps running. For a
  moment after `turn/start` Codex answers `thread/turns/list` with -32601
  "list_turns is not supported yet", then may show the turn in progress on
  a thread it reports not running (codex-cli 0.172.0, live in #551): the
  launch's observation retries both for up to 10 s, so a started launch
  is confirmed instead of failing into a replay.
- **Observation.** `thread/read`: `active` is running or needs_input (the
  waiting flags); the last turn's outcome is done, failed or interrupted;
  an in-progress turn on a thread the daemon unloaded, or a system error,
  has no terminal evidence and is an error. For a moment after every
  `turn/start` and unsubscribe (launch and send) Codex reports the thread
  `idle` and lists the new turn in progress or `interrupted` without
  `completedAt`; a real interrupt has `completedAt` (codex-cli 0.172.0,
  live in #555, gate g11). Observe rereads such a snapshot for up to
  500 ms; an unclosed interrupt that persists reads interrupted. Archive
  closes the session (`archived` closure). The reply is the final answer,
  4,000 characters.
- **Archive.** Archive in Codex relays `thread/archive`
  ([Actions and facts](#actions-and-facts)); unavailable once archived.
- **Push.** `execution.EventSource`: the daemon broadcasts thread status,
  archive and turn completion for every thread. Stage reads each thread and
  passes the observation to the local runner. `Attention` lists loaded
  threads waiting on input.
- **Interaction** (`execution.Interactive`). The runner passes tracked
  sessions to `TrackedEventSource` on the adapter's machine. On Codex
  0.172 or later it subscribes to a tracked thread with an active turn
  (running or waiting) using `thread/resume {threadId, excludeTurns: true}`.
  This attaches to the existing turn and replays its pending requests
  with their daemon-wide JSON-RPC IDs. Stage passes no configuration
  overrides and starts no turn. It unsubscribes when the thread is idle
  and reattaches to active threads after a connection drop.
  - **Requests.** Command, file-change and permission approvals are
    held in memory on the subscription by thread and JSON-RPC ID.
    `serverRequest/resolved` removes a request, including when a Codex
    client answered first. Turn completion and connection loss clear
    their pending approvals. The command preview shows the exact
    command, working directory and reason when present. Stage offers
    Codex's `availableDecisions` in order. The plain decisions are
    `accept`, `acceptForSession`, `decline` and `cancel`. The
    `acceptWithExecpolicyAmendment` and `applyNetworkPolicyAmendment`
    decisions carry Codex's proposed rules. File changes show paths and counts when
    matching file item metadata arrives, or direct the user to the
    resume command to review the diff. Permission requests show the
    requested profile with turn, session and denial choices.
  - **Respond.** Stage sends the result on the subscribed connection.
    Command and file approvals take `{decision}`. Permission approvals
    take `{permissions, scope}`, with an empty permission profile for
    denial. An invalid option is `*Invalid`. An absent request is
    `ErrNotFound`. Stage leaves experimental questions, MCP elicitations
    and other server requests for the Codex client. The legacy
    `applyPatchApproval` and `execCommandApproval` methods are outside
    the v2 event path Stage uses.
  - **Send.** `thread/read`: a thread with a turn running is `*Busy`
    ("Codex is working…", or waiting on an approval or question); Stage
    never steers a turn (`turn/steer` is unused). Otherwise resume (which
    loads a thread the daemon unloaded: `turn/start` on one answers
    -32600), check the status again, then `turn/start` with
    `clientUserMessageId` = the message's request ID (a random `stage-` id
    without one). A message whose request ID the newest turn's user message
    already carries was delivered and is not sent again (the coordinator
    sets a fresh request ID per API call). An archived thread is
    `*Refused` (409 `session_refused`) and a `turn/start` Codex refuses
    (-32600) is `*Busy`, each with Codex's message.

  Links carry `command` (`cd <dir> && codex resume <id>`), which task rows,
  notifications and task detail offer as Copy resume command.
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
fixtures from the live gates. The fake also resumes threads (replaying
their pending server requests), takes client replies to server requests
and broadcasts `serverRequest/resolved` to subscribers. `mise run codex:live`
launches a read-only scratch thread, answers a replayed approval in Stage,
and verifies that the approved command ran. The raw protocol gates run
with `STAGE_CODEX_GATES=1 go test -tags codexlive -run TestLiveGate
./internal/codex/`. These gates use the signed-in account and delete their
scratch threads afterwards.

## Steering live check

Tree steering ([ADR 0013](adr/0013-tree-steering.md)) was verified live on
2026-11-29 (task #551) with Claude Code 2.1.332, OpenCode 2.3.2, codex-cli
0.172.0 and T3 from source, against an isolated dev server
(`.scratch/dev/<branch>`) and a throwaway Git repository. Four trees, one
per steerer harness, each held four workers (one per harness) in a Latin
square over four roles, with a 60 s hold: R1 the steerer answers, R2 the
user answers first and the steerer's guarded answer must get 409
`already_answered`, R3 the steerer calls `escalate_to_user`, R4 nobody
answers until the hold expires. Each steerer was replaced once while
running, added one follow-up task and accepted every member. Workers ran
cheap models, steerers mid-tier models.

| Scenario | Claude Code | T3 | OpenCode | Codex |
| --- | --- | --- | --- | --- |
| Replace (old session stopped) | yes | yes | yes | yes |
| R1 steerer answers | yes | yes | yes | yes, after the hold was released |
| R2 user first, steerer 409 | yes | yes | yes | yes |
| R3 `escalate_to_user` | yes | yes | yes | yes (#555) |
| R4 hold expiry escalates | yes | yes | yes | yes (#555) |
| Follow-up task added | yes | yes | yes | yes |
| Final digest, steerer auto-completed | yes | yes | yes | yes |

Gaps found and fixed with it: a Claude background session listed
`working` with `status: idle` after its turn read running, so a Claude
steerer never received digests ([Claude Code adapter](#claude-code-adapter),
ADR 0005 decision 4); a Codex launch read the first turn before Codex could
list it and reported the launch failed ([Codex adapter](#codex-adapter));
a steerer's tree notes (`sourceTaskId` set to the steerer) were refused as
not a member; the launch brief now says to percent-encode request IDs; and
`stage tree steer` takes `--execution-config`. A Codex steerer read
`interrupted` for a moment after each digest was sent, which released its
tree's holds as `steerer_inactive`; Observe now waits that reading out
([Codex adapter](#codex-adapter)), and a rerun (#555, one Codex-steered
tree with Codex workers for R3 and R4) escalated both holds and
auto-completed the steerer after the final digest. Workers
on a harness without respond escalate at once; every harness here offers
respond, so that path is covered by the `notify` hold tests only.

To repeat it: start a dev server on a scratch data dir, connect each
harness, add a throwaway repository as a project on each, then create the
trees through the HTTP API acting as the user (no agent session variables
in the environment). Tell steerers in their prompt to use only that
server's HTTP API, since other Stage servers on the machine have colliding
task IDs. Remove the harness sessions, worktrees, projects and trust
entries afterwards with each harness's own tools.
