# Stage execution boundary

Stage owns task identity, dependencies, acceptance, journals and durable handoff.
`internal/execution` describes the operations Stage retains. `internal/sync`
coordinates those operations; `internal/t3.Adapter` translates T3 protocol,
authentication, models, permission modes, workspace strategies and states.
Composition is a fixed connection-to-adapter map in the server. There is no
plugin registration or service installation framework.

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
- `Observation` is a validated snapshot: session/project, normalized state,
  activity, top-level/archive eligibility, workspace, links, bounded reply and
  available artifact links. States are running, needs_input, done, failed,
  interrupted, cancelled or idle. Idle does not overwrite a known task state.
  Unknown/malformed snapshots and missing/offline sessions never imply execution
  failure or completion. Connection health is independent.
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
  offline. Only user completion in Stage releases dependencies.
- Hosts choose native versus web destinations through
  `Links.executionDestination`. Product code does not inspect browser location
  or dispatch native URLs directly.

Task 5 can implement an OpenCode adapter against these contracts. Task 6 can
consume connection capabilities, scoped observations and neutral configuration
without reading T3 transport types. Neither task should add global conversation
browsing, transcripts, follow-ups, question answering or detailed run panels.
OpenCode remains storage-only in this change.

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
does not accept a task or hide active work.

Contract tests use a second fake harness with document IDs, a non-T3 model,
two-step creation, optional capabilities, lost replies, restart, offline reads,
malformed states and offline acceptance. T3 tests cover namespace rotation and
checkpoint failures. API tests cover compatibility, frozen handoff/journals,
explicit base repair and link-preview-attach ownership/revision checks.
These are isolated tests, not live validation of a T3 or OpenCode service.
