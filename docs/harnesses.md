# Adding a harness

[ADR 0007](adr/0007-harness-boundary.md) sets ownership. [ADR 0008](adr/0008-harness-foundation.md)
defines the target for this series. The numbered layers below identify
upcoming contracts. The OpenCode setup walkthrough uses the shipped API.

## Module contract

At the baseline, the [descriptor](../server/internal/harness/harness.go)
defines the harness kind, name and policies. An
[adapter](../server/internal/execution/types.go) translates harness calls
into Stage's execution contract. A
[connector](../server/internal/harness/connector.go) handles per-machine
settings, setup actions, status and disconnect. The adapter launches with
durable checkpoint and recovery semantics, reports session observations
and replies, and supplies workspace inventory. The descriptor tells
Stage how to attribute agent calls to a session.
Its features report available operations and concrete reasons for
unavailability. See [execution](execution.md) for the shipped contract.

Each harness supplies a [module](../server/internal/harness/module.go)
with its descriptor and `Compose` function. The
[registry](../server/internal/harnesses/modules.go) names each module once
for serve and the worker. Keep typed settings, validation, client
construction and secret keys in the setup package. The shared store
persists public JSON and opaque secrets by machine and kind. Workers
keep their secrets in their own credential file. A module declares stable
identity, continuity proof or moved-retire policy as applicable. See
[ADR 0008 D5–D6](adr/0008-harness-foundation.md#d5) and
[worker setup](execution.md#remote-machines).

The worker handshake advertises protocol 2, supported kinds and traits,
and generic settings and status calls.
Layer 05 declares process requirements separately from machine or cloud
session locality, and supplies status phase, reason and fix. Layers 09
and 10 separate command, observation and discovery roles, then move
observation beside the adapter. Serve hosts the local observer and the
worker hosts its own observer. Layer 10 adds cached public settings to the worker. The
main server owns task state, acceptance and the database in both cases.

When the harness wraps another harness, declare its origin markers and
caller attribution. Stage uses those declarations to prevent duplicate
tracking and to attribute calls to the wrapper. Use the connection plus
external session ID for all calls. Report workspace evidence using the
[discovery contract](discovery.md). Filesystem evidence belongs in
machine operations, and session worktrees belong to the harness.

## Choose an extension

Use [ADR 0008 P4](adr/0008-harness-foundation.md#p4) to decide whether
Stage needs to understand the capability. Use the declared capability
at every consumer of a Stage-wide operation.

| Extension | Use it for | Contract |
| --- | --- | --- |
| Descriptor policy | A rule Stage interprets, such as reserved or correlated launch, caller attribution, wrapper origins or completion signals. | Shipped descriptor. Layer 02 adds link schemes and provenance. Layer 04 adds identity policy. Layer 05 adds process placement, locality and state reasons. |
| Typed operation | Stage behavior gated by capability, such as launch, stop, adopt, reply, opening, attention, images, respond or send. | Shipped `execution.Capabilities`. Layer 06 applies the declaration to UI controls. |
| Launch option | A typed choice, boolean or text input the harness takes during launch, such as its worktree option. | Shipped `execution.Config` and capability options. The adapter validates and translates the value. |
| Action | A harness-specific operation on a connection, session or project, such as labelling a session. | Layer 05 declares input fields, confirmation and allowed callers. The adapter validates the opaque input. One generic route, MCP tool and CLI command dispatch it. Layer 06 provides the renderer. |
| Fact | A display detail, such as usage or a harness status detail. | Layer 05 carries label, value and tone on status and observations. Layer 06 renders facts in the shared UI. |
| Optional interface | A specialized adapter protocol for a declared policy or typed operation, such as event streaming or interaction. | Shipped `execution.As[T]` reaches the interface through local and proxy placements. Layer 03 negotiates the trait. |

An agent may invoke a declared action only when its declaration allows
agents. A capability that belongs to one workspace can be exposed in that
scope. Stage's generic consumers relay the fields and results. The exact
action wire shape belongs to layer 05.

### Walkthrough: Connect local OpenCode

1. The [OpenCode descriptor](../server/internal/opencode/descriptor.go)
   declares `connect-local`, its label and the `newInstance` boolean.
   The connector's status supplies current availability and a reason.
2. The [connector](../server/internal/opencodesetup/connector.go) decodes
   the input, then the setup service reads the local OpenCode service
   registration on the selected machine. Its [module](../server/internal/opencodesetup/module.go)
   composes that connector in both placements. The request goes through
   the worker proxy when the connector runs on a worker.
3. [The handler](../server/internal/api/handlers_harnesses.go) validates
   that the descriptor declares the action, resolves that machine's
   connector and returns refreshed status. The shipped route is
   `POST /api/harnesses/{kind}/actions/{action}`, with a machine-scoped
   form under `/api/machines/{id}/harnesses/`.
4. The custom [OpenCode panel](../packages/app/src/harnesses/opencode/Setup.tsx)
   calls that route through its scoped client. The CLI's
   `stage harness opencode run connect-local` uses the same setup
   contract. The generic panel can render a setup action from its
   declared fields and availability. [Public setup reference source](../apps/website/src/pages/docs/reference/connections.mdx).

This is a shipped connector setup action. Layer 05 owns the target scoped
action API, MCP and CLI contract. Layer 06 owns shared rendering.

## UI slots

The [slot registry](../packages/app/src/harnesses/index.ts) selects a
harness's setup panel, connection row, status reader and query key, live
chip and web-address policy via [`HarnessSlots`](../packages/app/src/harnesses/types.ts).
UI code outside `packages/app/src/harnesses/` consumes the slot,
descriptor and declared capabilities.

- **Generic panel.** A kind without a registered custom entry uses the
  [generic slots](../packages/app/src/harnesses/generic.tsx). They render
  settings and setup actions from the descriptor and status. Layer 06 adds
  shared action and fact presentation with confirmation where declared.
- **Custom panel.** Register one slot entry to keep the harness's
  connection workflow, vocabulary, settings and details. OpenCode's
  setup panel owns its local service and pairing flows. Layer 06 shares
  primitives across the custom and generic panels.
- **Unavailable operation.** A harness without Stop declares that
  operation unavailable with a reason. Layer 06 gates the control
  on availability. The target uses the same declaration for API, MCP
  and CLI dispatch. Today the Stop handler calls the adapter directly.
  The harness keeps its native controls. Start and Track follow the
  same UI pattern for launch and adoption.

## Author checklist

- Implement the harness adapter and setup package. Keep native launch,
  session IDs, workspace creation, archive, models and processes there.
- Declare the module's identity and placement, durable launch policy,
  destinations, caller attribution and any wrapper markers. Verify that
  replacing and reviving instances preserves connection history.
- Add the adapter under `server/internal/<harness>/` and its module, typed
  settings and connector under `server/internal/<harness>setup/`.
  Register the module in [modules.go](../server/internal/harnesses/modules.go).
  Exercise first setup and reconnect through serve and the authenticated
  [worker setup rig](../server/internal/workersetup/workersetup_test.go).
  Test supported and unsupported channel traits.
- If the harness has a custom UI or CLI panel, add the slot under
  `packages/app/src/harnesses/<kind>/` or
  `server/internal/cli/slots_<kind>.go`.
- Report complete inventory and provenance for [read-only discovery](discovery.md).
  Verify incomplete reads and unavailable operations. Exercise launch
  recovery, session observation and attribution with contract tests.
- Declare extras with the smallest relevant extension. Test typed
  operations in each consumer, action inputs and caller authorization,
  and generic or custom UI slot behavior where applicable.
- Document setup, troubleshooting, capability limits and link the tests.
  Re-run the [Go boundary check](../server/internal/harness/boundary_test.go)
  and the [UI boundary check](../packages/app/tests/boundaries.test.mjs).

## Amp mapping

Read from Amp's public docs on **2026-11-26** for [ADR 0008 D13](adr/0008-harness-foundation.md#d13).
Layer 11 rechecks these sources. Verify the mapping when an adapter is
built. Amp is a design case for this series.

| Facility | Documented evidence and Stage mapping |
| --- | --- |
| Account and machine | Amp supports saved CLI accounts and runs a headless runner that serves directories on a particular machine. **Inference:** declare local processes required for the proposed CLI or runner integration, and cloud session locality. Account plus machine is the proposed identity policy. Stable account and runner identifiers and continuity proof are **unverified**. [CLI accounts](https://ampcode.com/docs/cli), [runners](https://ampcode.com/docs/cli/runners), [cloud thread access](https://ampcode.com/docs/threads). |
| Launch and identity | Amp's REST API creates a thread and its initial message for a cloud orb. Local `amp -x --stream-json` emits the `T-<uuid>` session ID in its initial JSON line. The CLI also starts detached runner and orb executions. Streamed execute runs can archive new threads at turn end unless configured otherwise. A launch adapter would capture the ID and report a connection-scoped session. Recovery if launch succeeds before Stage saves the ID is **unverified**. [External API](https://ampcode.com/api/external), [Streaming JSON](https://ampcode.com/docs/cli/streaming-json), [Execute Mode](https://ampcode.com/docs/cli/execute-mode), [Spawning Orbs](https://ampcode.com/docs/cli/spawning-orbs). |
| Observation and reply | The external API lists a user's threads and reads metadata and messages by ID. Metadata reports `working`, `idle`, `awaiting_approval` and `error`. An experimental stream covers a subset of threads for a Sign in with Amp token. **Inference:** use connection-scoped metadata and messages for reconciliation and reply; verify credential scope and state semantics across execution modes before declaring a push trait. [External API](https://ampcode.com/api/external). |
| Approval and input | Default tool execution needs no approval. Plugins can require approval and report that wait. **Unverified:** a general question state or a supervisor API for answering it. Map an observed approval wait through the declared reason and gate in-Stage responses on a verified operation. [Tools](https://ampcode.com/docs/tools), [Plugin API](https://ampcode.com/docs/plugin-api), [External API](https://ampcode.com/api/external). |
| Stop | Execute-mode docs describe exit on a local signal. A remotely callable cancellation operation for a detached runner or orb thread is **unverified**. Gate Stop by verified execution mode. [Execute Mode](https://ampcode.com/docs/cli/execute-mode), [Runners](https://ampcode.com/docs/cli/runners), [External API](https://ampcode.com/api/external). |
| Project evidence | Amp matches projects using Git remote URLs and aliases. Runners can serve selected directories. **Inference:** report the thread's repository evidence for Stage identity when its origin and subpath are known. A complete, authoritative local directory inventory for an external supervisor is **unverified**. [Projects](https://ampcode.com/docs/projects), [Runners](https://ampcode.com/docs/cli/runners). |
| Caller attribution | Streaming JSON and the plugin's invocation API expose a thread ID. Runners can pass configured variables to MCP processes. A built-in per-call MCP field or environment variable carrying the current caller's thread ID is **unverified**. The adapter needs a trusted path to Stage's caller session contract. [Streaming JSON](https://ampcode.com/docs/cli/streaming-json), [Plugin API](https://ampcode.com/docs/plugin-api), [Runners](https://ampcode.com/docs/cli/runners). |
| Extras | Amp documents continue, list, archive, share, label, export and native handoff. Its external API exposes thread usage with credential and retention limits. **Inference:** share, Amp handoff and labels fit declared session actions; usage fits facts. Stage's durable accepted handoff keeps its own contract. [Threads](https://ampcode.com/docs/threads), [External API](https://ampcode.com/api/external). |

The mapping assumes a trusted caller can be attributed to the Stage
connection and Amp thread. Supporting Amp without that attribution would
change the contract. Requiring universal cancellation for detached
execution would also change the declared-operation model above. Verify
both before proposing an adapter.
