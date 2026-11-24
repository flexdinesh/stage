# ADR 0002: Machines and workers

- **Status:** accepted (2026-11-22). The model was approved by the user in Stage
  task #336. This record changes no code. Stage tree 197 (tasks #338 to #349,
  [phased tasks](#phased-tasks)) implements it. Endpoint paths, intervals and
  Go names below are indicative. The task that builds each part fixes them
  and updates the contract docs it owns.
- **Replaces:** the earlier sketch in [execution.md](../execution.md)
  ("Worker-ready placement"), where a worker synced its harnesses upstream.
  This record makes the worker thin: the main server keeps the only database.
- **Amended by:** [ADR 0003](0003-storage-adapters.md) (2026-11-24): the main
  server's database is SQLite or Postgres, one owner per database on either
  engine. "One replica" is unchanged.

## Context

Today one `stage serve` process owns everything: SQLite, harness connections,
adapters, the coordinator, scheduling, acceptance, journals and MCP. Its
adapters reach T3 and OpenCode from that process. Machine-local work
(Git metadata, shared paths, `git rev-parse` matching, OpenCode's
`service.json`, T3 auto-pair) runs on the server's host.

The goal is to run the main server on one host (a cloud VM, a cluster, a home
server) while harnesses run on other machines (laptops, desktops). The web
app and desktop app keep talking to the main server only.

Groundwork that already exists:
- `machines` table (migration 0015), `db.LocalMachine = 1`. Every harness
  connection has a `machine_id`. The active and identity indexes are per
  machine.
- "Connection" already means one harness instance
  ([architecture.md](../architecture.md#project-and-session-identity)).
- Execution's worker-ready rules: narrow ports, JSON-serializable contract
  types, pushed observations ([execution.md](../execution.md#design-rules)).
- Stage has no authentication. `guard.Policy` is request-origin hygiene. Remote
  use sits behind an authenticating HTTPS reverse proxy
  ([runtime.md](../runtime.md#request-origin-policy)).

## Terms

| Term | Meaning |
| --- | --- |
| **Machine** | A host that runs harnesses. A row in `machines`. Data model and UI name. Machine 1 is the local machine, in the main server's process. |
| **Main server** | `stage serve`. It owns the database and all business rules. |
| **Worker** | The process role on a remote machine: `stage worker join\|run\|status\|leave`. One worker serves one machine. |
| **Harness connection** | Unchanged: one harness instance (a T3 environment, an OpenCode service). It belongs to one machine. |

Hierarchy: User -> Machines -> Harness connections -> Sessions.

## Decision

### 1. Embedded by default

A single install (desktop app, per-user service, compose) is a main server
plus the in-process local machine, in one binary, with no behavior change. A
remote machine is a second implementation of the same machine-runtime seam
(section 3). Running no worker is the default, and it stays the supported path.

### 2. Thin worker, no database

| Main server owns | Worker owns |
| --- | --- |
| The database (SQLite or Postgres, [ADR 0003](0003-storage-adapters.md)), migrations, the ownership lock | No database. A config file and its machine token (0600) in the worker data dir |
| Coordinator, polling schedule, release and schedule runners | Real harness adapters (T3, OpenCode) for that machine's connections |
| Acceptance, journals, handoff, notifications | Connectors (setup: pair, auto-pair, connect-local, disconnect) |
| MCP and REST API, the UI | Machine-local fs and process work: gitmeta inventory, shared-path translation, `git rev-parse` matching, OpenCode `service.json`, T3 auto-pair |
| Connection identity, status and non-secret settings | Harness secrets: T3 token, OpenCode password |
| Launch checkpoints (durable) | Nothing durable about launches. `SaveCheckpoint` calls back to the main server before each external effect |

The main server drives a remote machine through a remote adapter proxy that
implements `execution.Adapter` (plus `LinkResolver`, `EventSource` and the
other optional interfaces). Because of this, the coordinator, sync, the
runners and the contract suite do not know whether a machine is local.

### 3. Machine-runtime seam

There is one per-machine host for adapters, connectors and machine-local ops.
Sync, the runners, the API and connection setup reach harness code only
through it. Sketch (#340 fixes the names):

```go
// Machine hosts one machine's harness code.
type Machine interface {
	ID() int64
	Online() bool
	Adapter(connectionID int64) (execution.Adapter, bool)
	Connector(kind string) (harness.Connector, bool)
	Ops() MachineOps
}

// MachineOps is the fs/process work that must run where the harness runs.
type MachineOps interface {
	Inventory(ctx context.Context, ws []execution.WorkspaceInfo, shared []string) ([]execution.WorkspaceInfo, error)
	MatchWorkspace(ctx context.Context, path string, shared []string) (GitFamily, error)
}
```

- **LocalMachine** wraps today's in-process code. Embedded installs use only
  this one.
- **RemoteMachine** forwards every call over the worker channel (section 4).
  The worker builds the real adapters, connectors and ops through the same
  constructors LocalMachine uses.
- **Ports, never `*db.Store`.** Adapters and setup take a settings store, a
  credential store, a checkpoint save, an event sink and an
  identity/switch-instance callback. On the local machine these are
  store-backed. On a worker, settings, checkpoint, event and identity calls
  become RPCs to the main server, and credentials stay in a worker-local file.
- Every port and contract type is JSON-serializable, and a test round-trips
  them.

### 4. Transport and RPC channel

The worker always dials out to the main server's public URL. The main server
never connects to a worker. The transport uses only the standard library
(`net/http`, `encoding/json`, hand-written SSE framing).

| Direction | Carrier | Carries |
| --- | --- | --- |
| Main -> worker | `GET /api/worker/channel` (SSE, one long-lived stream) | RPC requests, `cancel`, keepalive comments |
| Worker -> main | `POST /api/worker/channel` (one POST per message) | RPC results, pushed events (observations, connection status), heartbeat, and worker-initiated calls (`checkpoint.save`, settings, identity). Each worker-initiated call gets its reply in that POST's response |

Envelope (both directions):

```json
{
  "id": "c7:r-81f2",
  "kind": "adapter.launch",
  "deadline": "2026-11-22T10:00:05Z",
  "connectionId": 7,
  "body": {}
}
```

A result carries the request's `id` and either `body` or
`error: {code, message}`. Error codes map to the existing typed errors
(`Rejected`, `operation_unavailable`, offline, timeout). The rules:

- **id.** The sender generates it. It is unique within one channel session
  (prefix). Replies match by id. A duplicate or unknown reply is ignored.
- **deadline.** An absolute UTC time. The worker runs the call under a context
  with that deadline. The main server stops waiting at the deadline and treats
  it as a **lost reply** (unknown outcome, never failure). A reply that
  arrives later is dropped.
- **cancel.** When the caller's context ends, the main server sends
  `{"kind":"cancel","id":...}`. The worker cancels that call's context. This
  is best effort; reconciliation covers a call that ran anyway.
- **heartbeat.** The worker POSTs a heartbeat about every 15s. The main server
  sends an SSE comment about every 15s, so a dead stream is detected on both
  sides. After about 45s of silence the machine is offline. `lastSeenAt`
  advances with every message.
- **Channel session.** Each SSE connect gets a session id, and up POSTs carry
  it. A newer stream from the same machine replaces the older one (one live
  channel per machine). POSTs that name a replaced session get 409.
- **reconnect.** The worker retries with exponential backoff and jitter (1s to
  60s). When a stream ends, the main server resolves that session's in-flight
  calls as lost replies. Every operation is a read or reconcilable on replay
  (Launch replays from its checkpoint), so the caller's normal retry is safe.
- **version check.** The worker sends `Stage-Api-Version` and its build version
  when it connects. The worker and main server must speak the same API version
  (`buildinfo.APIVersion`). If they differ, the connect is refused with 409
  `api_version_mismatch`, and the machine shows "needs update" in the UI. The
  worker reports it and backs off at the maximum interval instead of
  retrying quickly. A different build version only shows in the UI, as it
  does for the desktop app (ADR 0001).
- **Ordering and size.** Separate requests have no ordering guarantee. Message
  bodies are bounded by the API's existing upload cap.

### 5. Security model

This is Stage's first authentication. It covers worker endpoints only.
Browser and API access stays behind the authenticating proxy.

- **Pairing code.** Settings > Machines > Add (or `stage machine pair`) creates
  a one-time code: random, single use, 10 minute TTL, stored hashed. The UI
  shows it as a link based on `STAGE_PUBLIC_URL` and as the command
  `stage worker join <link>`. Creating a code needs ordinary API access, so it
  sits behind the proxy.
- **Redeem.** `stage worker join` POSTs the code to the redeem endpoint
  (`/api/worker/join`). The main server creates the machine row
  (`created_by_user_id` = the user who created the code) and returns the
  machine id plus a **machine token**.
- **Plain HTTP.** `stage worker join` accepts an `http://` link without a
  warning (#343): LAN and loopback mains are common, and a public main sits
  behind a TLS proxy whose `STAGE_PUBLIC_URL` is `https://` anyway.
- **Machine token.** Random (at least 256 bits), one per machine, stored hashed
  on the main server and compared in constant time. On the worker it is stored
  0600 in the worker data dir, per identity. It is a **machine principal**, not
  a user.
- **Scope.** The token is accepted only as `Authorization: Bearer` on
  `/api/worker/*`, and never on any other route. Worker routes accept nothing
  else: the redeem endpoint takes only a code.
- **Revocation.** Removing a machine deletes its token hashes and closes its
  open channel at once. Its connections retire as history, under the same
  rules as replaced instances. The worker sees 401, stops, and
  `stage worker status` reports it as revoked, with no retry loop. The local
  machine (id 1) has no token and cannot be removed.
- **What bypasses the auth proxy.** Only `/api/worker/*`: redeem, the channel,
  and the agent gateway's forwarding route (section 8). The proxy must pass
  these routes through without user auth, because the code or token is the
  auth. The UI, `/api` and `/mcp` stay behind the proxy. Worker routes still
  pass through `guard.Policy`. The worker's requests carry the public Host and
  no `Origin`, so they pass, and bearer auth runs on top. #341 defines the
  exact interaction and documents it in [runtime.md](../runtime.md).
- **Harness secrets stay on the worker.** The main server stores connection
  identity, status and non-secret settings only. Checkpoints already carry
  only references and non-secret identity
  ([execution.md](../execution.md#contracts-for-roadmap-tasks-5-and-6)), so
  storing them on the main server is safe. #345 implements it: the worker
  runs its machine's connection setup and keeps the T3 token and OpenCode
  credentials in `credentials.json` (0600) in the worker dir; the main
  server forwards setup calls over the channel
  ([execution.md](../execution.md#remote-machines)). Per-machine setup
  changes are refused through the agent gateway.
- **Gateway trust.** A machine token allows agent-level API access through the
  gateway. This matches today's rule that any local process that can reach a
  listener has full access. The gateway listens on the identity's private
  Unix socket by default (0600), and TCP is opt-in.

### 6. Identity

- `users` is seeded with the default user (id 1). `identities(user_id,
  provider, subject)` stays empty and is kept for future sign-in.
  `GET /api/me` returns the current user.
- `created_by_user_id` is added on machines, tasks, trees, journal entries,
  schedules and projects. It always resolves through one resolver (today: the
  default user; later: the request principal), never a hard-coded literal.
- Machine tokens identify machines, not users.

### 7. Projects across machines

- Projects span machines through bindings. There is no `projects.machine_id`.
- Task create, edit and schedule forms pick **Machine > Harness** from the
  project's bindings. The UX matches today's harness pick, grouped by machine.
  The machine level is hidden when only one machine exists.
- Cross-machine matching uses the normalized Git remote
  (`gitmeta.RemoteIdentity`) plus the subproject's path relative to the repo,
  never the absolute path. Path rules stay within one machine. When several
  projects match, the match is ambiguous and needs a manual bind. With no
  remote, there is no cross-machine merge. #346 rewrote the architecture rule
  that repository metadata is "never identity": the remote is a
  cross-machine match key, and it is never proof within one machine
  ([architecture.md](../architecture.md#project-import)). A matched project
  already bound on the importing machine at another path is also left for a
  manual bind.

### 8. MCP and the agent gateway

- There is one MCP and API implementation, on the main server.
- The worker serves a local **agent gateway**. By default it listens on the
  identity's default Unix socket (TCP is opt-in) and forwards `/api` and
  `/mcp` to the main server with the machine token. As a result, agents on that
  machine use `stage mcp`, the CLI and HTTP MCP unchanged: client resolution
  already finds the default socket. Caller attribution
  (`Stage-Caller-Session`, `STAGE_CALLER_SESSION`) passes through.
- Launch instructions for sessions on a remote machine point at the gateway,
  not at `STAGE_PUBLIC_URL`.
- #347 fixed the names: the forwarding route is
  `/api/worker/gateway/{api/...|mcp}` (machine management refused with 403
  `gateway_forbidden`), the listen list is `stage worker run --listen` or
  `STAGE_WORKER_LISTEN` (default and required item `unix`), and the worker
  reports its gateway URLs in the channel's `Stage-Worker-Gateway` header
  ([runtime.md](../runtime.md#agent-gateway),
  [api.md](../api.md#apiworkergatewayapi-and-apiworkergatewaymcp)).

### 9. Failure semantics

**An offline worker is an offline harness.** It never implies failure or
completion.

| Situation | Behavior |
| --- | --- |
| Worker offline (no channel, missed heartbeats) | Its connections report `connected: false`. `/api/health` stays 200. Polls skip. Task states, sessions and acceptance are unchanged. |
| Launch while the machine is offline | The remote adapter returns an offline error, never `Rejected`. The attempt stays **Launch unconfirmed**, and recovery retries until the worker is back. |
| Lost reply after dispatch (deadline, stream drop, worker or main restart) | The attempt stays **Launch unconfirmed**. Recovery retries with the existing backoff (5s to 1 min). The replay reconciles from the checkpoint ([execution.md](../execution.md#durable-launch-lifecycle)). |
| `checkpoint.save` cannot reach the main server | The adapter stops before the external effect, as the Launch contract already requires. |
| `Rejected` | Only when the worker's adapter certifies that no execution started. Never because the worker or harness is offline, timed out, or returned malformed data. |
| API version mismatch | The machine shows "needs update", and its connections are offline until the versions match. |
| Token revoked | The worker stops (section 5). The machine's connections are retired. |

### 10. User decisions recorded

1. **The worker runs under the per-user service.** `stage service` gains a
   worker mode: a managed `stage worker run` with its own unit or label per
   identity, and `service.env` holding the worker config path. It follows
   [service.md](../service.md). Tests use only the isolated `stage-svctest`
   identity, never the stable `stage` one (#343). Under one identity, a
   machine runs either the main server service or the worker service, not
   both: the gateway's default socket would collide with the main server's.
   #348 (desktop join as worker) kept this rule: joining from the desktop
   uninstalls this machine's server service (its data stays) and starts the
   worker service; leaving reverses it
   ([desktop.md](../desktop.md#worker)). So agents' default `stage` and
   `stage mcp` reach the main server through the gateway.
2. **The main server records the forwarding machine.** For requests that
   arrive through a worker's agent gateway, the main server resolves the
   machine principal from the token. It records that machine on writes
   (journal entries, created tasks, adopted sessions), next to the caller
   session attribution and the default user, and exposes it on the API shapes
   (#347: `source_machine_id` / `sourceMachineId` on projects, tasks, trees,
   journal entries and schedules, [architecture.md](../architecture.md#users)).

## Rejected alternatives

| Alternative | Why rejected |
| --- | --- |
| Worker with its own database, synced to the main server | Two sources of truth for tasks, acceptance and journals, conflict resolution, and migrations on every laptop. It breaks the rule that one owner holds the database (the data-dir lock on SQLite, an advisory lock on Postgres: [ADR 0003](0003-storage-adapters.md)). A thin worker keeps every business rule in one place. |
| Name "connection" for the remote host | It already means one harness instance. Reusing it would blur Machine -> Connection. |
| Name "node" | Generic, and it clashes with Kubernetes nodes in the deployment docs (#349). |
| Name "runner" | It clashes with the release and schedule runners and with CI runner semantics. |
| Projects owned by one machine (`projects.machine_id`) | The same repo on a laptop and a desktop would become two projects, splitting tasks, journals and trees. Bindings already model per-connection placement. |
| WebSocket | It needs a dependency or hand-written framing, plus upgrade support in proxies and ingress. SSE plus POST is standard library only, passes HTTP/1.1 proxies and existing auth proxies, and the server already streams SSE (notification inbox). |
| Main server dials the worker | Laptops sit behind NAT and firewalls, change addresses and sleep. It would need inbound ports and a listener on the worker that accepts remote calls. Dialing out needs only the main server's public URL. |

## Current single-machine assumptions

Found in #336. Each one is fixed by the task named here.

| Assumption | Where | Fixed in |
| --- | --- | --- |
| `activeT3SQL` selects the active T3 connection with no machine: `harness='t3' AND active=1` | `server/internal/db/connections.go`, used in `push.go`, `notifications.go`, `stats.go`, `threads.go`, `tasks.go`, `connection.go` | #339 |
| `t3_connection` and `opencode_settings` are `id = 1` singletons | `server/internal/db/connection.go`, `server/internal/db/opencode.go` | #339 (keyed by connection or machine) |
| `SwitchInstance` writes only `LocalMachine` | `server/internal/db/connections.go` | #339 (takes a machine id) |
| One release and one schedule runner per harness kind | `server/internal/serve/serve.go` ("One runner per harness kind") | #339 (per active connection) |
| Sync treats T3 as global (`IsT3`, `T3ID`) | `server/internal/sync/watch.go`, `session.go`, `service.go` | #339 |
| `sync.Service` and `opencodesetup.New` take `*db.Store`. `t3setup`'s store is `db.Store` | `server/internal/sync/service.go`, `server/internal/opencodesetup/setup.go`, `server/internal/t3setup/t3setup.go` | #340: setups take ports (`opencodesetup.Ports`, `t3setup` Settings/Identity/Local), `machine.StorePorts` backs them locally; sync reaches adapters through `machine.Set` and keeps the store as the coordinator |
| Server-host fs and process work: gitmeta `.git/config` reads, shared-path translation (`sync.Service.Inventory`), `git rev-parse` matching, OpenCode `service.json`, T3 auto-pair | `server/internal/sync/inventory.go`, `server/internal/api/matching.go`, `server/internal/opencode/pairing.go`, T3 setup | #340: `machine.Ops` (Inventory, Checkout), `opencodesetup.Local`, `t3setup.Local`; #344 (runs on the worker) |
| The harness profile is read from the local machine only | `server/internal/api/handlers_harnesses.go` (`GetMachine(LocalMachine)`) | #340: read from the local machine's host (`Machine.Profile`); #344 (per machine) |

## Phased tasks

Stage tree 197 (auto start). Each task ships as its own PR.

| Phase | Task | Scope | Needs |
| --- | --- | --- | --- |
| 0 | #337 | This ADR | #336 |
| 1 | #338 | Default user, identities, `created_by_user_id`, `GET /api/me` | #337 |
| 1 | #339 | Machine-scope harness connection singletons (no behavior change) | #337 |
| 2 | #340 | Machine-runtime seam, LocalMachine, narrow ports, machine-local ops | #339 |
| 2 | #341 | Machines API, pairing codes, machine tokens, `stage machine` CLI | #338, #339 |
| 2 | #346 | Cross-machine project matching by Git remote | #339 |
| 3 | #342 | Settings > Machines UI, Machine > Harness picker | #341 |
| 3 | #343 | `stage worker` CLI, channel skeleton (ping), `stage service` worker mode | #341 |
| 4 | #344 | Remote adapter over the channel (RemoteMachine) | #340, #343 |
| 4 | #347 | Worker agent gateway for local MCP and CLI; forwarding machine recorded | #343 |
| 5 | #345 | Remote harness setup with worker-held secrets | #342, #344 |
| 6 | #348 | Desktop: join this machine as a worker | #345, #347 |
| 6 | #349 | Deploy docs and Kubernetes example for main + workers | #345, #347 |

## Consequences

- An embedded install is unchanged. Phases 1 and 2 are refactors with no
  behavior change.
- Stage gains its first authentication, scoped to `/api/worker/*`. Proxy
  deployments must let that prefix through
  ([runtime.md](../runtime.md#remote-main-server-and-workers), #349).
- A machine token is a credential with agent-level access through the
  gateway. Removing the machine revokes it.
- The main server stays a single replica: one owner per database (SQLite on
  a local volume under the data-dir lock, or Postgres under an advisory lock;
  [ADR 0003](0003-storage-adapters.md)).
- Remote launches add one round trip per checkpoint. Lost replies fall back
  to the existing unconfirmed-launch recovery instead of new states.

## Open items

- ~~Running both the main server and a worker under one identity on one
  machine~~: kept as one role per identity in #348; the desktop switches
  roles on join and leave ([desktop.md](../desktop.md#worker)).
- ~~Where a remote machine's harness setup runs and its secrets live~~:
  resolved in #345. On the worker, composed from the same setup packages;
  the main server proxies the connector (`/api/machines/{id}/harnesses`)
  and serves the non-secret record back over `setup.*` calls.
- ~~How launch image bytes reach the worker~~: resolved in #344. The launch
  call carries image metadata only; the worker fetches each image with a
  `launch.image` callback while the launch waits
  ([execution.md](../execution.md#remote-machines)).
