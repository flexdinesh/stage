# stage REST API

Base path `/api`. JSON in and out (`Content-Type: application/json`). Field names camelCase. TypeScript mirror: `packages/app/src/api/types.ts` (change both together).

## Conventions

- Timestamps: ISO 8601 strings (UTC, e.g. `"2026-11-08T10:00:00Z"`). Nullable fields are `null`, never omitted.
- IDs: app ids (`id`, `projectId`, `taskId`, `treeId`, `needs`) are JSON numbers. T3 ids (`externalProjectId`, `externalSessionId`) are strings.
- `createdBy`: the user id (number) of the row's creator on `Project`, `Task`, `Tree`, `JournalEntry`, `Schedule`, `Machine` and the `GET /api/harnesses` machine. Always emitted; see [Users](#users).
- `sourceMachineId`: the machine whose worker agent gateway forwarded the create, on `Project`, `Task`, `Tree`, `JournalEntry` and `Schedule`; `null` when not through a gateway. Always emitted; see [Users](#users).
- Resource lists are normally wrapped, for example `{ "projects": [...] }`. Action discovery returns an array.
- PATCH bodies are partial: omitted fields are unchanged; `null` clears a nullable field.
- Repeatable list query params accept repetition or commas: `?state=running&state=needs_input` == `?state=running,needs_input`.
- `from` / `to` query params: ISO date (`2026-11-01`) or datetime. `from` inclusive, `to` exclusive. Filter on `createdAt` unless stated.
- Substring filters (`q`) match case-insensitively, non-ASCII letters included; every character is literal (`%`, `_` are not wildcards). Folding of rare Unicode characters may differ between the SQLite and Postgres engines.
- Errors: non-2xx responses have body `{ "error": string }`. Some 409s and the worker routes' 401s add a machine-readable `"code"`.
- Every API response carries `Stage-Api-Version: <n>` (currently `9`). See [API v9 changes](#api-v9-changes) for the coordinated rollout. The Go client (CLI, stdio MCP) refuses a response with a different value; a missing header is accepted.
- Request origin policy (`/api` and `/mcp`; [runtime.md](runtime.md#request-origin-policy)): requests with a Host that is not allowed, cross-site requests and untrusted cross-origin writes get 403; a non-empty write body that is not `application/json` (or `multipart/form-data` for uploads) gets 415.

| Status | Meaning |
|---|---|
| 400 | invalid body or query (bad enum, missing required field, bad JSON) |
| 401 | missing, expired, invalid or revoked credential on a protected route, invalid pairing code, or a machine token outside `/api/worker/*` |
| 403 | rejected by the request origin policy, insufficient principal scope, unavailable grant, or setup pending |
| 404 | resource not found |
| 409 | conflict: action not allowed in current state, duplicate unique value, T3 setup refused (other T3 environment, auto-pair unavailable) |
| 415 | write body not `application/json` / `multipart/form-data` |
| 502 | harness call failed, an action outcome is uncertain, or the harness does not list an added workspace (`workspace_not_listed`) |
| 504 | the harness did not confirm an add-workspace outcome (`outcome_uncertain`); retry with the same `requestId` |
| 503 | T3 not connected (no valid token) or DB unavailable |
| 500 | unexpected |

## Enums

| Name | Values |
|---|---|
| TaskState | `backlog` `launching` `running` `needs_input` `done` `failed` `interrupted` `cancelled` (`done` records the finished execution) |
| RuntimeMode | `approval-required` `auto-accept-edits` `auto` `full-access` (task default `full-access`) |
| InteractionMode | `default` `plan` (default `default`) |
| Delivery | `send` `steer` `queue` (default `send`) |
| StartMode | `auto` (a tree member launches once its needs are completed in Stage) `manual` (it reads `ready` and notifies; the user starts it). New tree default `auto`; a task's own `startMode` null inherits the tree's |
| Readiness | `blocked` (a need lacks user acceptance) `ready` (every need has user acceptance, no start time, and manual or no needs); backlog tree members only, else null |
| NotificationEvent | `needs_input` `failed` `ready` `completed` `tree_completed` `schedule_started` `schedule_skipped` `done` (display order; `done` is an untracked session's finished run) |
| ProjectSource | `app` (created via stage) `t3` (imported from T3) |
| T3State | `unreachable` `unpaired` `expired` `rejected` `connected` (see [T3 details](#t3-details)) |

## Shared shapes

```jsonc
// ModelOption: T3 ProviderOptionSelection
{ "id": "effort", "value": "low" }          // value: string | boolean

// ModelOption with a launch option (T3: a new worktree from origin/main)
{ "id": "worktree", "value": true }
{ "id": "baseRef", "value": "origin/main" }
```

Where and how a session starts is a harness launch option in `executionConfig.options` (#420): the connection's capabilities declare them (see [capabilities](#get-apiconnectionsconnectionidcapabilities)) and its adapter translates them. T3: `worktree` (boolean), `baseRef` and `branch` (text, only with `worktree` `true`), sent as T3 `workspaceStrategy` (`root`, or `worktree` with `baseRef`, `branch` and `startFromOrigin` when `baseRef` starts with `origin/` or `upstream/`; T3 requires `baseRef` for a worktree, so a launch without one returns the task to backlog with the reason). Claude Code and OpenCode: `worktree` (the harness picks the base and branch). Codex: none. A dependent never starts from its predecessor's branch; the handoff names the branch the predecessor reported. API v8 removed `workspace` (400, see [API v8 changes](#api-v8-changes)).

## Health

### `GET /api/health`
200 / 503 `Health`:
```json
{
  "status": "ok",
  "db": "ok",
  "service": "stage",
  "identity": "stage",
  "version": "v1.4.0",
  "commit": "4202029f...",
  "apiVersion": 9,
  "instanceId": "9f2c4e1a7b3d5c60",
  "startedAt": "2026-11-16T09:00:00Z",
  "schema": 35,
  "engine": "sqlite",
  "publicUrl": "https://stage.example.ts.net",
  "harnesses": [
    { "kind": "t3", "connectionId": 1, "configured": true, "connected": false, "lastError": "dial tcp 127.0.0.1:3773: connection refused" },
    { "kind": "opencode", "connectionId": 2, "configured": false, "connected": false, "lastError": null }
  ]
}
```
- A live Stage server answers 200 whenever its database works; a harness being offline is still 200. `harnesses` lists each connectable harness in registry order with its connector's last known state from memory: `configured` (setup done: T3 paired, with a stored token or a saved admin token; OpenCode saved settings), `connected` (T3: a valid token T3 has not rejected; OpenCode: an adapter installed without a known problem). Health never calls a harness (reachability and the setup state are in `GET /api/harnesses/{kind}/status`).
- 503: the DB query failed (`status` and `db` are `"error"`, `schema` is `0`; the other fields are still present).
- `service` is always `"stage"`, so a client can tell a Stage server from anything else on an address. `identity` is the installation identity, `version` the build (`"dev"` when unstamped), `commit` may be `""`. `instanceId` and `startedAt` identify the running process (`startedAt` is `null` only outside a real server). `schema` is the database's migration version ([runtime.md](runtime.md#schema-and-backups)). `engine` is the storage engine, `"sqlite"` or `"postgres"` ([storage.md](storage.md#engines-and-selection)); never the database URL. `publicUrl` is `STAGE_PUBLIC_URL` without a trailing slash, `null` when unset; the desktop uses it for copied links. `harnesses[].lastError` is nullable.
- `apiVersion` equals the `Stage-Api-Version` header. See [runtime.md](runtime.md#health-and-version) for readiness and compatibility rules.
- Shared public health limits the body to `status`, `db`, `service`, `version`, `apiVersion`, `sharedAccess`, and `setupPending`. The local service probe on Unix also receives `instanceId`. Active human sessions receive detailed health.

## Users

Personal mode uses the default user (id 1). Internally staged shared mode
authenticates humans and agents, preserves row 1 across claim, and
records known person IDs in existing creator fields. Authorization comes
from the admitted principal. Historical creator fields never confer
permissions. Worker gateways carry machine provenance. Background work runs
with an internal system principal.

### Work attribution

Task events, journal entries, accepted results, launch attempts and
schedule runs expose `actor`. Its `kind` is `human`, `personal`, `agent`,
`machine`, `system`, `cron`, `unknown` or `historical`. Named human
actors have `userId`. Agent credentials may identify their account through
`credentialAccountId`. An agent operation invoked by a signed-in browser
or automation derived from human work may have `initiatorUserId`. The
optional `displayName` is the public name of the direct human or initiator.
Login addresses remain in the caller's account and admin views.

`cause` can link an event to a schedule, prerequisite acceptance, launch
attempt, session or request. `sourceMachineId` records the forwarding
machine or verified observation source when known. The actor, cause and
machine are independent from the task's execution destination and caller
session hints. Personal access identifies the trusted deployment boundary.
Legacy rows without an actor stamp are historical even when their
`createdBy` is 1. Attribution has no effect on authorization.

Schedule-spawned tasks and template notes keep the schedule's creator ID.
Cron is the actor for automatic fires, and Run now records its admitted
requester. Launch confirmations and retries link to the original claim.
Account disable revokes the person's sessions and agent credential.
Existing tasks and schedules remain workspace-owned.

### `GET /api/me`
200 `User`, the acting user:
```json
{ "id": 1, "displayName": "Default user", "createdAt": "2026-11-22T10:00:00Z" }
```

In shared mode it also returns the calling account's `email`, `role`
(`admin` or `member`) and `status`. It requires a browser or CLI session.

## Shared access transport

Enable shared access per installation with `STAGE_MULTI_USER=1` or
`stage serve --multi-user`. See [startup and recovery](runtime.md#shared-setup-and-owner-recovery)
and the [principal policy](adr/0009-shared-access.md#principals-and-endpoint-policy).
In claimed mode, protected endpoints return `401` for missing or invalid
credentials and `403` when an admitted principal lacks permission. During
first-admin setup they return `403 setup_pending`. Public health, version
and access status remain available for client negotiation. Worker endpoints
authenticate with pairing or machine credentials independently of human sessions.

| Request | Credential and result |
| --- | --- |
| `GET /api/auth/status` | Public `{mode, installationId}`. Mode is `personal`, `setup_pending` or `claimed`. The ID identifies the installation across process restarts. Shared responses include `canonicalUrl`, the configured browser origin. Shared agent sockets also include `agentOnly: true`. |
| `POST /api/auth/login` | Browser JSON `{email,password}`. Exact canonical origin. Returns the account and sets the browser session cookie. |
| `POST /api/auth/cli/login` | Native JSON `{email,password}` without browser origin, cookie or fetch headers, at the canonical target or its product Unix socket. Returns `{token, expiresAt, installationId}` with CLI audience. |
| `POST /api/auth/{setup\|invite\|reset}/inspect` | Exact browser origin and purpose-bound `{token}`. Returns `{email,expiresAt}` without consuming the grant. |
| `POST /api/auth/setup` | Exact browser origin and `{token,email,name,password}`. Claims the first admin and sets a browser session. |
| `POST /api/auth/invite` | Exact browser origin and `{token,name,password}`. Redeems the grant for its fixed email and sets a browser session. |
| `POST /api/auth/reset` | Exact browser origin and `{token,password}`. Returns 204 and revokes that account's sessions. |
| `POST /api/auth/logout` | Revokes the presented human session. Clears the browser cookie when used and returns 204. |
| `PATCH /api/account` | Human session and `{name}`. Returns the updated account. |
| `POST /api/account/password` | Human session and `{currentPassword,password}`. Returns 204 and revokes all of that account's sessions. |
| `POST /api/account/agent-credential` | Human session. Replaces the calling account's 30-day agent credential and returns `{token, expiresAt, installationId}` once. Issued tokens have the `stage_agent_` prefix. |
| `DELETE /api/account/agent-credential` | Human session. Revokes only that account's agent credential and returns 204. |
| `GET /api/admin/accounts`, `GET /api/admin/invitations` | Admin session. Lists newest first with optional `beforeId` and `limit` (maximum 100). |
| `PATCH /api/admin/accounts/{id}/role` | Admin session and `{role}`. Returns 204. |
| `POST /api/admin/accounts/{id}/disable` | Admin session. Revokes existing browser, CLI and agent sessions for that account and returns 204. |
| `POST /api/admin/accounts/{id}/reset` | Admin session. Creates a member reset grant (201). Admin recovery uses local owner control. |
| `POST /api/admin/invitations` | Admin session and `{email}`. Creates an invite grant (201). |
| `POST /api/admin/invitations/{id}/replace`, `DELETE /api/admin/invitations/{id}` | Admin session. Replaces the invite (201) or revokes it (204). |

Grant creation returns `{id,url,expiresAt}` once. The grant token is in the
URL fragment. Send it only in the appropriate inspection or redemption body.
These endpoints and protected shared responses use a no-store policy.
Cookie requests that write, including login and logout, must include the
exact configured browser `Origin`.

The browser checks access status before mounting protected routes. In claimed
mode, `GET /api/me` supplies the current human account and role. The account
screens use `/auth/setup`, `/auth/invite`, and `/auth/reset`. They remove the
fragment from the current history entry, inspect without consuming the grant,
and submit only when the user confirms the form. Successful setup and invite
acceptance enter the dashboard. Reset returns to sign-in. Ordinary sign-in
restores the requested same-server route.

Settings → Account manages the signed-in account and its scoped agent
credential. Admin → People and Invitations manages access for the whole
installation. Grant URLs and agent credentials are held by the issuing screen
until a successful copy, dismissal, or navigation. Tabs signal session changes
without sharing credentials. A session change discards its work cache, streams,
private view state, and form values before work can mount under another account.
Personal mode uses the default user and opens the dashboard or requested route.

CLI bearer credentials authorize human REST. Agent bearer credentials
authorize scoped REST and HTTP MCP, independent of any caller-session hint.
A signed-in browser with the canonical origin can invoke MCP with agent
authority. MCP reuses the same policy on each tool's in-process REST
requests. Shared product Unix and TCP listeners require a credential.
The separate owner-private agent socket and the private worker gateway
admit the agent principal through local OS access. The private agent socket
serves scoped API and MCP calls without a bearer. The owner-control socket
serves setup reissue and admin recovery separately.

Clients bind stored credentials to the installation ID and exact configured
target (scheme, host and port, or the Unix socket path). The CLI verifies the
installation on the connection it uses for the credential request and
declines redirects. CLI sessions and agent credentials have separate
storage and use. Desktop captures the browser session in Go and verifies
the canonical origin before sending a password or session, including over
the local product socket. [Desktop shared access](desktop.md#shared-access).

Unauthenticated product clients receive
`401 authentication_required`. Valid credentials with an incompatible
scope receive `403 principal_forbidden`. Pending setup returns
`403 setup_pending`. Shared inbox, push and SSE use the signed-in
account's [notification state](#notifications-inbox). Web Push subscription
management requires a browser session.

## Machines

A machine runs harnesses ([ADR 0002](adr/0002-machines-and-workers.md)). Machine 1 is the local machine, inside this server's process; its default label names where the server runs (the hostname, or the `STAGE_PUBLIC_URL` host in a container, else `Stage server`; [runtime.md](runtime.md#machine-profile-and-hosted-harnesses)), set once and kept across renames. A remote machine runs `stage worker` and is added by pairing. Hierarchy: user, machines, harness connections, sessions.

```jsonc
// Machine
{
  "id": 2,
  "label": "work laptop",
  "kind": "remote",              // local (id 1) | remote
  "online": false,               // local: always true; remote: live, ready worker channel
  "status": "offline",           // online | offline | negotiating | needs_update
  // statusReason, when present, explains negotiation or identifies which side to update
  "lastSeenAt": "2026-11-22T10:00:00Z",  // null for the local machine; remote: last worker contact (join, channel connect and disconnect, at most once a minute while connected)
  "version": "v1.5.0",           // the build it runs: the server's for local; null when unknown
  "createdBy": 1,                // the user who created its pairing code
  "createdAt": "2026-11-22T10:00:00Z",
  "connections": [{ "id": 7, "harness": "t3", "label": "env a" }]   // its active harness connections
}
```

### `GET /api/machines`
200 `{ "machines": [Machine] }`, machines not removed, by id (local first).

### `GET /api/machines/{id}`
200 `Machine`. 404 unknown or removed.

### `PATCH /api/machines/{id}`
Body (strict) `{ "label": string }`: trimmed, nonblank, at most 100 characters. 200 `Machine`. 400 invalid. 404 unknown or removed.

### `DELETE /api/machines/{id}`
Remove a remote machine. 204. Its token hashes are deleted, so its worker gets 401 on its next request. Its active connections retire as history under the rules of a replaced instance ([architecture.md](architecture.md#project-and-session-identity)): a connection something references (a binding, a session, a task or a schedule) keeps its tasks, sessions and bindings, never polled or launched on (409 `connection_retired`), and can be forgotten (`DELETE /api/connections/{id}`); an unreferenced one is deleted. Unlaunched tasks and schedules stay on the retired connection until moved. The row is kept, hidden, so history keeps its label. 409 `local_machine` for machine 1. 404 unknown or already removed.

### `POST /api/machines/{id}/ping`
A round trip through the machine's worker channel. 200 `{ "machineId": 2, "latencyMs": 3, "version": "v1.5.0" }` (`version`: the build the worker reports). Machine 1 answers at once (`latencyMs` 0, the server's version). 409 `machine_offline` without a live channel, `machine_setup_starting` before ready, or `operation_unavailable` when the worker omits ping; 504 `lost_reply` when no reply came within 10s (the channel dropped or the worker did not answer); 502 with the worker's error `code` when it answered with an error. 404 unknown or removed.

### `POST /api/machines/pairings`
Create a one-time pairing code that adds a machine. Body (strict, may be empty) `{ "label"?: string }` (the new machine's name; default: the name the worker suggests, else `Machine <id>`). 201, `Cache-Control: no-store`:
```json
{
  "id": 12,                      // names the code in GET/DELETE /api/machines/pairings/{id}
  "code": "stage_pc_12_kq3...",
  "label": "desk",
  "expiresAt": "2026-11-22T10:10:00Z",
  "link": "https://stage.example.com/join#stage_pc_12_kq3...",
  "command": "stage worker join https://stage.example.com/join#stage_pc_12_kq3..."
}
```
- The code works once, for 10 minutes. Only its SHA-256 is stored; this response is the only place it appears. The creating user (`GET /api/me`) becomes the machine's `createdBy`.
- `link` is `STAGE_PUBLIC_URL` + `/join#` + code: the server address and the code in one value. The code is in the fragment, which browsers and HTTP clients never send, so it stays out of proxy logs.
- 409 `public_url_required` when `STAGE_PUBLIC_URL` is unset: a worker needs an address it can reach. No code is created.
- Like every route outside `/api/worker/*`, this sits behind the deployment's authenticating proxy.

### `GET /api/machines/pairings/{id}`
Where a pairing code stands, so Settings can show when its worker joins. 200 (never the code):
```jsonc
{ "id": 12, "state": "waiting", "expiresAt": "2026-11-22T10:10:00Z", "machineId": null }
// state: waiting (redeemable) | joined (machineId: the machine it added) | expired
```
404 unknown, revoked, or pruned (codes used or expired over a day ago are deleted when the next code is created).

### `DELETE /api/machines/pairings/{id}`
Revoke a code that was not redeemed: it can no longer add a machine. 204. 404 unknown or already revoked. 409 `pairing_redeemed` when it already added a machine (remove the machine instead). Settings revokes a waiting code when its Add machine drawer closes.

### Worker routes (`/api/worker/*`)

Called by `stage worker` on a remote machine, never by the UI. They are the only routes an authenticating reverse proxy passes through without user auth: the pairing code or machine token is their auth ([runtime.md](runtime.md#worker-routes)). They still pass the request origin policy. Every worker route:

- except the channel GET and POST, requires `Stage-Api-Version` equal to the server's `apiVersion`; missing or different: 409 `{error, code: "api_version_mismatch"}`;
- except join, requires `Authorization: Bearer <machine token>`. Missing: 401 `machine_token_required` with `WWW-Authenticate: Bearer realm="stage-worker"`. Malformed, wrong, or its machine removed: 401 `machine_token_invalid` with `error="invalid_token"`. An unknown worker path needs a token before it gets 404.

A machine token (`stage_mt_<id>_<secret>`, 256 random bits) identifies a machine principal. The server stores its SHA-256 and compares in constant time. Using it on a non-worker `/api` route or `/mcp` gets 401 `machine_token_not_accepted`. Personal mode passes other authorization schemes through for authenticating proxies.

#### `POST /api/worker/join`
Redeem a pairing code. Body (strict) `{ "code": string, "label"?: string, "version"?: string }` (`label`: the worker's suggested name, used when the pairing has none; `version`: its build). No `Authorization` header (400 if sent). 201, `Cache-Control: no-store`:
```json
{ "machineId": 2, "label": "desk", "token": "stage_mt_5_x7m..." }
```
Creates the machine (`lastSeenAt` now, `version` as sent) and its token; the token appears only here. 401 `pairing_code_invalid` when the code is malformed, wrong, already used or expired (indistinguishable; a failed attempt does not use the code up). Concurrent redeems of one code: exactly one succeeds.

#### `GET /api/worker/machine`
The machine the token names: 200 `Machine`. A worker uses it to check its token (401 `machine_token_invalid` means revoked).

#### `POST /api/worker/leave`
The machine removes itself (`stage worker leave`): 204, with the effects of `DELETE /api/machines/{id}` (token revoked, open channel closed, connections retired).

#### `GET /api/worker/channel`
The worker channel's down half is a `text/event-stream` ([runtime.md](runtime.md#worker-channel)). Send inclusive integer bounds in `Stage-Channel-Protocol-Min` and `Stage-Channel-Protocol-Max`, with min at least 1 and max at least min. Invalid bounds return 400 `invalid_channel_protocol`. A disjoint range or a legacy worker with no range returns 409 `channel_protocol_mismatch` after token authentication. `Stage-Version` identifies the worker build. `Stage-Worker-Gateway` lists its agent gateway listener URLs, comma-separated.

- `hello`: `{session, machineId, protocolRange: {min, max}, protocolVersion, kinds, channelKinds, heartbeatSec, timeoutSec}`. The version is the highest shared version. The server advertises `observe.batch` in `channelKinds`.
- `rpc`: an envelope `{ "id", "kind", "deadline"?, "connectionId"?, "call"?, "body"?, "error"? }`. Calls include `ping`, the worker's advertised `adapter.*`, `ops.*` and `connector.*`, and `cancel` for a caller that stopped waiting. [Request bodies and errors](execution.md#remote-machines).
- `: keepalive` comments every 15s.

A newer stream replaces the old session. Removing the machine closes it.

#### `POST /api/worker/channel`
The up half takes one envelope (at most 32 MiB), with `Stage-Channel-Session: <hello.session>`. `ready` carries `body: {kinds, channelKinds, upKinds?, connections: [{connectionId, traits}], details?}`. It publishes or replaces capabilities after composition (204). `heartbeat` (every 15s) and `result` (reply by id) also return 204. A worker-initiated call gets a `result` envelope (200). Launch callbacks name the waiting request in `call`. When it stops waiting they get `call_ended`. A replaced or ended session returns 409 `channel_replaced`.

#### Worker observation

Both directions must be advertised for observation. The worker advertises
`observe.watch` in `ready.channelKinds` and `observe.batch` in
`ready.upKinds`. The main sends `observe.watch`
down, and the worker replies with its producer ID and optional
`releasedCursor`. The authenticated machine determines the scope of
both messages.

This watch follows two sessions on connection 8.

```json
{
  "epoch": "main-epoch",
  "generation": 12,
  "connections": [
    {"id": 8, "attention": true, "sessions": [{"id": "s1"}, {"id": "s2", "stale": true}]}
  ]
}
```

```json
{"producer": "worker-producer", "releasedCursor": 40}
```

Each watch replaces the complete set. An empty `connections` array clears
it. A generation orders snapshots within the server epoch. An identical
generation can be replayed. A session is fast by default, or stale with
`stale: true`. `attention` enables attention discovery for a connection or
marks a session already followed for attention.
The main includes `resample: true` to request an immediate full sample
after a gap, even when the set is unchanged.
The released cursor is the last cursor the worker has durably acknowledged
and removed from the outbox. It excludes records still in a sealed batch.
An absent cursor is zero. When a database restore leaves the main behind
this cursor, it records the missing range, catches up and requests resampling.

The worker POSTs `observe.batch` after attachment without a `call` field.
The batch `cursor` is its highest included record cursor. Cursors are
contiguous within the producer. A record carries the source `observedAt`
and an observation, a status change or a gap. An observation record scopes
the adapter's `Observation` by `connectionId` and `externalSessionId`.
Status records can signal an unavailable read or a stream error. An empty
`streamError` clears the previous error. `attention` marks an attention
sample, and `quiet` suppresses attention notifications during seeding. A gap
carries the count and time range of lost samples.

This batch reports a gap before the next observation.

```json
{
  "producer": "worker-producer",
  "cursor": 42,
  "records": [
    {
      "cursor": 41,
      "observedAt": "2026-11-27T02:31:00Z",
      "gap": {"from": "2026-11-27T02:30:00Z", "to": "2026-11-27T02:31:00Z", "count": 3}
    },
    {
      "cursor": 42,
      "connectionId": 8,
      "externalSessionId": "s1",
      "observedAt": "2026-11-27T02:32:00Z",
      "observation": {
        "session": {"connectionId": 8, "externalSessionId": "s1"},
        "project": "workspace-1",
        "state": "done",
        "active": false,
        "updatedAt": "2026-11-27T02:31:58Z",
        "executionId": "run-1"
      }
    }
  ]
}
```

```json
{"cursor": 42, "resample": true}
```

The acknowledgement returns the durable contiguous cursor, which may cover
only a prefix if a write fails. The worker retries the sealed records until
acknowledged. Stale or permanently rejected records are consumed, including
records for retired connections. The main rejects an unbound or fenced
producer, or a batch that names another machine's connection. A retry after
a lost acknowledgement returns the committed cursor. `resample` reports
that the main has requested a fresh watch sample after a gap.
[Batch and outbox limits](runtime.md#worker-observation-files)

#### `/api/worker/gateway/api/...` and `/api/worker/gateway/mcp`
A worker's agent gateway forwards the local `/api/...` and `/mcp` requests of agents on its machine here ([runtime.md](runtime.md#agent-gateway)), any method, with the machine token and `Stage-Api-Version` (the worker route checks above apply first). The server serves the rest of the path as that same request on its own `/api` or `/mcp`, with the same handlers, bodies, statuses and streams, and with the token's machine as the forwarding machine: writes record it as `sourceMachineId` ([Users](#users)). The `Authorization` header is dropped before the inner request; `Stage-Caller-Session` and body caller fields (`callerSession`, `sourceSession`, `originSession`) pass unchanged.

- Machine management and harness setup changes return 403 `gateway_forbidden`. This covers both the local `/api/harnesses/...` and the per-machine routes. Machine inventory and harness status remain readable.
- Shared gateway requests require a valid private-socket attestation in
  `Stage-Private-Worker-Gateway` along with the machine token. See
  [Agent gateway](runtime.md#agent-gateway).
- `POST /api/connections/:connectionId/observations` pushes only for the forwarding machine's own connections; another machine's is 403 `gateway_forbidden`.
- `/api/worker/*` and any path that is neither `/api/...` nor `/mcp`: 404.

## Harness connections (API v6)

The descriptor's `setup` defines settings `fields`, connection `actions` and a boolean `disconnect`. Settings and actions share the [field schema](#declared-actions-and-facts). A harness can provide its own setup panel. An unknown kind is 404. When the machine disallows the kind, these routes return 409 `harness_unavailable` with the machine's reason. A kind without a connector on the machine also returns 409. Pairing or connecting can create the first connection.

### `GET /api/harnesses/{kind}/status`
200 `HarnessStatus`:
```json
{
  "kind": "t3",
  "name": "T3",
  "connectionId": 1,
  "configured": true,
  "connected": true,
  "lastError": "",
  "state": "connected",
  "phase": "connected",
  "reason": "",
  "fields": [{ "id": "url", "value": "http://127.0.0.1:3773", "set": false, "source": "default" }],
  "actions": [{ "id": "pair", "available": true }, { "id": "auto-pair", "available": false, "reason": "The t3 CLI was not found on this machine." }],
  "facts": [{ "id": "publicUrl", "label": "Browser URL", "value": "http://127.0.0.1:3773", "link": "http://127.0.0.1:3773", "copyable": true }],
  "sync": { "lastError": null, "lastFullSyncAt": "2026-11-08T10:00:00Z", "running": false, "startedAt": null, "projects": 0, "sessions": 0 },
  "details": { "...": "the harness's own setup state, below" }
}
```
- `connectionId`, `configured`, `connected`, `lastError`: as in health, from the same connector (`lastError` and `reason` are omitted when empty). `state` is a harness-defined word (T3: T3State; OpenCode: `unconfigured`, `connected`, `unavailable`, `rejected`; Codex: `connected`, `not_connected`, `signed_out`, `unreachable`, `not_installed`). `reason`: why it is not connected.
- `phase` is `disconnected`, `needs_setup`, `connecting`, `connected`, `degraded` or `unavailable`. Optional `facts` carry display values. Optional `fix` carries `{text, command?}`. Stage displays the fix and offers to copy its command.
- `GET /api/connections/{id}/facts` returns `{facts: []}` or the last saved connected status facts. The read works while the worker is offline and returns 404 for forgotten connections.
- `fields`: each setting's current value (`source`: `saved` or `default`). No environment variable sets a field. A secret's `value` is always `""`; `set` says whether one is stored.
- `actions` reports current setup action availability with a reason and optional error code for an unavailable action.
- `sync`: Stage’s last setup error, last successful full sync and current full sync progress. The error and timestamps can be null.
- `details`: the harness's setup panel state (T3: the T3 details; OpenCode: the OpenCode details; Codex: the Codex details). Generic clients ignore it.
- Refreshes a stale probe first (bounded, T3: 3s; OpenCode health check: 4s; Codex: the daemon CLI's 20s). Codex reuses a probe for 5s; `?refresh=1` forces one.

### `PUT /api/harnesses/{kind}/settings`
Body: one strict JSON object keyed by setup field id; `null` resets a setting to its default. 200 `HarnessStatus` (freshly probed), plus `replaced` (the retired connection, as in `GET /api/connections`) when the change retired the active connection. 400 invalid or unknown field. 409 the harness's own conflicts (below).

### `POST /api/harnesses/{kind}/actions/{action}`
Use this route for setup before a connection ID exists. The request body is the action input object. An empty body means `{}`. Success returns the existing `HarnessStatus` with the action message, optional link and the replaced connection record when applicable. For an existing connection, the scoped route below returns the action result directly.

### `DELETE /api/harnesses/{kind}/connection`
Forget the connection's credentials; the connection keeps its identity, tasks and history. 200 `HarnessStatus`. 409 when the descriptor's `setup.disconnect` is false.

### Per machine (`/api/machines/{id}/harnesses...`)
`GET /api/machines/{id}/harnesses`, `GET .../{kind}/status`, `PUT .../{kind}/settings`, `POST .../{kind}/actions/{action}` and `DELETE .../{kind}/connection` are the routes above for machine `{id}`; `/api/harnesses...` is machine 1 (this server). Unknown or removed machine: 404. A remote machine's setup runs on its worker ([execution.md](execution.md#remote-machines)): credentials stay there. When its worker cannot answer: 503 `{error, code}` with `machine_offline` (not connected, or no reply in time), `machine_needs_update` (another API version, or a worker without this harness's setup) or `machine_setup_starting` (connected, still composing its setup; retry). Health and `GET /api/harnesses` report an offline remote connection as not connected with the offline reason as `lastError`. A remote machine's connection is created by its first pairing or connect.

## T3 connection, sync

T3's setup: fields `url`, `publicUrl` and `adminToken` (secret), actions `pair` (`{code}`) and `auto-pair` (`{}`), disconnect.

### T3 details
`GET /api/harnesses/t3/status` `details`:
```json
{
  "connected": true,
  "state": "connected",
  "t3Url": "http://127.0.0.1:3773",
  "connectionId": 1,
  "publicUrl": "http://t3.example.com:3773",
  "t3UrlSource": "default",
  "environmentId": "env_123",
  "clientId": "mcp_client_abc",
  "tokenExpiresAt": "2026-12-08T10:00:00Z",
  "adminTokenConfigured": true,
  "reachable": true,
  "checkedAt": "2026-11-08T10:00:05Z",
  "probeError": null,
  "t3": { "environmentId": "env_123", "label": "controlbox", "serverVersion": "0.1.9" },
  "autoPair": { "available": true, "reason": null }
}
```
`environmentId`, `clientId`, `tokenExpiresAt`, `checkedAt`, `probeError`, `t3` and `autoPair.reason` are nullable.

- `connected`: a valid token T3 has not rejected (same as the T3 entry of `/api/health` `harnesses`). T3 is configured only when paired: a stored token or a saved admin token. An unpaired T3 is never contacted in the background (no probes or sync until paired).
- `state` (T3State), the first problem in this order: `unreachable` (nothing answered as T3 at `t3Url`), `unpaired` (no token), `expired` (token past expiry), `rejected` (T3 answered 401 to the token), else `connected`.
- `t3UrlSource`: `settings` (saved through the `url` setting) or `default` (`http://127.0.0.1:3773`). `publicUrl` is the saved Browser URL (`publicUrl` setting), else `t3Url`. `adminTokenConfigured`: an admin token is saved.
- `reachable`, `checkedAt`, `probeError`, `t3`: the last unauthenticated probe of `{t3Url}/.well-known/t3/environment` (`probeError` is a short reason such as `connection refused`, `timed out`, `not a T3 server (HTTP 404)`). Probes are cached 10s, success or failure, and concurrent refreshes share one request. The status route refreshes a stale probe (waiting at most 3s); `/api/health` never probes.
- `autoPair`: whether the `auto-pair` action can work. `reason` when not: `not_local` (`t3Url` is not loopback: `localhost`, `127.0.0.0/8`, `::1`), `cli_not_found` (no `t3` CLI: `T3_BIN`, then `PATH`, `~/.local/bin`, `~/.bun/bin`, `~/.local/share/pnpm`, `~/.npm-global/bin`, `~/.volta/bin`, `/usr/local/bin`, `/opt/homebrew/bin`), `fake` (`T3_FAKE=1`).
- `sync`: the running full sync (`projects` and `threads` mirrored so far); all zero when none runs.
- `connectionId`: this machine's active T3 connection (0 when none). It changes when another T3 replaces it.

Instances: each T3 environment is its own harness connection (`GET /api/connections`); one T3 connection is active per machine at a time (these routes set up the local machine's). There is no default harness: a fresh install has no T3 connection until the first successful pairing creates one (or a refreshed status finds a valid token and the T3 identity). The first successful pairing stamps the active connection with that environment (a server paired before identities existed is stamped the first time it reads a valid token and the T3 identity). While paired, the T3 URL may change only to an address serving the same environment. Pairing a different environment replaces the connection: the old one retires with its tasks, sessions, bindings and mirror (history only: never polled, linked or launched on again), a new connection becomes active, and backlog tasks and schedules require explicit reselection. Discovery places workspaces on the new connection using stored identity evidence. Pairing a retired connection's environment again revives it (its bindings and tasks come back). Moved instances: when a disconnected T3 connection's environment is active and connected on another machine (the T3 was paired again from a worker), the server retires the disconnected one onto it: the default connection prefill moves there, and tasks and schedules keep their original destination. It checks at startup, every full sync interval (5 minutes), after a pairing, when a worker connects and after `POST /api/sync`; it never retires a connection on an offline worker (its state is unknown) or one that is still connected.

### Action `pair`
Paste fallback: exchange a pairing code from `t3 pair` for a token.
Body: `{ "code": "ABCD-1234" }`. 200 `HarnessStatus`, plus `replaced` when this pairing reached another environment and replaced the active connection. 400 empty code. 502 pairing failed. A success starts a full sync in the background (watch `sync`).

### Action `auto-pair`
Pair with no code: runs `t3 auth pairing create --json --label "<client name> mcp" --ttl 5m --scope ...` (the full-access scopes) with the `t3` CLI on the server's machine (15s limit) and redeems the code like `pair`. Body `{}`. 200 `HarnessStatus` (with `replaced` like `pair`). 409 auto-pair unavailable (`autoPair.reason`, named in `error`). 502 T3 is not answering at `t3Url` (checked first, so nothing is minted), or the CLI or the pairing failed (its error in `error`; a T3 with a custom `T3CODE_HOME` can make the CLI mint for another instance: pair with a code instead).

### Disconnect (`DELETE /api/harnesses/t3/connection`)
Disconnect: forget the stored token, the T3 URL, the Browser URL and the admin token, and stop syncing. The connection record keeps its environment: pairing the same T3 resumes it, pairing another replaces it (see Instances). Tasks, projects, bindings, journals and mirrored data stay; nothing is sent to T3, so the token stays valid there until it expires. Active T3 tasks keep running in T3, untracked until a pairing. No body. 200 `HarnessStatus` (`unpaired`). Idempotent. Never locked.

### Settings (`PUT /api/harnesses/t3/settings`)
Body: any of `{ "url": string | null, "publicUrl": string | null, "adminToken": string | null }`, at least one; `null` clears. `url`: the T3 URL (cleared: `http://127.0.0.1:3773`). `publicUrl`: the Browser URL for Open in T3 links (empty: the T3 URL). `adminToken`: optional secret that lets Stage renew the T3 token automatically; never returned. A URL must be `http(s)://host[:port]` (no path, query or credentials; a trailing `/` is dropped). 200 `HarnessStatus` (freshly probed). 400 invalid field or empty body. 409 only while paired, when the new `url` does not serve that environment (`This stage server is paired with T3 '<label>'. Disconnect it first, then change the URL and pair the other T3; its tasks stay here, read-only.`) or nothing answers there to check it. Unpaired, any URL is accepted. A change applies at once (the MCP session reconnects; the token is kept) and survives restarts. On a worker the admin token stays in the worker's credentials file (like the T3 token); the URLs are stored on the main server. `stage harness t3 set` writes the same settings.

### `POST /api/sync`

A mixed-harness sync refreshes connections independently. A partial success includes `unavailableConnections` with the IDs that could not finish. Only configured connections sync; there is no default T3 endpoint.
Refresh workspace catalogs, organize newly discovered rows and refresh task-owned sessions. External active-session discovery belongs to the notification watcher. Sync does not sweep historical sessions. Synchronous.

## Logical project groups

- `GET /api/project-groups?archived=false|true|all` returns
  `{groups: [...]}`. The default includes all unarchived groups, even
  empty or retired-only groups.
- `GET /api/project-groups/:id` accepts a project ID or legacy alias.
  Human responses include `id`, `title`, `archivedAt`, `aliases`,
  `lastActivityAt`, `state`, `members`, and `relatedProjectIds`. State is `empty`,
  `retired` when every member's connection is retired, or `current`
  when at least one member has an active connection. Related IDs share
  strong workspace evidence and can be reviewed manually.
- Each member has `connectionId`, `workspaceId`, `machineId`,
  `title`, `path`, `catalogState` (`listed`, `missing`,
  `archived`, or `retired`) and `blockedReason` (why the harness will not
  start a session in a listed member, `""` when nothing known stops it).
  These are stored catalog observations.
  Current destination eligibility comes from the destination projection.
- Scoped agent group reads return the shared `id`, `title`,
  `archivedAt` and `aliases`. The destination response on
  `GET /api/projects?view=destinations` and MCP `list_projects` includes
  these summaries in `groups`. Destination summaries and human catalog
  rows include `groupProjectId`. The existing `projectId` identifies
  the legacy binding's project.
- `POST /api/project-groups/membership` lets an active member or a
  scoped agent preview an edit with `{connectionId, workspaceId, targetProjectId}`.
  Supply an existing project ID to group the row there. Use null to
  separate the row into a new project. The preview names the source and
  target (or suggested new title) and returns a `previewToken`. Repeat the
  same request with that token to apply atomically. A 409
  `preview_changed` includes the updated preview. A successful edit
  returns `applied: true` and both project IDs. Agent responses omit the
  workspace `path`, also in the 409 preview.

Task and schedule filters and snippet scopes use the project IDs on their
records. Group edits operate on one catalog row at a time. Existing project
IDs, aliases and recorded work stay addressable.

## Projects

Stage owns project IDs, titles and archive state. Creating a project needs no
harness. Discovery organizes catalog workspaces through evidence-based
[grouping](discovery.md). Project IDs in paths, bodies and filters also
accept merged aliases. Responses use the surviving ID.

`Project` is `{id, title, bindings, aliases, archivedAt, createdAt, createdBy, sourceMachineId, updatedAt, lastActivityAt, origin, importedAt, repos}`.
`aliases` lists `{id, title, mergedAt}`. Project `origin` is `discovered` or
null. `repos` lists distinct normalized origin remotes from active bindings.
Activity includes tracked sessions, tasks and journals.

`ProjectBinding` is `{id, projectId, connectionId, machineId, harness, externalProjectId, title, workspace, revision, status, observedWorkspace, connectionActive, connectionLabel, primary, rule, remote, subpath, fileHost, evidenceAt, facts}`.
`status` is `available`, `missing` or `changed`. `rule` is `manual`, `new`,
`remote`, `local` or `moved`. `fileHost` and `evidenceAt` may be null.
Workspace `facts` use the declared fact shape. Stage adds an archive fact
when the harness archived the workspace.

- `GET /api/projects?archived=false|true|all` returns `{projects: Project[], imports: [{connectionId, harness, count, at}]}`. `imports` counts, per connection, projects discovery created in the last seven days.
- `POST /api/projects` takes only `{title}` and returns 201 Project. The trimmed title is required. The old `bindings` field returns 400. To add a workspace, preview and confirm a separate [membership edit](#logical-project-groups).
- `GET /api/projects/:id` returns Project or 404.
- `PATCH /api/projects/:id` takes `{title?, archived?}` and returns Project. An omitted field keeps its value; a present `title` must be nonblank.
- Scoped agent callers get the project summary from create and update.

Scoped agent reads return project summaries and the scoped destination
projection. Human callers get the same projection with
`GET /api/projects?view=destinations`. It includes `destinations`, an array
of `{connectionId, workspaceId, title, connectionLabel, machineLabel,
discriminator?, projectId?, groupProjectId?, available, reason?}`. Use the exact
pair of an available destination for selection. A listed workspace its harness
will not launch in (for example, a folder not trusted in Claude Code) has
`available: false` and `reason` in the harness's words. A short discriminator distinguishes repeated display
labels. The projection includes eligible rows without historical bindings
and omits paths, native IDs, evidence and discovery diagnostics. Project
summaries include identity and aliases. Human project history retains
recorded binding fields for provenance.

### Workspace discovery and project identity

`Workspace` is `{connectionId, workspaceId, externalId, title, workspace, available, importable, harnessArchived, blockedReason, activeAt, projectId, groupProjectId, bindingId, primary, fileHost, canonicalPath, remote, subpath, identityKey, evidenceAt, evidenceSource, declined, resolution}`.
An empty `workspace` is a valid pathless row. `blockedReason` says why the harness will not start a session in a listed row (`""` when nothing known stops it); such a row is not an eligible destination ([discovery](discovery.md#eligibility)). `projectId` and `bindingId`
are null for unbound rows. `resolution` is `{outcome, reason, rule, candidates}`.
Outcomes are `bound`, `join`, `new` and `review`. Review reasons are
`suggested`, `ambiguous`, `declined` and `moved`. `candidates` lists project IDs.
See [storage](storage.md#project-identity) for persisted values.

| Route | Request | Result |
| --- | --- | --- |
| `GET /api/connections/:connectionId/workspaces` | Live refresh by default, or `?refresh=false` for a cached read | 200 `{workspaces: Workspace[], inventory: {state, refreshedAt, lastError}}`. 404 for unknown or forgotten connections. |
| `GET /api/projects/:id/events` | Latest project identity events | 200 `{events: [{id, type, text, createdAt, createdBy, actor, cause, sourceMachineId}]}`, newest first, up to 100. `type` is `workspace_moved`, `project_merged`, `project_split` or `workspace_added`. `actor` and `cause` follow [work attribution](#work-attribution); events recorded before migration 0049 have a null actor and cause. |

Each `workspaceId` names the exact connection, native ID and reported
location. It stays stable across refreshes and membership edits. A
different root or connection has its own selector.

`ready` means a complete refresh succeeded, including an empty result.
`pending` means the connection has no refresh result yet. `refreshing` means
a refresh is running. `stale`, `unavailable` and `retired` distinguish
refresh failure, loss of access and replacement. These states can carry
last-known rows with `available: false`. `refreshedAt` records the most
recent complete refresh and `lastError` records the most recent failure
until a refresh succeeds. Live refresh can return 409 `connection_retired`
or `connection_unavailable`, or 502 on a harness inventory failure.

### Add a workspace

`POST /api/connections/:connectionId/workspaces` asks the connection's
harness to add a workspace ([ADR 0012](adr/0012-harness-workspace-add.md)).
Humans and scoped agents may call it. The body is
`{sourceId, input?, requestId, projectId?, confirmed?}`:

- `sourceId` names one of the connection's `workspaceSources` from
  [capabilities](#get-apiconnectionsconnectionidcapabilities). Each source is
  `{id, label, help?, input: Field[], confirm?, availability}`, where a field
  uses the declared action field shape and may have kind `path`.
- `input` is an object validated against the source's fields (omitted or
  null: `{}`).
- `requestId` (required, at most 256 bytes) is the caller's retry key.
  Reuse it to retry the same request.
- `projectId` moves the added workspace into that project with the same
  membership edit as `POST /api/project-groups/membership`.
- `confirmed: true` is required when the source has `confirm` text.

Stage checks the request before the harness sees it:

| Status | When |
| --- | --- |
| 400 | missing `requestId` or `sourceId`, invalid input, missing confirmation, or a harness validation error |
| 404 | unknown or forgotten connection, unknown source, or unknown `projectId` |
| 409 | retired or unavailable connection, `workspaceAdd` or the source unavailable (`operation_unavailable` with the reason), `harness_busy` (the harness changed nothing, for example Claude Code holding its state lock; retry), or `membership_failed` |
| 502 | harness failure, or `workspace_not_listed` when the next inventory refresh does not list the returned workspace |
| 504 | `outcome_uncertain`: a lost worker reply, the 5-minute deadline, or the harness could not confirm |

After the harness answers, Stage refreshes the connection's inventory,
resolves the row by native ID and root, and groups it like any discovered
workspace. 201 returns `{workspace, group: {id, title}, existing,
message?, facts?}`. `workspace` is the destination projection
(`connectionId`, `workspaceId`, `title`, `connectionLabel`,
`machineLabel`, `projectId?`, `groupProjectId?`, `available`) plus `path`
for humans. `existing` is true when the harness already had the target.
Agent responses omit `path`, `message` and `facts`.

Each success records a `workspace_added` project event on the group with
the actor and a `workspace_add` cause carrying `requestId` and the caller
session. A `membership_failed` 409 means the workspace was added but not
moved; retrying with the same `requestId` is safe. T3 adds folders, clones
and new projects ([T3 workspace sources](execution.md#t3-workspace-sources)).
OpenCode adds existing folders ([source `folder`](execution.md#adding-folders)).
Codex trusts folders ([source `trust`](execution.md#codex-adapter)). Claude
Code trusts folders ([source `trust`](execution.md#claude-code-adapter)).

### Browse folders

`GET /api/connections/:connectionId/directories?path=&hidden=` lists the
child folders of `path` on the connection's host, through the declared
`directoryBrowse` operation. Use it to pick a `path` field's value. An
empty `path` opens the harness's default start. `path` must be absolute
and at most 4096 bytes. `hidden=true` includes dot-named folders (default
false). The response is `{path, parent, entries: [{name, path}],
truncated}`: folders only, sorted by name ignoring case, at most 500
(`truncated` when more exist), with `parent` `""` at the root. Browsing
reads only and is `Cache-Control: private, no-store`.

Only people may browse, like session search: the personal operator and
active shared members and admins. Agent credentials, MCP tool calls,
private agent sockets and worker agent gateways receive 403
`principal_forbidden` ("Browse folders from the Stage app. Agents type
paths."). There is no MCP or CLI equivalent.

400 means invalid input or the harness refused the folder (missing, a
file, unreadable), in the harness's words. 409 `operation_unavailable`
gives the harness's reason it cannot browse, or that the worker needs an
update. 502 `directory_browse_failed` means the listing failed. An offline
worker answers like any worker route.
[Folder browsing by harness](execution.md#folder-browsing)

### Retired workspace routes

The following routes return 410 with `{error, code: "removed"}`.
The `error` field names the replacement.

| Retired operation | Replacement |
| --- | --- |
| `PUT /api/projects/:id/binding`, `DELETE /api/projects/:id/binding`, `DELETE /api/projects/:id/bindings/:bindingId` | Move or separate the exact workspace with `POST /api/project-groups/membership`. |
| `PUT /api/projects/:id/bindings/:bindingId/primary` | Select `connectionId` and `workspaceId` on the task or schedule. |
| `POST /api/connections/:connectionId/resolve` | Refresh with `POST /api/sync` and read `GET /api/connections/:connectionId/workspaces`. |
| `POST /api/projects/:id/merge`, `POST /api/projects/:id/split`, `POST /api/connections/:connectionId/workspaces/decline` and `.../undecline` | Edit membership one workspace at a time with `POST /api/project-groups/membership`. |
| `GET /api/workspaces/review`, `POST /api/projects/:id/merge-dismissals` | Read `GET /api/project-groups`. Existing distinct groups already represent separate membership. |
| `GET /api/connections/:connectionId/projects`, `GET /api/shared-paths`, `POST /api/projects/:id/bindings/:connectionId/match` | Read `GET /api/connections/:connectionId/workspaces`. |
| `PUT /api/shared-paths` | Use `POST /api/project-groups/membership`. |

### Workspace route authorization

In personal mode, the implicit operator uses the human product routes.
During `setup_pending`, product routes are closed to humans and agents.
In claimed mode, active human members and admins can create or edit projects,
change group membership, read or refresh inventory, browse the
connection's session roster and browse its host's folders. The retired routes keep human admission, so
410 guidance is returned only after admission.

Scoped agents can read project and group summaries, project events and the
exact destination projection, create, rename and archive projects, preview
and apply group membership edits, add workspaces, fetch scoped capabilities, and use the
allowed task and schedule operations
([ADR 0011](adr/0011-agent-operation-parity.md)). Their project and
membership responses omit raw paths. Raw inventory, `POST /api/sync`,
roster search and folder browsing require human admission. Named session reference lookup,
context and adoption retain agent access under the existing transport
scope and adoption checks. Declared actions also enforce their caller
policy.

### Harness and connection records

- `GET /api/harnesses`: the harness kinds this server knows and what this machine can run: `{machine: {id, label, createdBy, profile: {kind, localProcesses}}, harnesses: [{kind, name, localProcesses, sessions, stateReasons, closures: [{signal, provenance, setting, completed, via}], linkSchemes, webAddress?, unlistedRequests?, setup: {fields, actions, disconnect}, attribution: {metaKeys, env?, precedence?: {owns?, yields?}, origin?: {env?}}, launch, sessionHint?, wraps?: {origins}, available, reason?, connectable}]}`. `sessionHint`: what names one of the harness's sessions when a user tracks it (Codex: a thread ID or a pasted `codex resume` command; Claude Code: a session UUID, the 8-character ID `claude agents` shows, or a pasted `claude attach` or `--resume` command); hosts show it as the Track session field's hint, and the field keeps its generic copy when it is absent. `setup` and `connectable`: see [Harness connections](#harness-connections-api-v6). `launch`: `reserved` or `correlated`, how a launch learns its session identity ([launch identity](execution.md#launch-identity)). `attribution`: how the harness names an agent's session: `metaKeys` are MCP `_meta` keys it sends per call, `env` the variable it sets to the session id in agent processes, which the CLI and stdio MCP read (see [Agent interface](#agent-interface)). `precedence` ranks `env` when several harness variables are set: `owns` and `yields` are `[{env, value?}]` conditions (`value` absent: any non-empty value). `origin`: the harness records the client that started each session, so another harness may wrap it; `env` is the variable holding that origin in its agents (Claude Code `CLAUDE_CODE_ENTRYPOINT`). `wraps`: the harness runs other harnesses' sessions and leaves these `origins` in them (T3: `t3-code`, `T3 Code`); Stage refuses to track such a session on the wrapped harness (409 `session_refused`, "Track it through T3"), and when the wrapper's `env` is set (or a wrapped harness's origin variable holds one of its origins) the wrapped harness's `env` yields to it ([module policies](execution.md#modules-and-the-registry)). `profile.kind`: `desktop` (a managed per-user service: the server's, or on a remote machine the worker service), `server` (foreground `stage serve` or `stage worker run`) or `container` (the Docker image). `localProcesses` says whether the module requires a profile that permits local processes. `sessions` says whether its sessions live on a machine or in the cloud, independently of process requirements. `stateReasons` lists the reasons the module may report with observations. A remote machine reports its worker's last advertised profile and returns the worker's fix when setup is unavailable. Before the worker advertises its profile, setup replies can return 503 `machine_offline` or `machine_setup_starting`. `closures`: the closure signals the harness reports and the copy for each: `setting` labels its auto-complete toggle, `completed` and `via` describe an inferred completion with that `provenance`. `linkSchemes`: the harness's own native URL schemes, an empty array when it declares none. The registry requires unique lower-case scheme names without a colon matching `^[a-z][a-z0-9+.-]*$` and reserves `http`, `https`, `ftp`, `ftps`, `mailto`, `tel`, `javascript`, `data`, `vbscript`, `file`, `blob`, `about`, `stage` and all `stage-` prefixes. T3 declares `t3code`. `webAddress` is true when a session link can need a browser address in connection settings. `unlistedRequests`: the harness can wait on requests Stage can't list (its `respond` lists only some kinds), so a needs-input session with none listed waits on one of those, not on a reply; the value says where to answer them (T3: "Approve permission requests in T3."), and the app shows it instead of a Reply composer. Clients use the descriptor for closure copy and for rendering harness links in replies. See [descriptor policies](execution.md#descriptor-policies).
- `GET /api/connections`: `{connections: [{id, machineId, harness, configured, autoComplete, active, label, retiredAt, forgottenAt, tasks, bindings}]}`, active first. `machineId`: the main machine or worker that runs the instance. A connection records one harness instance. At most one per kind is active on a machine. Use the returned IDs. A replacement gets its own ID and a revived connection keeps its old ID. `autoComplete` (default true): a closure in this harness may complete a done task (see "Inferred completion"). `label`: the instance's name (T3 label, OpenCode address). `retiredAt`: when another instance replaced it (null while active); a retired connection keeps its tasks, sessions and bindings as history, is never polled, linked or launched on, and is `configured: false`. `forgottenAt`: set by DELETE. `tasks`, `bindings`: tasks and active bindings it holds.
- `PUT /api/connections/:connectionId/auto-complete`: strict `{autoComplete: boolean}`. 200 `{connection}`; 400 missing/invalid body; 404 unknown connection.
- `PUT /api/connections/:connectionId/new-task-config`: strict `{config: {model, options} | null}`, the model and options new tasks on the connection start with (`newTaskConfig` on its row; null or an empty config unpins). Checked against the connection's capabilities (no project). Pairing another instance of the harness carries the pin to the replacement. 200 `{connection}`; 400 unknown or replaced connection, a model or option value the connection does not offer, or a `permission` / `mode` (never pinned).
- `DELETE /api/connections/:connectionId`: forget a retired connection, or an active one that is disconnected (`configured: false`) on a machine that answers (the local machine or an online worker): it retires (the default connection is cleared; its tasks, backlog and schedules stay as history) and is forgotten at once. Removed outright when no task, session or launch references it; otherwise its tasks and history stay (still naming it), its identity is cleared (that instance can no longer revive it; pairing it again starts a new connection), its bindings are deactivated, and grouped catalog rows remain visible as retired history. 204. 404 unknown or already forgotten. 409 the connection is connected (`Disconnect it first`), or its machine is offline.
- `POST /api/connections/:connectionId/observations`: an external bridge or hook pushes observations of the connection's sessions, for a harness Stage cannot poll or stream well ([external push](execution.md#external-push)). Strict body `{observations: Observation[]}`, 1 to 100 (body at most 1 MiB); `Observation` uses the adapter contract's JSON. Keys are `session` (`{connectionId, externalSessionId}`), `project`, `title`, `state`, `active`, `archived`, `child`, `branch`, `directory`, `links`, `createdAt`, `updatedAt`, `startedAt`, `endedAt`, `executionId`, `config`, and optional `parent`, `reply`, `usage`, `closed`, `stateReason` and `origin`. `links` may contain `web`, `native` and `command`. `reply` contains `itemId`, `runId`, `text`, `truncated` and `updatedAt`. For the removed keys and their 400 responses, see [API v9 changes](#api-v9-changes). Bridge pushes share ingest rules with local adapter events and worker batches. The coordinator's apply path enforces the harness's policies, updates task state and checks inferred completion. `updatedAt` orders snapshots: one older than the stored observation never replaces it. 200 `{results: [{status, reason?}]}`, one per observation in order: `ingested` (the owning task took it), `ignored` (no task owns the session, left to discovery; or its task is launching and the launch reconciles it), `invalid` (malformed: unknown state or reason, contradictory activity, missing identity or `updatedAt`, an `updatedAt` more than 5 minutes ahead of the server's clock (it would pin the stored snapshot), a reason the harness may not report; nothing changes; `reason` says why). Polling continues and reconciles. 400 bad JSON, unknown field, an empty or oversized batch, or any observation whose session is on another connection (nothing is ingested); 404 unknown connection; 409 `connection_retired` (a replaced connection). Any active connection accepts pushes, including one whose worker is offline or incompatible. 403 `gateway_forbidden` through a worker's agent gateway for a connection of another machine. A storage failure is 500 after the observations before it were ingested; pushing the batch again is safe. No MCP tool or CLI command pushes.
- Retired connections: creating or moving a task or schedule to a retired connection returns 409 `code: "connection_retired"`. Tasks carry `harness`, `connectionActive` and `connectionLabel`; project bindings carry `connectionActive` and `connectionLabel`. Tasks on retired connections omit live session links and retain recorded evidence.
OpenCode's setup: fields `endpoint`, `browserEndpoint`, `webClient`, `username`, `password` and `newInstance`; actions `connect-local` (`{newInstance}`) and `pair` (`{link, newInstance}`); disconnect. `GET /api/harnesses/opencode/status` `details` reports connectionId, state (`unconfigured`, `connected`, `unavailable`, or `rejected` when the service answers 401/403), diagnostic reason, checkedAt, endpoint, browserEndpoint, webClient, hasCredentials, and:

- `auth`: `none`, `local` (the service on the Stage server's machine, authenticated from its registration file), `paired` (a token from an `opencode pair` link) or `basic` (entered credentials).
- `expiresAt`: RFC3339 expiry of a paired token (issued + 30 days); omitted otherwise. Stage renews a token older than 7 days on the next health check (at most one attempt at a time, retried hourly after a failure, keeping the old token).
- `local`: `{available, url, version, reason}` for the OpenCode service registered on the Stage server's machine now (cached ~10s). `reason` is `""` when available, else `missing`, `invalid` or `unreachable`. Never includes the password.

Each connect tests the service and returns 200 `HarnessStatus`. Credentials and tokens are write-only. PUT, connect-local and pair accept `newInstance` and share the instance check below.

- `PUT /api/harnesses/opencode/settings`: changed endpoint, browserEndpoint, webClient, username, password and `newInstance`. A new endpoint clears stored credentials (auth `none`, or `basic` with credentials in the same request); credentials set `basic`; browserEndpoint/webClient alone keep the auth mode and token. URL errors name the field (`Service URL`, `Browser service URL`, `Web client URL`) and any invisible character with its code point.
- `POST /api/harnesses/opencode/actions/connect-local` (`{newInstance}` optional): connect the service registered on the Stage server's machine; its password is read from the registration file and never stored.
- `POST /api/harnesses/opencode/actions/pair`: `{link}` from `opencode pair` (`http(s)://host:port/auth/connect/CODE`, no credentials, query or fragment; else 400). The link origin becomes the endpoint. A link stopped by `instance_changed`/`instance_returned` keeps its redeemed token for 10 minutes, so resending the same link with `newInstance: true` confirms it (links work once).
- `DELETE /api/harnesses/opencode/connection`: disconnect. Clears the addresses, credentials and token and deactivates the adapter; keeps the connection's identity, tasks and history. Never locked.

409 codes: `opencode_local_unavailable` (no, unreadable or invalid registration file, or the service is not answering), `opencode_pairing_expired` (the code expired or was used), `opencode_auth_required` (the service answered 401/403).

Claude Code's setup (kind `claude`, hosted: only where the machine allows local processes, [runtime.md](runtime.md#machine-profile-and-hosted-harnesses); a remote machine refuses it for now): field `binary` (the claude command name or path; null or blank resets to the default lookup: `PATH`, then `~/.local/bin`, `~/.claude/local` and the other usual install dirs); action `connect` (`{}`); disconnect. Stage stores no Claude credentials: Claude's own login is used. `GET /api/harnesses/claude/status`: `state` `disconnected` (not set up, or disconnected: ready to connect), `connected`, `not_found` (no executable), `logged_out` or `error` (the command did not answer, or Claude now reports another config directory), with a `reason` that says what to do; `details` `{binary, version, configDirectory, loggedIn, scope, events?}` (`scope`: background launches run in their own systemd user scope; `events`, once set up: `{mode, reason?, lastEventAt}`, `mode` `push` (Claude hooks wake Stage; `lastEventAt` the last accepted hook, null for none since Stage started) or `polling` (no hooks, `reason` says why; polling is not a problem state), [execution.md](execution.md#claude-code-adapter); while it is `push`, the approvals and questions of the sessions Stage launches are answered in Stage too, [runtime.md](runtime.md#claude-code-hooks)). The first status call and `refresh=true` run `claude --version` and `claude auth status --json`, each bounded to 20 seconds; otherwise memory only.

- `PUT /api/harnesses/claude/settings`: `{binary}`. A set-up connection moves to the new command: Stage runs it once it is found (also after a command that was not, so fixing the command reconnects without another connect), and stops using Claude Code while it is not.
- `POST /api/harnesses/claude/actions/connect`: checks the command (`--version`) and the login (`auth status`), then records the machine's connection with Claude's `configDirectory` as its identity (label `Claude Code (~/.claude)`, never the account) and installs the adapter. The same config directory keeps the connection. 409 codes: `claude_not_found`, `claude_unresponsive` (no answer within 20 seconds, or an unreadable status), `claude_logged_out`.
- `DELETE /api/harnesses/claude/connection`: disconnect. Removes the adapter, survives restarts, keeps the connection's tasks and history; connect again resumes it. Never locked.

Instance check: an address change is checked against the stored identity evidence (catalog workspaces, saved pins, bindings and observed sessions): a service that serves them is the same service at a new address and keeps the connection (as does a change on a connection that holds nothing). Otherwise 409 `code: "instance_changed"`, or `code: "instance_returned"` when the service proves to be a retired connection's (it serves that connection's workspaces and sessions), and nothing changes; resend with `newInstance: true` to confirm: the current connection retires (as for T3, see Instances), and a new one becomes active, or the proven retired one revives. A different service gets its own connection. Discovery resolves its workspaces after the change is confirmed. `connectionId` in the status changes with it.

Codex's setup (hosted; [ADR 0006](adr/0006-codex-app-server-harness.md)): field `binary` (the Codex CLI path; empty or `null` uses `codex` on PATH), action `connect` (`{}`), disconnect. `GET /api/harnesses/codex/status` `state`: `connected`, `not_connected`, `signed_out` (the daemon answers but Codex has no login), `unreachable` (the daemon is down or not answering), `not_installed` (the CLI is missing). `details`: `{binary, cliVersion, appServerVersion, codexHome, daemon: running|stopped|unknown, account: {type, email, plan} | null, fix, checkedAt}`. `fix` is a shell command that fixes the current problem (`codex login`, `codex app-server daemon start`), else `""`.

- `POST /api/harnesses/codex/actions/connect`: starts the daemon if it is down, reads the account and connects the Codex home as the connection's identity (label `Codex · <email>`). 409 `codex_not_installed`, `codex_signed_out` or `codex_unavailable` with the problem as the message; nothing changes.
- `PUT /api/harnesses/codex/settings`: `{binary}`. A connected Codex reconnects through the new CLI on next use.
- `DELETE /api/harnesses/codex/connection`: deactivates the adapter; the daemon keeps running and its threads stay in Codex.

Codex tasks: respond and send are available ([session interaction](#session-interaction-api-v6)); requests Stage doesn't list are answered in Codex, and every Codex session's links carry `command`, the `cd <dir> && codex resume <id>` line to copy. Adoption accepts a thread id or that command; a thread T3 started (originator `T3 Code`, which T3 wraps) is refused with "This Codex session was started by T3. Track it through T3."

Request `/api/connections/:connectionId/capabilities?workspaceId=ID` for options for an exact workspace. The scoped session context route supports explicit adoption previews. Task detail includes a bounded finalReply, the session's `usage` when its harness reports one, and `links.web`, `links.native` or `links.command` when available. `links.command` is a shell command that resumes the session in the harness's own client; hosts offer to copy it (Copy resume command) when there is no destination to open. Capabilities may list harness-wide `options` (any model) beside each model's `options`; an `executionConfig` may set the union, and option ids never collide (capabilities that reuse an id are rejected).

External deletion or connection failure retains Stage metadata, tasks and
history. Launch checks the saved workspace on its owning connection. A
missing pin returns 409 `destination_required`. An unavailable pin returns
409 `destination_unavailable`. Edit backlog work or a schedule to reselect the
workspace.
Claimed attempts and uncertain retries retain the original connection,
native workspace ID, root and any binding revision.

### Identity contract (API 5)

Use matching API 5 server, CLI and desktop builds. Project `externalProjectId`,
`workspaceRoot` and `source` are replaced by `bindings`. Session/task wire names
use `externalSessionId` and `externalProjectId`, with `connectionId`.
A scoped reference is `{connectionId, externalSessionId}`; tasks expose `session`
and `originSession`, journal entries `sourceSession`, accepted evidence `session`,
and resolved references `session`. A raw ID is never globally unique.

Bare historical session references explicitly mean the local machine's active T3 connection.
Storage holds no bare IDs: migration 0027 resolved stored ones to session
identities (the row's own connection, else an existing T3 identity, else the
local active T3; unresolvable ones became null) and 0028 dropped the columns.
Task and schedule requests use `originSession` and `needsSession`; journal notes
use `sourceSession` (API v6 removed `originThreadId`, `needsSessionId`, `sourceThreadId` and `callerThreadId` as inputs). T3 cache tables and historical links remain intact. Migration
does not rewrite accepted snapshot JSON or journal text.

See [explicit session lookup and adoption](#explicit-session-lookup-and-adoption)
for the current named-session contract.

Backlog tasks can be saved for planning without a workspace. Launch
requires a verified exact pin before claiming an attempt. A cron occurrence
with a missing or unavailable pin records a skip with a repair reason.

Save-and-start requests retain the saved task if launch preflight fails. The task
stays in backlog with `lastError` and a journal event naming the repair; no launch
attempt is consumed.


## Tasks

`Task`:
```json
{
  "id": 7,
  "projectId": 1,
  "projectTitle": "stage",
  "title": "Say OK",
  "prompt": "reply OK",
  "connectionId": 1,
  "harness": "t3",
  "executionConfig": { "model": "[\"claude\",\"claude-haiku-5-5\"]", "options": [{ "id": "effort", "value": "low" }], "permission": "full-access", "mode": "" },
  "state": "running",
  "externalSessionId": "thread_abc",
  "session": { "connectionId": 1, "externalSessionId": "thread_abc" },
  "links": { "web": "http://t3.example.com:3773/env_123/thread_abc", "native": "t3code://threads/env_123/thread_abc" },
  "treeId": 3,
  "treeTitle": "Ship auth",
  "startMode": null,
  "effectiveStartMode": "auto",
  "readiness": null,
  "needs": [5],
  "dependents": [9],
  "adopted": false,
  "readyAt": null,
  "startAt": null,
  "startTz": null,
  "scheduleId": null,
  "scheduleTitle": null,
  "originSession": null,
  "originSessionTitle": null,
  "handoff": true,
  "attachments": [{ "id": 4, "name": "error.png", "mimeType": "image/png", "sizeBytes": 48211 }],
  "launchAttempt": 1,
  "lastError": null,
  "launchedAt": "2026-11-08T10:00:00Z",
  "finishedAt": null,
  "acceptanceId": null, "acceptanceRevision": 0, "completedAt": null, "completionProvenance": null,
  "stateRevision": 2,
  "steering": null,               // { treeId, active, endedAt?, endReason? } once it steered a tree
  "runStartedAt": null,
  "runEndedAt": null,
  "lastWordPreview": null,
  "lastWordAt": null,
  "createdAt": "2026-11-08T09:59:00Z",
  "createdBy": 1,
  "sourceMachineId": null,        // the forwarding machine when created through a worker's agent gateway
  "updatedAt": "2026-11-08T10:00:00Z",
  "stateSince": "2026-11-08T10:00:00Z",
  "archivedAt": null,
  "treeStatus": null,
  "deletable": false
}
```
- `stateSince`: when the task entered its current `state` (its latest task event into that state); null without one.
- `archivedAt`: when the task was archived (null = not archived); `treeStatus`: its tree's `status` (null outside a tree); `deletable`: the task never launched, has no session and has no recorded work, so it can be deleted; recorded work must be archived. See "Done, archive and delete".
- `executionConfig`: the model, options, permission and mode the launch uses (`""` = the harness default). Legacy T3 model inputs are translated into it on write, and migration 0027 backfilled T3 tasks saved before execution configs, so every T3 task carries one.
- `treeId` / `treeTitle`: the task's tree (see "Trees"); null outside one. `needs`: tasks in the same tree this one needs (by id); `dependents`: tasks that need this one. Both `[]` outside a tree.
- `startMode`: the task's own start mode (null = inherit the tree's); `effectiveStartMode`: `startMode`, else the tree's; null outside a tree. `readiness`: see "Tree rules". `readyAt`: when a manual task's needs all have user acceptance (the `ready` notification); cleared when it stops being ready or launches.
- `adopted`: the task tracks an existing harness session (see "Adopt session"); `prompt` is `""`.
- `startAt` (UTC instant) / `startTz` (IANA zone it was entered in): one-shot start time of a `backlog` task; the scheduler launches it at that time (see "Schedule rules"). Both null or both set; non-null only while the task is in `backlog` (any launch clears them). Never set together with `needs`.
- `scheduleId` / `scheduleTitle`: the recurring schedule that spawned this task (null for ordinary tasks, or once the schedule is deleted).
- `originSession` / `originSessionTitle`: the session that created the task (agent CLI/MCP provenance; spawned schedule tasks inherit the schedule's). Title is the mirrored session title, null when not mirrored. Not editable.
- `handoff`: append one handoff block per accepted need to the launch prompt (see "Handoff"). Default `true`; editable in `backlog` only.
- `attachments`: the task's images (`Attachment`, upload order), sent with the prompt on every launch. Default `[]`. See "Images".
- `links`: where the session opens, from the harness adapter: `web`, `native` (an app scheme), `command` (a copyable resume command); each omitted when the harness has none. T3: `web` = `publicUrl/<environmentId>/<externalSessionId>` (the Browser URL), `native` = `t3code://threads/<environmentId>/<externalSessionId>`.
- `finishedAt`: first entry into `done|failed|interrupted|cancelled` (per launch attempt).
- `stateRevision`: counts the task's real state transitions (a same-state event does not count; 0 for a new task). A guarded answer names the revision it answers (`expectedStateRevision`, see "Session interaction").
- `steering`: the task's latest steerer binding (see "Tree steering"), null when it never steered: `{ "treeId", "active", "endedAt"?, "endReason"? }`, `endReason` one of `replaced | stopped | tree_finished | task_removed`.
- `hold`: the task's held needs-input notification (see "Notification rules"), else null: `{ "until", "steererTaskId" }`. `until`: when it escalates to the user unless answered first; `steererTaskId`: the steerer that can answer (null once that task is deleted).
- `runStartedAt` / `runEndedAt`: the task session's elapsed run span, from its observation; null without a session or before one is observed.
- `lastWordPreview` / `lastWordAt`: the task session's latest reply as one plain line (markdown stripped, at most 240 characters) and its time, from the observation; null without a session or before a reply is read.

`TaskEvent`:
```json
{ "id": 3, "taskId": 7, "fromState": "launching", "toState": "running", "detail": "session status running", "at": "2026-11-08T10:00:01Z" }
```
`fromState` null for the creation event.

`TaskDetail` = `Task` + `{ "events": TaskEvent[] (oldest first), "sessions": TaskSessionSummary[] (newest first), "finalReply"?: {itemId, runId, text, truncated, updatedAt}, "tree": TreeDetail | null, "currentHandoffs": Handoff[], "currentSteering": {attempt, brief} | null, "usage"?: Usage, "reported"?: {branch?, directory?} }`. `reported`: the branch and directory the current session's harness reports (its observation), each omitted when unreported and the object omitted when it reports neither. `usage`: the current session's usage, omitted when its harness reports none; `Usage` = `{ "turns", "inputTokens" (uncached), "cacheReadTokens", "cacheWriteTokens", "outputTokens", "workingMs" (finished turns; 0 unknown), "costUsd": number | null }`, cumulative and best effort ([execution.md](execution.md)). `costUsd` is a cost the harness itself reports, else null; Stage never prices tokens, and Claude Code reports none. `stage task show` prints it as one line (`usage    1 turn · 44.3k in (27.6k cached) · 4.9k out · 23s working`, plus `$1.23` when a cost is reported). `tree`: the task's tree (see "Trees"); null outside one. `currentHandoffs`: the current launch attempt's handoff records, one per need, by need id (see "Handoff"); `[]` before the first launch, without needs, or with `handoff` off. `currentSteering`: `{attempt, brief}`, the steering brief the current launch attempt appended (see "Steering brief"); null for a task that did not launch as an active steerer. `finalReply`: the session's latest reply excerpt, omitted before one is read (API v8 removed `thread`).

### `GET /api/tasks`
Query (all optional): `originConnectionId` with `originSessionId` (the creator session; `originThreadId` is 400 since v6), `project` (project id), `schedule` (schedule id: tasks it spawned), `state` (repeatable TaskState), `q` (case-insensitive substring of title or prompt), `from`, `to` (createdAt), `completedFrom` (`completedAt >= completedFrom`), `closed` (`false` default: open tasks only, not archived and not in a done or archived tree; `true`: only those; `all`: both; else 400).
200 `{ "tasks": Task[] }` ordered by `updatedAt` desc.

### `POST /api/tasks`
Body `CreateTaskRequest`:
```jsonc
{
  "projectId": 1,                // required without an exact workspace
  "title": "Say OK",             // required
  "prompt": "reply OK",          // required
  "connectionId": 1,             // required connection ID
  "workspaceId": "ws_...",       // exact selector from that connection's catalog, or null for unresolved backlog
  "executionConfig": { "model": "", "options": [], "permission": "", "mode": "" }, // optional; "" = harness default
  "initialNote": "",             // optional: first task journal note
  "treeId": null,                // optional: join this tree (tree rules)
  "needs": [],                   // optional: tasks this one needs (tree rules)
  "needsSession": null,          // optional: {connectionId, externalSessionId}, also need this session (adopted first; "afterMe")
  "startMode": null,             // optional: auto | manual; omitted = inherit the tree's
  "originSession": null,         // optional: {connectionId, externalSessionId}, the session creating the task (provenance)
  "attachmentIds": [4],          // optional: staged uploads to attach (max 8)
  "startLocal": null,            // optional: one-shot start, wall time "YYYY-MM-DDTHH:mm" in startTz
  "startTz": null,               // required with startLocal: IANA zone, e.g. "America/New_York"
  "handoff": true,               // optional, default true: append each need's handoff at launch
  "start": false                 // optional: true = Save & start (not with needs or needsSession)
}
```
201 `Task` (state `backlog`, or `launching` when started). 400 (incl. an `attachmentIds` entry that is not a staged upload, more than 8; `startLocal` without `startTz` or vice versa; unknown zone; unparseable `startLocal`; a start time not in the future; `startLocal` together with needs or `start: true`; `start: true` with needs; bad `startMode`). 404 project / tree / need / `needsSession` (session not mirrored). 409 tree rule broken (e.g. needs in different trees) or `needsSession` not adoptable. The removed T3 model inputs `providerInstanceId`, `model`, `modelOptions`, `runtimeMode` and `interactionMode` are 400 `<field> was removed in API v8; send executionConfig with connectionId` (also on update and on schedules). An omitted `executionConfig` runs on the harness defaults. Start errors retain the task: preflight/rejection leaves backlog; uncertainty remains launching.

Immediate and one-shot starts require `connectionId` and a nonempty
`workspaceId`. With an exact selector, omitting `projectId` associates the
task with that workspace's current group, with historical context as a
fallback for an ungrouped row. For planning, provide
`projectId` and `connectionId` and omit `workspaceId` or send null. Select an
exact workspace before starting or adding a one-shot time. `bindingId`
returns 400 with replacement guidance. [Workspace selection](#task-and-schedule-workspace-selection)

### `GET /api/tasks/:id`
200 `TaskDetail`. 404.

`sessions` lists recorded attempts newest first and is empty before a
session is attached. Each entry has `attempt`, `session`, `current`,
`recordedAt`, `state`, `stateReason`, `observedAt` and optional `links`.
`current` marks the session named by the task's current pointer. A
pending restart may have no current session. `observedAt` is the time
Stage captured the stored observation. Without one it is null and the
state and reason are empty. New adoptions of sessions that have never run
use attempt 0. MCP `get_task` includes the same list and `stage task show`
prints one line per session.

### `GET /api/tasks/:id/sessions/:attempt`

200 read-only snapshot with `attempt`, `session`, `current`, `state`,
`stateReason`, `observedAt`, optional `links`, `reply`, `usage`,
`facts` and optional `reported`. The reply carries the same shape as
`finalReply` and is bounded to 4000 characters. Replies use Markdown
and the browser sanitizes the rendered HTML. Reply and usage are null
when absent. Facts follow the session harness's display policy.
`reported` contains a branch and directory when available. The route
uses Stage's observation cache and works without a live connection.
400 invalid attempt. 404 no session recorded for that task and attempt.

### `PATCH /api/tasks/:id`
Body `UpdateTaskRequest`: any of `projectId title prompt connectionId workspaceId executionConfig` `attachmentIds` `startLocal` `startTz` `handoff` `startMode`. `executionConfig` replaces the stored config; a `connectionId` change needs one. `bindingId` and the removed T3 model inputs return 400 (see create).
In `backlog` all fields are editable; otherwise only `title` (409 for others).
A changed `title` on a task whose session Stage launched (not an adopted one) renames that session in its harness when the connection offers `sessionRename` ([execution.md](execution.md#session-rename)). The rename runs after the response, in the background, within 10 s; it never fails the update. A declared-unavailable `sessionRename` is skipped. Any other failure appends a system note (`actor.kind` `system`, `cause.kind` `session_rename`) to the task's journal: "Could not rename the <harness> session: <reason>". `attachmentIds` is the exact image set: the task's own ids to keep plus staged uploads to add; the task's images not listed are deleted (400 for ids that are neither, more than 8).
`startLocal` + `startTz` (backlog only, 409 otherwise) set the one-shot start (same 400 rules as create); both `null` clears it; a task with needs cannot have one (400). `handoff`: `true|false` (backlog only, 409 otherwise; 400 for `null`). `startMode`: `auto|manual`, `null` inherits the tree's (backlog only). Needs change with `PUT /api/tasks/:id/needs`; tree membership with the tree endpoints.
200 `Task`. 404.

### `PUT /api/tasks/:id/needs`
Body `{ "needs": number[] }` (required; `[]` clears them): replaces the task's needs per "Tree rules". A task outside a tree joins its needs' tree (or a new one). 200 `Task`. 400 missing `needs`, or needs on a task with a start time. 404 task / need. 409 tree rule broken.

### `DELETE /api/tasks/:id`
Only a task that never started and has no recorded work (`deletable`). 204. 409 when active (stop first), protected by notes/execution (archive instead), or in a done or archived tree. In a tree it is spliced out first: its dependents take over its needs (A→X→B becomes A→B).

### `POST /api/tasks/:id/archive`, `POST /api/tasks/:id/unarchive`
Archive (sets `archivedAt`) or unarchive a standalone task. Archive 409 when the task is in a tree (`archive the tree, or remove it from the tree first`) or is `launching|running|needs_input` (`#N is running: stop it first`). Unarchive of a task that is not archived is a no-op. 200 `Task`. 404.

### `POST /api/tasks/:id/start`
Launch through the task's adapter. Fresh attempts are allowed from backlog,
failed, interrupted or cancelled. Every prerequisite needs user acceptance in
Stage at the atomic claim. Adopted tasks cannot launch.

The attempt freezes destination, accepted inputs, prompt, journal context,
configuration (launch options included) and images before dispatch. An uncertain outcome stays
`launching` (UI: Launch unconfirmed). Automatic reconciliation and repeated Start
use the same attempt, including after restart. Legacy attempts without safe
identity require explicit linking. A certified rejection before execution
returns to backlog for repair. See [execution.md](execution.md).

200 Task on confirmation; 409 preflight/repair required; 502/503 unconfirmed
adapter error. Additive `launch` metadata: attempt, revision, phase, reason,
retryAt, retryCount (failed checks) and session: the session the harness returned
for an attempt Stage could not record yet (`{connectionId, externalSessionId,
title, state, links}`, links as on task detail; with no destination and no
stored observation, the resume command `links.command` the harness reported
for it), else null. Internal checkpoints
and frozen prompts are not exposed.

### `POST /api/tasks/:id/launch-link/preview`
Body `{link}`: T3 session URL. Returns `{attempt,revision,environment,observation}`.
Verifies the original connection/workspace, top-level status and ownership.
Does not launch or attach.

### `POST /api/tasks/:id/launch-link`
Body `{link,attempt,revision,environment}` from the preview. Revalidates and
attaches to the unresolved attempt; 409 if it changed. No launch or acceptance.

### `GET /api/connections/:connectionId/capabilities`
Stage-owned operation availability/reasons, models/options, permissions,
interaction modes and launch options. `operations` maps every operation
(`launch`, `stop`, `adopt`, `reply`, `opening`, `attention`, `sessionSearch`, `images`,
`respond`, `send`, `steer`, `queue`, `activity`, `sessionRename`, `workspaceAdd`,
`directoryBrowse`) to `{available, reason?}`. An unavailable operation says why.
`workspaceSources`, when present, lists the sources for
[adding a workspace](#add-a-workspace).
An option (harness-wide in `options`, or a model's) is `{id, kind?, label?,
values, default, dependsOn?}`: `kind` `choice` (one of `values`),
`boolean` (`true` or `false`) or `text` (a non-empty string; `values`
empty; leave it out for the harness default); an option without `kind`
(an older harness) is `boolean` when every value is a boolean, else
`choice`. `dependsOn` `{option, value}`: the option applies only when that
option (set, or by default) has that value; otherwise it is 400. Clients
render options by `kind` and `label` and never name one. Task/schedule `executionConfig` is
`{model,options,permission,mode}`; model IDs are opaque catalog keys. A model's
`permissions: null` (not reported) inherits the top-level `permissions`.
Legacy T3 request fields remain supported through translation. Create requests
cannot combine neutral config with legacy model/permission fields.

Pass `workspaceId` to scope capabilities to a catalog workspace on that
connection. With no selector the route returns connection-wide capabilities.
The `projectId` and `bindingId` queries are rejected with 400.

`actions`, when present, advertises session and project action declarations
allowed to the caller. To check availability and run an action, select its
actual target through the routes below.

Choice launch options may supply `valueLabels`, keyed by their string values. The client shows these labels and submits the original value. An omitted Boolean follows the harness default and an explicit `false` overrides it.

### Declared actions and facts

| Target | List with GET | Run with POST |
| --- | --- | --- |
| Connection | `/api/connections/{id}/actions` | `/api/connections/{id}/actions/{action}` |
| Session linked to a task | `/api/tasks/{id}/actions` | `/api/tasks/{id}/actions/{action}` |
| Project workspace | `/api/projects/{id}/actions?connectionId=ID&workspaceId=ID` | `/api/projects/{id}/actions/{action}?connectionId=ID&workspaceId=ID` |

The task ID resolves to its linked session on its actual connection.
Both project action routes require the exact workspace on the named
connection and check its current membership in the project. A workspace
that no longer belongs to the project returns 404. A listed workspace its
harness will not launch in (`blockedReason`) still takes project actions,
since one may fix it, and so does one its harness archived
(`catalogState: "archived"`), so it can be restored. A project action that
succeeds, or ends uncertain (502), refreshes the connection's inventory. The selected
workspace determines the action's capabilities and target. Lists return
an array of declarations, including unavailable actions with their reasons.

An action carries `{id, label, target, input?, primary?, confirm?, destructive, callers,
availability: {available, reason?}}`. Targets are `connection`, `session`
and `project`. `primary` asks the UI to emphasize the action. Callers are `["user"]` or `["user", "agent"]`. Input fields
carry `{id, label, kind, help?, required?, choices?}`. Kinds are `text`,
`url`, `secret`, `bool` and `choice`. Choices carry
`{id, label}` and the input supplies a choice ID.

POST takes the input object directly, including `{}` for no input. Unknown
or duplicate fields, wrong types, missing required values and invalid choices
return 400. A boolean field can explicitly be `false`. The owner validates
the opaque input against its own rules. An undeclared action, unknown target
or task without a linked session returns 404. A disallowed agent receives
403 `action_forbidden`. Unavailable or busy operations return 409
(`operation_unavailable`, `action_busy` or the connector's conflict code).

MCP and the worker agent gateway always use agent policy. A caller session
header or `Stage-Action-Agent: 1` restricts a direct request to agent policy.
The app and ordinary CLI use user policy on Stage's trusted deployment
boundary. Caller or permission fields in action input grant no rights.
User-only destructive actions require confirmation. The CLI uses
`--confirm` and a confirmed client sends `Stage-Action-Confirmed: 1`.
The owner receives that confirmation for every action and may require
it: Claude Code's project action `trust` answers 400 without it, from
people and agents alike (MCP `run_action` `confirmed`).

Success returns `{message, facts?, link?, replaced?, journalError?}`.
`replaced` is the retired connection ID when applicable. A successful
session action adds a `session_action` event naming the action, target and
caller, with caller session provenance when available. The event excludes
input and the raw result. `journalError` means the action succeeded and
Stage could not record it. An offline worker before dispatch returns
503 `machine_offline`. A lost reply after dispatch returns
502 `action_outcome_unknown`. Check the harness before retrying.

A fact carries `{id, label, value, tone?, link?, copyable?}`. Tone is
`neutral`, `ok`, `warn` or `bad`. Labels and values are plain text.
Links may use HTTP, HTTPS or a native scheme the harness registers.
Observations can carry `facts`, which are also available on task detail.

An observation can also carry `actions`: `[{id, available, reason?}]`, the
session's own availability of its declared session actions (for example
`archive` once the session is archived). `GET /api/tasks/{id}/actions`
applies them from the last stored observation; `POST` applies them from
the fresh observation it reads before dispatch and returns 409
`operation_unavailable` with the harness's reason. A state never makes an
unavailable declaration available. A session action whose caller session
(`Stage-Caller-Session`) owns the task returns 403
`{"code": "self_action_forbidden"}` ([ADR 0011](adr/0011-agent-operation-parity.md#never-on-its-own-work)).
The session archive actions per harness are in
[execution.md](execution.md#actions-and-facts).

### `POST /api/tasks/:id/stop`
Interrupt the task's scoped session through its adapter. State updates via the
poller. A task without an execution session returns 409. A connection that
declares Stop unavailable returns 409 `operation_unavailable` with its reason.
During a catalog outage the owning adapter can still stop the session.

Stop bumps `updatedAt` and re-arms polling of a `failed|interrupted` task for
10 minutes. It then polls the session once. An adopted session waiting in
Backlog can advance when execution begins in its harness. Execution observations
alter acceptance only through inferred completion.
200 `Task`. 502 / 503.

### Session interaction (API v6)

A harness may answer what its session waits on, take messages or provide a
bounded activity view. See [ADR 0014](adr/0014-session-surface.md).
Each route needs a task
with an execution session (409 otherwise); a harness without the operation,
or reporting it unavailable, answers 409 `{"code": "operation_unavailable"}`
with the reason, and nothing else is limited. Claude Code offers `respond`
(its approvals and questions, through the hook its launches carry) and
`send` (a follow-up to a finished background session, or one whose turn
ended asking you: [execution.md](execution.md#claude-code-adapter)); T3
offers `respond` (its threads' questions; permission requests are answered
in T3, its descriptor's `unlistedRequests`) and `send` (queued: a new turn
on an idle thread, or after a running one's current turn ends, never a
steer: [execution.md](execution.md#t3-interaction));
OpenCode offers `respond` (its permissions and the forms Stage can show,
when its OpenAPI has the reply operations; other forms are answered in
OpenCode, its descriptor's `unlistedRequests`) and `send` (queued: a
running session takes it when its turn ends, never mid-turn; both when its
OpenAPI has the operations: [execution.md](execution.md#opencode-interaction)); Codex offers `respond` (command and file-change approvals and its `requestUserInput` questions; permission requests, MCP elicitations and secret questions are answered in Codex, its descriptor's `unlistedRequests`) and `send` (a new turn on an idle thread; busy while a turn runs: [execution.md](execution.md#codex-adapter)). The app answers and sends on task detail
(answers also on needs-input rows); agents use `list_requests`,
`respond_request` and `send_message` (CLI `task requests|respond|send`).
An answer or message whose `Stage-Caller-Session` belongs to the task gets 403
`{"code": "self_action_forbidden"}`: an agent never answers or messages its
own session. Best effort: a request naming no caller session passes. Every
agent route resolves `Stage-Caller-Session` once; a send or respond records
it as its journal event's `cause.session`, with the target task in
`cause.taskId` (`cause.kind` `session_message` or `session_request`).

**First answer wins** ([ADR 0013](adr/0013-tree-steering.md#first-answer-wins)).
Both routes take an optional `expectedStateRevision`: the task's
`stateRevision` the caller is answering. With it the answer is guarded:
Stage requires the task in `needs_input` at that revision and not yet
answered there, holding the task's lock through the harness call, else 409
`{"code": "already_answered", "error", "state", "stateRevision"}` (the task's
current state and revision; nothing was sent). The winner records the
revision as answered. The app's Reply and Requests pass it; agents pass it
through `send_message`/`respond_request` `expectedStateRevision` (CLI
`--expect-revision`). Without it nothing changes: follow-ups to done
sessions keep working. An answer typed directly in the harness cannot be
prevented; Stage sees it only as the task's next state.

- `GET /api/tasks/:id/requests` (operation `respond`; humans and agents, the caller's own task included): 200 `{"requests": [{id, kind: approval|question|input, title, detail?, preview?, options?: [{id, label, available, reason?}], textOption?, questions?: [{id, header?, prompt, options?: [{label, description?, preview?}], multi?, custom?}], createdAt}]}`. An option's `preview`: preformatted text it shows (monospaced). `preview`: the command, path, URL or input the request acts on (monospaced). `textOption`: the option whose answer may carry `text`. A `question` request lists its `questions`, answered together. Claude Code: approvals offer `allow`, `always:<i>` (one per Claude suggestion; the label names the rule and where Claude writes it) and `deny` (`textOption`; Claude reads "Denied in Stage: <reason>", or "Denied in Stage." without one); `AskUserQuestion` is a question request whose questions are all `custom`. A session with no open hook (answered in Claude, adopted, launched before Stage could answer) lists none: answer it with the resume command. T3: each pending user-input request is a `question` request (title "T3 asks", or "T3 asks N questions"; `custom` unless T3 disallows free answers; `createdAt` is when this Stage process first listed it, T3 does not report one); permission requests are not listed (approve them in T3). OpenCode: permissions are approvals offering `once` "Allow once", `always` "Allow always" (only when OpenCode would save a rule for it) and `reject` "Deny" (`textOption`, sent as OpenCode's message), titled by OpenCode's action description; a form whose fields Stage can show (string, multiselect, boolean as Yes/No, number or integer) is a `question` request keyed by field key ("OpenCode asks" for the agent's questions, else the form's title); forms with conditions, hidden or external fields are not listed (answer them in OpenCode); `createdAt` is zero. Codex: a command or file-change approval is keyed by its `approvalId` (else `itemId`) and offers the plain decisions Codex names, in its order: `accept`, `acceptForSession`, `decline`, `cancel` ("Run command", "Run, and don't ask again for this command", "Don't run", "Stop the turn"; for files "Apply edits", "Apply, and don't ask again for these files", "Don't apply", "Stop the turn"; a file change offers all four), titled "Run a command" (preview: the command, detail: Codex's reason) or "Edit files" (Codex shares the diff only with the session that made it, so the detail says to read it with the resume command); decisions carrying an exec policy or network amendment stay in Codex. A `requestUserInput` is a `question` request keyed by its `itemId` ("Codex asks", or "Codex asks N questions"; `custom` when Codex allows other answers or offers no options); one with a secret question, permission requests and MCP elicitations are not listed (answer them in Codex with the resume command).
- `POST /api/tasks/:id/requests/:rid` body `{"option"?: string, "text"?: string, "answers"?: {questionId: string[]}}` (one required; `answers` for a question request: every question, option labels or one custom text, several only when `multi`): answers the request, then polls the session once. 200 `Task`; 400 (also an answer that does not fit the request); 403 `{"code": "self_action_forbidden"}` when the caller session owns the task; 404 the request is no longer pending (answered in the harness, superseded or expired); 409 `already_answered` for a guarded answer that lost.
- `POST /api/tasks/:id/messages` body `{"text": string, "delivery"?: Delivery, "expectedStateRevision"?: number}`. Text must be nonblank and at most 32 KiB. Each delivery mode gates on its same-named connection operation.

  | Delivery | Turn | Timeline event |
  |---|---|---|
  | `send` (also omitted or empty) | Start a new turn after the previous one ends. T3 and OpenCode currently also accept a follow-up into their native queues while running. | Follow-up sent from Stage |
  | `steer` | Inject into the running turn. | Steer sent from Stage |
  | `queue` | While a turn runs, let the harness hold the message and start the next turn with it. | Message queued from Stage |

  A successful delivery records its event without the text, polls the session once and returns 200 `Task`. Unknown delivery or invalid input returns 400. An agent's own-session write returns 403 `self_action_forbidden`. A missing operation returns 409 `operation_unavailable` with its reason. A turn or session unable to take the message now returns 409 `session_busy` with guidance. A final harness refusal, such as an archived thread, returns 409 `session_refused`. A guarded message that lost returns 409 `already_answered`. Stage assigns a fresh request ID for harnesses that deduplicate delivery.

- `GET /api/tasks/:id/activity?attempt=N&limit=N` (operation `activity`, the caller's own task included). Returns 200 `{"activity": {items, plan, files, model, truncated, details?, at}}` for the selected session. Omit `attempt` for the current session, or pass a nonnegative attempt number. 404 when the attempt has no recorded session. Stage reads the selected ref through its connection's declared operation. A nonnegative integer limit defaults to 30 when omitted or zero and caps at 100. Items are the newest N, oldest first. Each item has `id`, `at`, `kind` (`message_user`, `message_agent`, `reasoning`, `tool`, `command`, `file_change`, `plan`, `event`), `title` (up to 200 runes), `text` (up to 2000 runes), `status` (`running`, `done`, `failed` or empty) and `preview` (command, path or URL). `plan` is the current list of `{text, status}` with `pending`, `in_progress` or `done`. `files` lists changed files as `{path, additions, deletions, status}` with `added`, `modified`, `deleted` or empty. `model` is the current model label and `at` is the snapshot time. `truncated` means older items exist or harness-specific `details` was dropped for exceeding 16 KiB. Invalid adapter data returns 502. Stage reads this view live from the harness.

### `POST /api/tasks/:id/duplicate`
New `backlog` task with the same project, title, prompt, images (copied), execution config (launch options included); no copied notes, no tree, no needs, no start time, no schedule.
An optional JSON draft takes `title`, `prompt`, `connectionId`, `workspaceId`, and optional `projectId`, `executionConfig`, and `attachmentIds` under the create rules. For a draft, `attachmentIds` selects source images to copy and staged images to attach. Both forms record the source link in the copy's journal.
201 `Task`. 404. 409 for an adopted task.

## Journals

Task and tree journals contain immutable notes and events. Task notes belong to the task; a tree read combines its own notes/events with notes of its current members. Moving a task moves its notes into the destination aggregate. Tree notes and historical events stay in their original tree. Move events in both trees carry `fromTreeId` and `toTreeId`.

API v2 removes task `notes`. Create and adopt accept `initialNote`; PATCH rejects both `notes` and `initialNote`. Schedule `notes` remains a template, appended as each spawned task's initial note. New or edited templates must be UTF-8 and at most 16 KiB; whitespace-only templates produce no note. Existing oversized templates remain unchanged and spawn numbered notes in original text order, each within 16 KiB including its part heading, preserving all text and UTF-8 boundaries. Duplicate copies no notes and records a source-link event. Old task notes are discarded, not imported. Existing task event history remains available; accurate historical tree events begin at rollout, without reconstructing past membership.

### `GET /api/tasks/:id/journal`, `GET /api/trees/:id/journal`

200 `{ "entries": JournalEntry[], "nextCursor": string | null }`, newest immutable ID first. Query: `kind=notes|events|all` (default `notes`), exact `category`, text substring `q`, `cursor`, `limit` (default 20, range 1..100). `taskId` is a tree-only filter matching entry task or source task. Empty category means no category filter. Entries preview at most 600 Unicode runes; `truncated` identifies previews. Cursor is opaque and bound to owner and filters. A tree membership change invalidates it with 409; restart from the first page. 400 invalid query/cursor, 404 owner missing.

```json
{
  "id": 123,
  "kind": "note",
  "taskId": null,
  "treeId": 3,
  "sourceTaskId": 12,
  "sourceSession": { "connectionId": 1, "externalSessionId": "session-abc" },
  "attempt": null,
  "category": "decision",
  "text": "Keep the public contract stable.",
  "eventType": "",
  "fromTreeId": null,
  "toTreeId": null,
  "createdAt": "2026-11-16T10:00:00Z",
  "createdBy": 1,
  "sourceMachineId": null,
  "truncated": false
}
```

`kind` is `note|event`. `createdBy`: the note's author, or the user behind an event Stage writes directly (acceptance, duplicate); derived events (task state, tree, membership and needs; [storage.md](storage.md#journal-events)) carry the default user. For notes, exactly one of `taskId` and `treeId` denotes ownership. Task events can also carry a `treeId` snapshot; tree events have `treeId` and optional `sourceTaskId`. `sourceSession` is the scoped session that authored the note or produced the event, or null when no source is known. Provenance and IDs never change. Empty `category` means uncategorized; `eventType` is empty for notes. Nullable provenance fields are always emitted.

### `GET /api/journal/:id`

200 full `JournalEntry`, with `truncated: false`. 404 missing entry.

### `POST /api/tasks/:id/journal`, `POST /api/trees/:id/journal`

Body `{ "text": string, "requestId": string, "category"?: string, "sourceTaskId"?: number, "sourceSession"?: {connectionId, externalSessionId} }`. Text must be nonblank UTF-8, at most 16 KiB. Category is optional: empty (uncategorized), `finding`, `decision`, `blocker`, or `outcome`; other values are 400 for append and reads. `requestId` is required and scoped to the owner: retry identical content and provenance to return the existing entry; different content with the same key is 409. A tree note's `sourceTaskId` must currently belong to that tree, or be the tree's active steerer; a committed retry still succeeds after it moves. 201 full entry for both append and retry; 400 invalid input, 404 missing owner/source, 409 stale provenance or conflicting retry.

Appending is allowed on closed work. It records context without reopening, scheduling, launching, or sending a session message. There is no edit/delete note endpoint. Notes and execution protect their task and associated tree from deletion, including after the task leaves.

## Trees

A tree is a DAG of tasks: each member's `needs` are members that must be completed by the user in Stage before it starts. It is the only way to express dependencies. A task is in at most one tree; trees may span projects. Rules in "Tree rules".

`Tree`:
```json
{ "id": 3, "title": "Ship auth", "startMode": "auto", "status": "open", "closedAt": null, "createdAt": "2026-11-08T09:59:00Z", "createdBy": 1, "sourceMachineId": null, "updatedAt": "2026-11-08T10:00:00Z", "deletable": false, "steerer": null, "steeringHoldSeconds": 300 }
```
`steeringHoldSeconds`: how long a member's needs-input notification waits for the tree's steerer (0 off, at most 3600, default 300; see "Notification rules").
`steerer`: the tree's active steerer (see "Tree steering"), else null: `{ "taskId", "title", "state" (the steerer task's), "since" (bound at), "deliveredThrough" (the last event id a digest covered; starts at the binding's `steering_started`, which the launch brief covers), "lastDeliveredAt" (last digest sent, null before any), "pending" (the binding's events after `deliveredThrough` not caused by the steerer: what the next digest carries), "lastError" (why the last delivery failed, `steerer not running` while the steerer can't take updates, null after a delivery) }`.
`status`: `open | done | archived` (see "Done, archive and delete"); `closedAt`: when it was marked done or archived (null while open); `deletable`: the tree and every member have no recorded work and no member started (`DELETE` removes the tree and its tasks).
`TreeDetail` = `Tree` + `TreeTimes` + `{ "tasks": Task[] (by id), "edges": [{ "taskId": 9, "needId": 5 }], "levels": [[5, 7], [9]], "events": TaskEvent[] }`. `levels`: member ids per level, roots first; a task's level is the longest need path to a root; each level by id. `events`: the members' 50 newest task events, newest first (`{ id, taskId, fromState, toState, detail, at }`), retained temporarily for compatibility. New tree Activity reads the journal.

`TreeTimes` = `{ "startedAt": string | null, "lastActivityAt": string }`. `startedAt`: the earliest member `launchedAt` (null before any launch). `lastActivityAt`: the latest tree metadata, journal entry, current member update or member session update (so an agent working counts as activity).

`TreeSummary` = `Tree` + `TreeTimes` + `{ "counts": TreeCounts, "levels": number, "projects": string[] (member project titles, sorted), "completedAt": string | null }`. `counts`: `{ total, backlog, blocked, ready, launching, running, needsInput, done, completed, failed, interrupted, cancelled }` (`blocked` and `ready` split `backlog`). `completed` counts accepted members. `completedAt` is the latest member acceptance once every member is accepted, or null.

### `GET /api/trees`
Query (all optional): `status` (`active` default: open trees with an unaccepted member; `done`: marked done, or open with every member accepted; `archived`; `all`; else 400), `all=1` (= `status=all`), `completedFrom` (with `active`: also completed open trees whose latest acceptance is at or after it), `project` (a member is in that project). 200 `{ "trees": TreeSummary[] }`, `lastActivityAt` desc.

### `POST /api/trees`
Body `{ "title": string, "startMode"?: StartMode, "tasks": [{ "taskId": number, "needs"?: number[] }] }`: creates the tree, moves each listed task into it (a member of another tree is spliced out there), then sets each listed `needs` (omitted keeps the task's needs). One transaction. 201 `TreeDetail`. 400 blank title, no tasks, a task listed twice, bad `startMode`. 404 task / need. 409 tree rule broken.

### `GET /api/trees/:id`
200 `TreeDetail`. 404.

### `PATCH /api/trees/:id`
Body: any of `title`, `startMode` (`auto|manual`), `status` (`open|done|archived`; applied first), `steeringHoldSeconds` (0 to 3600; a change journals `steering_hold_changed`). 200 `TreeDetail`. 400. 404. Switching to `manual` makes released members ready (each notifies once); switching to `auto` launches them on the next reconcile. `status` `done` or `archived` 409 while a member is `launching|running|needs_input` (`#N is running: stop it first`); `startMode` 409 on a done or archived tree; `open` reopens or unarchives.

### `DELETE /api/trees/:id`
Deletes the tree and every member task only when `deletable`: no recorded work in the tree or its members and no member ever started. 204. 404. 409 otherwise (archive instead), and while the tree has an active steerer (stop steering first). Protection survives a task leaving the tree.

### `POST /api/trees/:id/dissolve`
Detaches the tasks, clears their needs, and archives the tree. Its identity and journal remain readable. 204. 404.

### `POST /api/trees/:id/tasks`
Body `{ "taskId": number, "needs"?: number[] }`: adds the task (a member of another tree moves: it is spliced out there and loses its needs), then sets `needs` when given. 200 `TreeDetail`. 404 tree / task / need. 409 tree rule broken.

### `DELETE /api/trees/:id/tasks/:taskId`
Splices the task out: its dependents take over its needs; the task stays, standalone. An emptied tree is archived. 204. 404 (also when the task is not in that tree).

### `POST /api/trees/:id/start`
"Start ready": starts every `ready` member (same as `POST /api/tasks/:id/start` each). 200 `{ "started": Task[], "errors": [{ "taskId": number, "error": string }] }`. 404. 409 on a done or archived tree.

## Tree steering

A tree can have one steerer ([ADR 0013](adr/0013-tree-steering.md)): an ordinary standalone task bound to it that hears about the tree's members and acts on them (answering, accepting) with the operations any agent has. It is never a member: a task cannot join the tree it steers (400 `{error, code: "steerer_not_member"}` on create, add, needs and a pull-in through needs), and a task steers at most one tree. While a binding is active, each member change enqueues a steering event in the same transaction as the change; nothing is enqueued without one. While a live steerer can answer, a member's needs-input notification is held (see "Notification rules"). Stage delivers them to the steerer's session as digests (see "Steering delivery") and finishes the steerer with its tree; the steerer can also read the feed.

### Steering brief

When an active steerer launches, Stage appends a `<stage-steering-brief tree=N>` block after the prompt (and any handoff) and before the journal block. It holds the tree's id, title, start mode and status; each member's id, title, state, readiness, needs, `stateRevision`, reported branch and last word (at most 300 characters); the tree's 20 newest notes (previews of at most 300 characters); the steering rules (which Stage interface to use with HTTP equivalents, answering with `expectedStateRevision`, accepting through `get_completion_preview` then `complete_task`, follow-ups through `create_task` with `treeId` and `needs`, decisions as tree notes with `sourceTaskId`, escalation, never acting on its own task, `<stage-steering tree=N through=N>` updates and `get_steering_events after=N`, the outcome note when the tree finishes); then the playbook. The brief is at most 24 KiB: past that the oldest notes go first, then the last members, each cut ending with a pointer to `read_journal` or `get_tree`, and an oversized playbook is cut with a pointer to its snippet. It is frozen per launch attempt: a retry of the same attempt reuses it and a new attempt recomposes it. Task detail returns it as `currentSteering`.

The playbook is the snippet named `steering-playbook` (`GET /api/snippets`). The first composition creates it as a global snippet from Stage's default (working with tasks, steering toward the user's intent, decisions, reviews, merging, follow-ups, when to ask the user) when no snippet has that name; edit it to change what later briefs carry. A snippet of that name in any scope is used as is; a deleted one is created again from the default. Stage enforces none of it.

### `POST /api/trees/:id/steerer`
Body `{ "prompt": string, "title"?: string, "connectionId": number, "workspaceId": string, "executionConfig"?: ExecutionConfig, "replace"?: boolean, "requestId": string }`. Creates a standalone task (title default `Steer: <tree title>`, the prompt as given), binds it and launches it. 201 `{ "tree": TreeDetail, "task": Task, "warnings": string[] }`. `requestId` is required and scoped to the tree: retrying identical content returns the existing steerer (200, nothing launched again); other content with the same key is 409. 400 missing fields or destination. 404 tree. 409: the tree is done or archived; `steerer_exists` while one is active and `replace` is not set; `operation_unavailable` with the harness's reason when the connection's `send` operation is unavailable (a steerer needs send). `replace` ends the current binding (`replaced`), stops the old steerer's session when it is active and the harness offers stop, then launches the new task; a failed stop still ends the binding and returns a warning. A launch failure leaves the bound task failed with `lastError` and a warning.

### `DELETE /api/trees/:id/steerer`
Ends the binding (`stopped`) and stops the steerer's session as above. 200 `{ "tree": TreeDetail, "warnings": string[] }`. 404 tree. 409 `no_steerer` when none is active.

### `GET /api/trees/:id/steering/events`
Query `after` (event id, default 0) and `limit` (1 to 200, default 50). 200 `{ "events": SteeringEvent[], "hasMore": boolean }`, ascending by id, every event of the tree's bindings (past ones too). 400 bad query. 404 tree. Ids are monotonic per tree: a cursor never skips an event committed later.

`SteeringEvent`:
```json
{ "id": 41, "treeId": 3, "steererId": 2, "kind": "member_needs_input", "taskId": 9, "payload": { "title": "Build API", "stateRevision": 4 },
  "causedBy": { "session": { "connectionId": 1, "externalSessionId": "t-abc" }, "taskId": 12 }, "self": true, "createdAt": "2026-11-28T10:00:00Z" }
```
- `kind` and `payload` (every member event carries the member's `title`):
  - `member_needs_input` (`stateRevision`, the revision a guarded answer names), `member_done` (`reply`, at most 600 characters, and `replyTruncated`), `member_failed` and `member_interrupted` (`error` when set), `member_cancelled`: the member entered that state.
  - `member_ready`: a manual member's needs were all completed (its `ready: needs completed` episode).
  - `member_added`, `member_removed`: membership changed.
  - `member_accepted` (`acceptanceId`, `revision`), `member_reopened` (`revision`).
  - `member_answered` (`via`: `respond` with `requestId`, or `send`): an answer through Stage.
  - `note` (`noteId`, `category`, `text` at most 600 characters, `truncated`): a `blocker` or `decision` note on a member or on the tree.
  - `hold_escalated` (`stateRevision`, `held`: a hold was released; `reason` from `escalate_to_user`, or `timeout: true` when the hold expired): the user was notified about the member's wait.
  - `tree_status` (`from`, `to`), `steering_started`, `steering_ended` (`reason`); `taskId` is the steerer for these two.
  - `tree_finished` (`reason`: `members_completed`, `tree_done` or `tree_archived`): once per binding, with no cause, when every member has acceptance (also when the last open member leaves), or the tree is marked done or archived.
- `causedBy`: the caller session that made the change (`Stage-Caller-Session`) and the task that owns it; null for harness observations, the user's own actions and the server's. A dependent made ready inherits the cause of the acceptance that released it. `self`: caused by the binding's steerer task.

### `POST /api/tasks/:id/escalate`
Body `{ "reason": string }` (nonblank, at most 500 characters). `escalate_to_user`: the tree's steerer hands a member's wait to the user now. Allowed only for a request whose `Stage-Caller-Session` belongs to the active steerer of the task's tree; otherwise 403 `{ "code": "not_steerer" }`. It releases the task's held notification at once, or records a new one when the task needs input and nothing is held; the notification's `note` is `Steerer: <reason>`. Enqueues `hold_escalated`. 200 `Task`. 400 reason. 404 task. 409 `not_waiting` when nothing is held and the task does not need input (or that wait was already answered).

The tree journal records `steering_started`, `steering_replaced` and `steering_ended` events. An acceptance whose caller is the tree's active steerer reads `Completed by steerer #N` (`Reopened by steerer #N`). Archiving or deleting the steerer task ends its binding (`task_removed`).

### Steering delivery
A server dispatcher sends each active binding's pending events (after `deliveredThrough`, not caused by the steerer) into the steerer's session as one message, through the same send as `send_message` but as Stage itself:

- **When**: 10 s after the newest pending event, or 60 s after the oldest, whichever comes first, timed from when the server first saw them (a restart starts the windows again). One delivery in flight per tree. Only while the steerer task is `running`, `done` or `needs_input`; otherwise nothing is sent and `lastError` reads `steerer not running`.
- **Format**: `<stage-steering tree="ID" through="N">`, one line per event, oldest first (`#id task #T "title" kind: details`, with the tools that act on it), then `</stage-steering>`. A `member_needs_input` line names the `stateRevision` to answer at and the member's requests from `list_requests` when its harness offers respond (else `no listable requests: reply with send_message, or escalate_to_user`); a member answered or moved on since says so. While the steerer itself needs input, the digest ends with a reminder that its question to the user is still open. At most 32 KiB: lines that do not fit end in `K more events (#a-#b): get_steering_events {treeId: ID, after: X}`, and `through` still covers them. `through` lets a steerer skip a digest it has seen.
- **Delivery**: at least once, with request ID `steering-<binding>-<through>` (deduplicated by T3, OpenCode and Codex). Success sets `deliveredThrough`, `lastDeliveredAt`, clears `lastError` and records "Steering update sent (through #N)" on the steerer's timeline. `session_busy` keeps the events pending for the next pass; other errors set `lastError` and back off from 10 s, doubling, up to 5 minutes.
- **Tree finish**: on `tree_finished` the final digest goes out with no debounce once the steerer's current turn has ended, telling it to write an outcome note and end; the binding then ends (`tree_finished`). A steerer not running at that point ends at once, with no digest. When the steerer next reaches `done` after the final digest, Stage completes it through the acceptance path (provenance `user`, actor `system`, event "Completed automatically: tree #N finished"), once; a `failed`, `interrupted` or `cancelled` run, or an acceptance someone else made first, settles it without completing. A person can reopen it; it is never completed again, and a reopened tree does not resume the ended binding.

## Schedules

A schedule is a recurring task template: on each cron fire it spawns a new task (`scheduleId` set) with its config and images and starts it. One-shot starts are not schedules; they are `startAt` on a backlog task.

`Schedule`:
```json
{
  "id": 3,
  "projectId": 1,
  "projectTitle": "stage",
  "title": "Nightly deps bump",
  "prompt": "bump deps, run tests",
  "connectionId": 1,
  "workspaceId": "ws_...",
  "executionConfig": { "model": "[\"claude\",\"claude-sonnet-5-5\"]", "options": [{ "id": "worktree", "value": true }, { "id": "baseRef", "value": "origin/main" }], "permission": "full-access", "mode": "" },
  "notes": "",
  "attachments": [],
  "cron": "0 2 * * *",
  "tz": "America/New_York",
  "paused": false,
  "nextRunAt": "2026-11-11T20:30:00Z",
  "lastRunAt": "2026-11-10T20:30:00Z",
  "lastTaskId": 41,
  "lastTaskState": "done",
  "originSession": null,
  "createdAt": "2026-11-08T09:59:00Z",
  "createdBy": 1,
  "sourceMachineId": null,
  "updatedAt": "2026-11-08T10:00:00Z"
}
```
- `createdBy`: the schedule's creator. Every task it spawns (cron fire or run now) and that task's initial notes are attributed to this user, not the caller.
- `cron`: standard 5-field expression (minute hour day-of-month month day-of-week; `@hourly|@daily|@weekly|@monthly|@yearly` also accepted). `@every` and `CRON_TZ=`/`TZ=` prefixes are rejected (400). Evaluated in `tz` (IANA), so DST is handled by the zone.
- `nextRunAt`: next fire (UTC); null while `paused`. `lastRunAt`: fire time of the newest run row (any outcome); null before the first.
- `lastTaskId` / `lastTaskState`: newest task this schedule spawned (null before the first).
- `originSession`: the session that created the schedule (agent provenance; set on create only). Spawned tasks inherit it as their `originSession`.

`ScheduleRun`:
```json
{ "id": 9, "scheduleId": 3, "fireAt": "2026-11-10T20:30:00Z", "outcome": "started", "taskId": 41, "detail": "", "at": "2026-11-10T20:30:04Z", "task": null }
```
- `outcome`: `started` (task spawned; launch result lives on the task), `skipped` (previous run still active), `missed` (server was down across the fire time; one row per outage), `manual` (Run now).
- `task`: the spawned `Task` on detail reads, else null.

`ScheduleDetail` = `Schedule` + `{ "runs": ScheduleRun[] }` (newest first, at most 50, each with `task`).

### `GET /api/schedules`
Query (optional): `project` (project id), `dueWithin` (Go duration, e.g. `24h`: only unpaused schedules with `nextRunAt <= now + dueWithin`).
200 `{ "schedules": Schedule[] }` ordered by `nextRunAt` asc (paused last), then id.

### `POST /api/schedules`
Body `CreateScheduleRequest`: shared task config (`projectId connectionId workspaceId title prompt executionConfig attachmentIds originSession`), `notes` (initial-note template for each spawned task), required `cron` and `tz`, optional `paused` (default false). Example: `"cron": "0 2 * * *", "tz": "America/New_York"`. `initialNote`, `treeId`, `needs`, `needsSession`, `startMode`, `start`, `startLocal`, `startTz` are 400; spawned tasks are standalone.
`connectionId` and `workspaceId` are required. 201 `Schedule`. 400 (invalid cron or zone, plus task field rules). 404 project. A missing destination returns 409 `destination_required`.

### `GET /api/schedules/:id`
200 `ScheduleDetail`. 404.

### `PATCH /api/schedules/:id`
Body: any of the create fields (`attachmentIds` is the exact image set, as for tasks). `bindingId` is rejected with 400. A null `workspaceId` returns 409 `destination_required`. Changing `cron`, `tz` or `paused` recomputes `nextRunAt` from now (resuming never fires a missed time). Spawned tasks are not changed.
200 `Schedule`. 400. 404.

### `DELETE /api/schedules/:id`
204. Spawned tasks are kept (their `scheduleId` becomes null). 404.

### `POST /api/schedules/:id/run`
Run now: spawn a task and start it immediately, ignoring `paused` and the overlap rule; records a `manual` run; `nextRunAt` unchanged. Launch errors land on the task (`failed` + `lastError`).
201 `Task`. 404. A missing or unavailable destination returns 409 with repair guidance.

### `POST /api/schedules/preview`
Body `{ "cron": "0 2 * * 1-5", "tz": "America/New_York" }`. 200 `{ "next": [ISO, ISO, ISO] }` (next three fires, UTC). 400 with the parse error message.

### Schedule rules
- The scheduler ticks every 15s (and on start).
- One-shot: a `backlog` task with `startAt <= now` is launched (`startAt` cleared); the `launching` event detail reads `scheduled start` or `scheduled start, late by 12m` when more than a minute late (e.g. server was down: late starts are not skipped). Manual Start now also clears `startAt`.
- Recurring, per unpaused schedule with `nextRunAt <= now`, in one transaction (holds the database write lock):
  - `now - nextRunAt > 2m` (server was down): record one `missed` run (`detail`: `missed N fires`), advance `nextRunAt` to the first fire after now. Nothing starts.
  - A previous spawned task still `backlog` (pending launch), `launching`, `running` or `needs_input`: record `skipped` (`detail`: `#41 still running`), advance. A task in `done` does not block a schedule, even while awaiting acceptance.
  - A missing or unavailable workspace pin records a `skipped` run with the repair reason and advances `nextRunAt`.
  - Else spawn a task (config + images copied, `scheduleId`, `startAt = fireAt`, `startTz = tz`), record `started`, advance. The one-shot pass then launches it (a crash in between still launches it on restart).
- Fires are computed in the schedule's `tz`; a fire is never run twice (`nextRunAt` advances in the same transaction).
- A cron fire that spawns a task notifies `schedule_started` ("Schedule started"); a fire recorded `skipped` or `missed` notifies `schedule_skipped` ("Schedule skipped"). Run now never notifies. Both use group `schedule:<id>`, tag `schedule-<id>` and `url` `/schedules/<id>`. A failed launch notifies as usual (`failed`).

## Images

`Attachment`: `{ "id": 4, "name": "error.png", "mimeType": "image/png", "sizeBytes": 48211 }`. Types: `image/png|jpeg|gif|webp` (sniffed from the bytes), max 10 MB each, max 8 per task or follow-up. Stored in the database. An upload is *staged* until a task create/update attaches it or a follow-up sends it; staged uploads older than 24h are deleted hourly. A launch delivers a task's images the harness's way ([execution.md](execution.md): T3 uploads, Codex input items, Claude Code files); images are accepted only where the connection's `images` operation is available (`GET /api/connections/:connectionId/capabilities`, scoped with `workspaceId`): a task or schedule create with `attachmentIds`, an update that adds an image, and an update that moves images to another connection are 400 with the operation's reason elsewhere, and answer the harness error (502; 409 `connection_unavailable` without an adapter) when its capabilities can't be read. The MCP tools gate the same way.

### `POST /api/uploads`
Multipart form, field `file` (one image). 201 `Attachment` (staged). 400 not an accepted image type, empty, over 10 MB.

### `GET /api/attachments/:id`
200 raw image bytes (`Content-Type` = `mimeType`, immutable cache, `nosniff`). 404.

### `DELETE /api/attachments/:id`
Deletes a staged upload, or an image of a `backlog` task. 204. 404. 409 when the task has left backlog (images are read-only after launch).

## Session search

### `GET /api/connections/:connectionId/sessions`

The personal operator and active shared members and admins can browse a
connection that offers `sessionSearch`. The human route admits each page
before calling the owning adapter, including through its worker. Agent
credentials, private agent sockets and worker agent gateways receive 403
in claimed mode. Setup pending receives 403 and missing or expired shared
credentials receive 401. This route has no MCP equivalent.

Optional query parameters are `q` (up to 256 bytes), `limit` (1–50, default
20) and `cursor`. The response is `{sessions, nextCursor?}`. Each entry
has `session: {connectionId, externalSessionId}`, optional `title`,
`workspace` and `startedAt`, `eligibility: {available, reason?}`, and
`trackedTaskId` when a task already owns that exact session. Eligibility
is preliminary. Use the named session routes to preview and adopt.

Send the same connection, query and limit on each page. Each cursor is
opaque, scoped to the admitted account and search, and valid for five
minutes or until the server restarts. A 400 `invalid_session_search_cursor`
means to start a fresh search. A 409 `operation_unavailable` gives the
reason search is unsupported or requires a worker update. A 503
`session_search_unavailable` indicates an unavailable harness or worker.
A 502 `session_search_failed` indicates a failed search. Direct link or
ID entry is still available under its existing policy.

The current harness roster determines the results. Listed completed sessions
can appear. T3, Codex and OpenCode may return a short or empty filtered
page with a continuation. Follow `nextCursor` to continue searching.
[Search scope and ordering by harness](execution.md#session-search)

Browsing creates no task, observation, subscription or notification.

## Explicit session lookup and adoption

`GET /api/connections/:connectionId/sessions/:id/context` (see Agent interface) is the session read; API v8 removed the T3 thread read `GET /api/connections/:connectionId/sessions/:id` (404).

`POST /api/connections/:connectionId/sessions/:id/adopt` accepts `{title?, initialNote?, treeId?, projectId?, newProjectTitle?}`. Returns Task: 201 new, 200 existing owner. Revalidates current eligibility; never creates a second task for a session. Archived and child sessions cannot be newly adopted. Stage refuses a session another registered harness wraps (its origin among that harness's `wraps`): 409 `session_refused` with where to track it (a Claude Code session T3 started: "This Claude Code session was started by T3. Track it through T3."). Session context lookups and resolving such a session from a pasted link or ID (`GET /api/refs/resolve`) answer the same 409. The UI previews before confirming. See Adopt session below.

For a human tracking a verified workspace with no project selection, Stage
reuses its existing project or records a stable historical project. Current
group membership can supply the initial historical project. This
requires no launch pin. A preview leaves tasks, notifications and read
receipts unchanged. Scoped callers can still adopt a named session subject
to the existing workspace verification and adoption checks. Use
`projectId` or `newProjectTitle` when project context is needed. Concurrent confirmations
return one tracked task and record the creation under the winning requester.

### `GET /api/tasks/:id/opening-prompt`

Adopted tasks only (400 otherwise). Returns `{item: {itemId, runId, text, truncated, updatedAt} | null}` with the first own user message, skipping inherited history. Default 2000-character preview. `full=true` expands only that opening message, bounded to 1,000,000 characters. A missing task or session returns 404. A connection that declares opening prompts unavailable returns 409 `operation_unavailable` with its reason. The task's cached final reply is independent of this live read.

## API v9 changes

The server and workers roll forward together at API v9. Layer 07 raises the independent worker protocol from 2 to 3. A legacy worker shows **Needs update** until upgraded. Later breaking changes in this coordinated harness foundation series also ship as v9.

- Harness descriptors expose `localProcesses`, `sessions: "machine" | "cloud"` and `stateReasons`. Use `localProcesses` for placement decisions formerly based on `hosting`.

- Tasks use `done` for finished execution and `completedAt` for acceptance. Tasks drop `settledAt` and `artifactLinks`. Task and tree lists accept `completedFrom`. Tree summaries and MCP tree rows use `completedAt` and `completed` in their counts.
- Journal and handoff records drop `sourceThreadId`. Use `sourceSession` on journal entries and `session` on handoffs. Accepted evidence uses `externalSessionId` and `sessionLink` and drops `artifacts` and `historicalSettledAt`. The storage reader handles older evidence.
- Session links drop `service` and `app`. `finalReply`, the opening prompt's `item` and observation `reply` drop `messageId` and `position`. Observations drop `modelLabel` and `artifacts` on push and the worker channel.
- `POST /api/sync` returns `sessions`. The T3 setup status keeps its own `details.sync.threads` counter.
- External notification groups use `session:<connectionId>:<externalSessionId>`. Push and unread tags use `session-<connectionId>:<externalSessionId>`. Previously shown notifications with a `thread-` tag stay until dismissed. Acceptance sends no notification. Migration 0035 maps old task states and notification events to `done` while preserving acceptance history.
- MCP `get_task` describes session identity, `stop_task` interrupts a running session, and `list_tasks.states` accepts the current TaskState values. MCP and CLI handoff help names the final reply, branch and session link. `stage task show` drops its artifact lines, `stage show` uses web and native links, and the CLI journal source line uses `connection N session X` for a scoped source session.
- Notification subjects (additive, no version bump): events `completed`, `tree_completed`, `schedule_started` and `schedule_skipped`; `Notification.treeId` and `scheduleId`; groups `tree:<id>` and `schedule:<id>` with tags `tree-<id>` and `schedule-<id>`. A task's finished run no longer notifies (`done` is for untracked sessions); acceptance by inference or an agent notifies `completed` (see "Notification rules"). Migration 0056 adds `completed`, `tree_completed` and `schedule_skipped` to saved filters.
- Session rename (additive, no version bump): the `sessionRename` operation, and a task title change renames the session Stage launched where available (`PATCH /api/tasks/:id`).
- Session surface contracts (additive, no version bump, [ADR 0014](adr/0014-session-surface.md)). Connections declare `steer`, `queue` and `activity`. Session messages accept `delivery` and the activity route returns a bounded session view. Worker kinds `adapter.deliver` and `adapter.activity` gate the new calls. MCP and CLI expose the same contracts.
- Task session history (additive, no version bump). Migration 0057 preserves session links by attempt. Task detail and MCP `get_task` expose `sessions`, and `GET /api/tasks/:id/sessions/:attempt` reads the cached snapshot. Activity accepts an attempt over HTTP, MCP and CLI. Historical session references return the owning task and attempt.
- Needs-input task notifications store and push `/tasks/<id>/session`. Task group keys and push tags keep the same IDs. Historical notification records keep their stored URLs.
- Agent operation parity (additive, no version bump; [ADR 0011](adr/0011-agent-operation-parity.md)): agent callers may complete, reopen, list and answer requests, send messages, read opening prompts, create, rename and archive projects, read project events, edit group membership, manage snippets and delete attachments. 403 `agent_forbidden` on respond and send is now `self_action_forbidden`, only for the caller's own task. `PATCH /api/projects/:id` keeps omitted fields.

- Workspace add (additive, no version bump; [ADR 0012](adr/0012-harness-workspace-add.md)): `POST /api/connections/:connectionId/workspaces`, capabilities `workspaceSources` and operation `workspaceAdd`, field kind `path`, MCP `list_workspace_sources` and `add_workspace`, CLI `stage workspaces sources|add`. Project events add type `workspace_added` and `actor`, `cause` and `sourceMachineId`. Workers relay `adapter.workspaceAdd` behind the `workspaceAdder` trait without a protocol change.

Task detail and MCP `get_task` omit the former `batch` field. To list tasks created by a session, use `originConnectionId` and `originSessionId` on `GET /api/tasks` or `list_tasks {originSession}` in MCP.

Removed inputs return 400. MCP and CLI surface the same validation text where applicable. The push endpoint checks removed fields in the raw request before strict decoding or ingest. In the message, `i` is the zero-based observation index.

| Removed input | Error |
|---|---|
| `settledFrom` on `GET /api/tasks` or `GET /api/trees` | `settledFrom was removed in API v9; use completedFrom` |
| `state=settled` or `settled` in MCP `list_tasks.states` | `state settled was removed in API v9; use state done (completedAt marks acceptance)` |
| `settled` in `events` on `PUT /api/preferences/notifications` | `event settled was removed in API v9; use completed (want needs_input, failed, ready, completed, tree_completed, schedule_started, schedule_skipped, done)` |
| `settled` in MCP `wait_task.until` or CLI `--until` | `until: settled was removed in API v9; wait for done` |
| Group key starting with `thread:` on `GET /api/notifications?group=` or `POST /api/notifications/read` | `group key thread:... was renamed in API v9; use session:<connectionId>:<externalSessionId>` |
| Pushed observation `artifacts` | `observations[i].artifacts was removed in API v9` |
| Pushed observation `modelLabel` | `observations[i].modelLabel was removed in API v9` |
| Pushed observation `links.service` | `observations[i].links.service was removed in API v9` |
| Pushed observation `links.app` | `observations[i].links.app was removed in API v9` |
| Pushed observation `reply.messageId` | `observations[i].reply.messageId was removed in API v9` |
| Pushed observation `reply.position` | `observations[i].reply.position was removed in API v9` |

`wait_task` also rejects any other unknown state with `until: invalid state "<s>" (want one of ...)`.

## API v8 changes

Breaking, so `apiVersion` is 8 and v7 clients refuse this server. T3 is no longer the default harness (ADR 0007 decision 12): nothing outside the harness's own adapter and setup treats a connection as T3.

- Removed inputs, 400 naming the replacement (`<field> was removed in API v8; send executionConfig with connectionId`): `providerInstanceId`, `model`, `modelOptions`, `runtimeMode` and `interactionMode` on task and schedule create and update. Send `executionConfig` with `connectionId`.
- Task and schedule create take `connectionId` and `workspaceId`. See [current workspace selection](#task-and-schedule-workspace-selection). An omitted `executionConfig` runs on the harness defaults.
- Removed routes: `GET /api/catalog` (404; use `GET /api/connections/:connectionId/capabilities`), `GET /api/connections/t3/projects` (use the connection id), and the T3 thread read `GET /api/connections/:connectionId/sessions/:id` (404; use `…/context`).
- Removed fields: `thread` on task detail (use `session`, `links`, `finalReply`, `runStartedAt` / `runEndedAt` and `lastWordPreview`, now derived from the session's observation for every harness).
- References: a bare session id or `…/sessions/<id>` names no connection: 400 without `connectionId`. T3 web and `t3code://` links resolve only through the T3 connection's adapter.
- MCP: `list_models` requires `connectionId`; the removed T3 model arguments fail schema validation. CLI: `--provider`, `--model`, `--effort`, `--option`, `--runtime` and `--mode` are errors naming `--connection ID --execution-config JSON`; `models ls` needs `--connection`.
- Worktree as a harness launch option (#420): `workspace` on task and schedule create and update is 400 `workspace was removed in API v8; set the harness's launch options in executionConfig.options (worktree, baseRef, branch where the connection declares them; see its capabilities)`. Tasks and schedules drop `workspace`, task detail drops `currentLaunchBase`, the harness descriptor drops `worktree`, and capability operations `predecessorBase` and `worktreeBase` are gone. Options gain `kind`, `label` and `dependsOn` (see [capabilities](#get-apiconnectionsconnectionidcapabilities)). Predecessor bases are gone: a dependent starts where its own launch options say, and the handoff names the predecessor's branch. MCP: `workspaceType`, `baseRef`, `branch`, `base` and `baseTaskId` fail schema validation; CLI: `--workspace`, `--base-ref`, `--branch`, `--base` and `--base-task` are errors naming `--execution-config`. Storage: migration 0033 moves each stored workspace into the execution config's options (T3 `worktree`, `baseRef`, `branch`; Claude Code and OpenCode `worktree`; Codex none) (a T3 predecessor-based task keeps its stored fallback `baseRef` and `branch`), as well as unfinished launch attempts' intents, and journals on each predecessor-based task that can still start (not finished or archived) where its worktree now starts (T3: from its `baseRef`, or, with none stored, that one must be set before it starts, since T3 rejects a worktree without one; Claude Code and OpenCode: the harness default); migration 0034 drops `tasks.workspace`, `schedules.workspace` and `task_launch_bases`.
- Storage (migration 0032): the T3-only `threads` mirror is dropped (sessions live in `execution_observations`), and a fresh install has no connection: the seeded T3 connection 1 is removed when it was never configured and nothing references it. A never-ran T3 session (idle, no run) adopts as `backlog`; T3 idle after a run still reads `done` (the T3 adapter decides it). Project `lastActivity` no longer counts external sessions. The T3 status's `lastFullSyncAt` is recorded by the T3 setup after any successful full sync of its connection.

## API v7 changes

Breaking, so `apiVersion` is 7 and v6 clients refuse this server. New-task defaults move from preferences to each connection ([Preferences](#preferences)). Removed: `PUT /api/preferences/pinned` (404) and `pinned` / `lastUsed` on `GET /api/preferences`; creating a task no longer records a last-used model. Added: `defaultConnectionId` on preferences and `PUT /api/preferences/default-connection`; `newTaskConfig` on connections and `PUT /api/connections/:connectionId/new-task-config`. A create without `connectionId` starts on the default connection when the project is available there. Migration 0024 moves the old global pin to the main machine's T3 connection and sets no default.

Observation push (#420; additive, no version bump): `POST /api/connections/:connectionId/observations` lets an external bridge or hook push observations of an existing connection ([Harness connections](#harness-connections-api-v6)).

Session interaction answers (#375; additive, no version bump): requests gain `preview`, `textOption` and `questions`, responses `answers`; Claude Code offers `respond`; and the respond route answered 403 `agent_forbidden` to agent callers (replaced by `self_action_forbidden` for the caller's own task, [ADR 0011](adr/0011-agent-operation-parity.md)).

Legacy storage dropped (#417; no version bump, no client reads these): sessions drop `providerInstanceId`, `creationSource` and `runCount`. Journal `sourceThreadId` is the external ID of a T3 `sourceSession` (else null); handoff `sourceThreadId` is the source session's external ID (launch bases were later dropped, #420). PR links of the old thread mirror leave `artifactLinks` (accepted evidence keeps its copy). The legacy T3 model inputs are translated into `executionConfig` on write instead of stored.

## API v6 changes

Breaking, so `apiVersion` was 6 and v5 clients refuse this server. Removed, returning 404: `GET /api/t3/status`, `POST /api/t3/pair`, `POST /api/t3/pair/auto`, `DELETE /api/t3/pair`, `PUT /api/t3/url`, `GET /api/opencode/status`, `PUT /api/opencode/settings`, `DELETE /api/opencode/settings`, `POST /api/opencode/connect-local`, `POST /api/opencode/pair`. Use [Harness connections](#harness-connections-api-v6): their old bodies are the new settings and action bodies, and their old replies are the status `details` (the OpenCode routes now answer 200 with the status instead of 204). Health's `t3` object is now `harnesses[]`. CLI: `stage t3` and `stage opencode` are `stage harness KIND status|set|run|disconnect`.

Added: [session interaction](#session-interaction-api-v6) routes, descriptor `launch`, `links.command`, harness-wide capability `options` and `modeLabel`, capability operation `worktreeBase` (unavailable when the harness chooses the worktree base and branch itself; removed in v8, #420).

T3 legacy fields are gone from responses: tasks, threads, notifications and waiting sessions drop `t3WebUrl`, `t3AppUrl` and `t3DeepLink` (use `links`); tasks and schedules drop `providerInstanceId`, `model`, `modelOptions`, `runtimeMode` and `interactionMode` (use `executionConfig`, now always present for T3 rows) and `originThreadId` (use `originSession`); the task's `originThreadTitle` is `originSessionTitle`; resolved references drop `threadId` (use `session`). Inputs removed: `needsSessionId` and `originThreadId` on task and schedule create (use `needsSession` and `originSession`; strict decoding answers 400), the `originThreadId` task list query (400; use `originConnectionId` and `originSessionId`), MCP `list_tasks {originThreadId}` and CLI `task ls --origin-thread`. `artifactLinks` now include harness-reported artifacts for every harness. The legacy T3 model inputs on create and update stay accepted for now.

Caller attribution is generic: removed `callerThreadId` (MCP tools), journal `sourceThreadId` input, adoption `threadId`, CLI `--thread`, `sessions adopt THREAD` (use `--target-connection` and `--target-session`) and `mcp --t3-session-env`. Use `callerSession`, the `Stage-Caller-Session` header or `STAGE_CALLER_SESSION` ([Agent interface](#agent-interface)). The CLI and stdio MCP later read `T3CODE_THREAD_ID` again, generically, as the harness's session variable (`attribution.env`, resolved by `GET /api/connections/resolve`).

Free-form tags are gone (migration 0016 drops the columns and their values): tasks and schedules drop `tags`; `tags` on task create and update, schedule create and session adoption is an unknown field (400; schedule update ignores unknown fields); the `tag` task list query is 400. MCP `create_task`, `update_task`, `create_schedule` and `adopt_session` drop `tags`, `list_tasks` drops `tag` and its rows drop `tags`; CLI `--tag` is removed from `task add|edit|ls`, `schedule add|edit` and `sessions adopt`.

## API v3 retirement

Removed outright, returning 404: `GET /api/threads`, `GET /api/threads/search`, all transcript and question routes, `POST /api/tasks/:id/followup`, `GET /api/stats`. Removed CLI session ls/search/show/questions and task followup; removed MCP list_sessions/search_sessions/get_session/get_session_questions/followup_task. Use the matching v3 CLI/server/desktop release. No 410 compatibility period or redirects for retired session pages. Historical rows, journals, execution references and stored artifact links are preserved; no destructive migration.

## References

### `GET /api/refs/resolve?ref=`
What a pasted reference names. `ref` accepts: a stage task, tree, schedule, project or session link (`…/tasks/<id>`, `…/trees/<id>`, `…/schedules/<id>`, `…/projects/<id>`, `…/sessions/<id>`; any host, query and fragment ignored, a sub-path prefix allowed for tasks, trees and sessions; schedule and project links must be the whole path, so `github.com/orgs/x/projects/3` is not one), a journal entry link (its owner's task or tree link with `?entry=<id>`, as the journal copies it; an invalid `entry` is 400), the markdown link the web UI copies (`[stage task #12: Title](…/tasks/12)`, `[stage tree #4: Title](…/trees/4)`), a desktop deep link (`stage://tasks/<id>`, `stage://trees/<id>`, `stage://schedules/<id>`, `stage://projects/<id>`, `stage://sessions/<id>`, a task or tree deep link with `?entry=<id>`, also under `stage://localhost/`; any `stage-<suffix>://` developer scheme), a scoped session path (`…/connections/<connectionId>/sessions/<id>`), a harness's own session link (a T3 web or `t3code://` link, a Claude Code or Codex resume command), which the connection's adapter resolves (`execution.LinkResolver`; with `connectionId` that connection's, else each active connection's), `#12` or `12` (task), or any other single token (an external session id, with `connectionId` only). A session reference that names no connection (a bare id, `…/sessions/<id>`) without `connectionId` is 400. Stage paths win over harness links on the same host. 200:
```json
{ "kind": "task", "taskId": 12, "session": { "connectionId": 1, "externalSessionId": "th-1" }, "links": { "web": "…", "native": "…" }, "state": "running" }
```
`session`: a task's session (null before launch; API v6 removed `threadId`); a session's `taskId` is the task that owns it (null for sessions started outside stage). A tree link returns `{ "kind": "tree", "treeId": 4, "taskId": null, "session": null, "links": {} }`; a schedule link `{ "kind": "schedule", "scheduleId": 5, "taskId": null, "session": null, "links": {} }`; a project link `{ "kind": "project", "projectId": 2, … }` with the canonical id (a merged project's alias resolves to the project it was merged into); a journal entry link `{ "kind": "journal", "entryId": 30, "taskId": 12, "session": null, "links": {} }` (`treeId` instead of `taskId` for a tree entry). A `connectionId` selection does not apply to these. A session stage has not observed yet is read from its harness once when connected. 400 not a reference; 404 unknown task, tree, schedule, project, journal entry or session, or a journal entry that does not belong to the task or tree its link names.

For a past attempt, the resolver includes `attempt` alongside the owning
`taskId`, the historical ref and cached links. The browser session
reference page opens the owning task until the Session tab routes ship.

## Snippets

Reusable prompt text, inserted into prompts, follow-ups and notes by typing `/` in the web app. `projectId` null = global (shown in every project); set = only in that project's tasks.

`Snippet`:
```json
{
  "id": 1,
  "name": "plan-first",
  "body": "Before editing, write a plan and wait for approval.",
  "projectId": null,
  "createdAt": "2026-11-08T10:00:00Z",
  "updatedAt": "2026-11-08T10:00:00Z"
}
```

- `GET /api/snippets` → 200 `{ "snippets": Snippet[] }`: global first, then by project title, then `name`.
- `POST /api/snippets` body `{ "name": string, "body": string, "projectId"?: number|null }` → 201 `Snippet`. 400 blank name/body or unknown `projectId`. 409 duplicate name (names are unique across all scopes).
- `GET /api/snippets/:id` → 200 `Snippet`. 404.
- `PATCH /api/snippets/:id` partial of the POST body → 200 `Snippet`. 400, 404, 409 as above.
- `DELETE /api/snippets/:id` → 204. 404.

### Task and schedule workspace selection

Send `connectionId` and `workspaceId` from
`GET /api/projects?view=destinations` or the connection inventory. The
exact pair is required for immediate start, a one-shot start and schedule
creation. A backlog task can provide `projectId` and `connectionId` for
planning and omit the workspace. With an exact pair, an optional
`projectId` organizes the work. When omitted, Stage resolves the
workspace's project context. Task and schedule reads return `workspaceId`
when a pin is present.

On update, omitting both selectors and keeping the same connection preserves
the pin. For an untimed backlog task, `workspaceId: null` clears the pin.
Send the new workspace and execution configuration when changing
connections. Duplication copies the saved workspace. Each schedule fire
uses its template's pair. A cron fire with an unavailable pin records a
skipped run and its reason.

Older clients should replace `bindingId` and project-only launch requests
with an exact pair selected from the destination summary. Use
`stage projects ls --json` or MCP `list_projects`, then pass
`--connection ID --workspace-id ID` in the CLI or
`{connectionId, workspaceId}` in MCP. Capabilities take `workspaceId`
on that connection. Review the saved repair reason and explicitly choose
a replacement if an older saved destination is unresolved. Task detail
retains historical workspace fields, including the frozen location on a
claimed attempt.

## Preferences

The shared default connection can prefill a visible selection in the new-task form, which submits the selected exact pair. In shared access, the notification filter belongs to the authenticated account. New-task defaults live with each connection: its model pin is `newTaskConfig` on [`GET /api/connections`](#harness-connections-api-v6) (`PUT /api/connections/:connectionId/new-task-config`). Model and options prefill from the selected connection, else the harness default. Permission and mode start at the harness default.

### `GET /api/preferences`
200 `{ "defaultConnectionId": number | null, "notifyEvents": NotificationEvent[], "sharedNotifications"?: true }`. `defaultConnectionId` is null when unset or when its connection is no longer active (its machine removed). Pairing another instance of its harness moves it to the replacement. `notifyEvents` lists the person's enabled events in display order (`needs_input`, `failed`, `ready`, `completed`, `tree_completed`, `schedule_started`, `schedule_skipped`, `done`). An account that never saved a filter gets every event except `schedule_started`. Human sessions in shared access receive `sharedNotifications: true`. Shared agent credentials can read the connection default and receive an empty `notifyEvents`.

### `PUT /api/preferences/default-connection`
Body: `{ "connectionId": number | null }` (null clears it).
200 `Preferences`. 400 unknown or replaced connection.

### `PUT /api/preferences/notifications`
Body `{ "events": NotificationEvent[] }` with `needs_input|failed|ready|completed|tree_completed|schedule_started|schedule_skipped|done`. Duplicates are dropped. An empty array mutes every event. Applies to new events on every device for the signed-in account. Shared history keeps muted events as read. In personal mode, muted events are dropped.
200 `Preferences`. 400 unknown event or missing `events`.

## Notifications (Web Push)

The server sends Web Push (VAPID, RFC 8291 aes128gcm) to active subscribed browsers eligible for the event. In shared access, each binding belongs to one browser session and its account. Multiple signed-in devices can receive the same account's events. Signing out invalidates that session's bindings. Disabling an account, completing a password reset, or changing its password invalidates all of its bindings. Idle and absolute expiry also end delivery. Opt-in removes the personal endpoints and requires signing in and enabling push again. The VAPID key pair is generated on demand and stored in the database. The VAPID subject is `STAGE_PUBLIC_URL` when it is HTTPS, otherwise `https://github.com/flexdinesh/stage`.

### `GET /api/push/config`
200 `{ "publicKey": string }` (VAPID public key, base64url, uncompressed P-256 point; the browser's `applicationServerKey`).

### `POST /api/push/subscriptions`
Body contains the browser's `PushSubscription.toJSON()` subset `{ "endpoint": string, "keys": { "p256dh": string, "auth": string } }`. Upserts by endpoint for the current browser session. Rebinding across sessions requires matching subscription keys and rotates the delivery binding. 204 on success, 400 for missing fields or an endpoint without HTTPS, and 404 if the endpoint cannot be rebound.

### `DELETE /api/push/subscriptions`
Body `{ "endpoint": string }`. Removes only the current session's subscription in shared access. 204 when removed or absent. 400 missing endpoint.

### `POST /api/push/subscriptions/check`

Body `{ "endpoint": string }`. Returns 200 `{ "bound": boolean }` for the current browser session (personal subscription in personal mode).

### `POST /api/push/test`
Body `{ "endpoint": string }`. Sends a test notification to the current browser session's subscription synchronously. 204 on success, 404 when the session has no matching endpoint, and 502 when the push service rejects it. A 404/410 from the push service deletes that binding.

### `POST /api/push/delivery`

Body `{ "binding": string, "eventId": number }`, where zero represents a test. Resolves a shared push reference using the active browser cookie without extending its idle window. Returns the display payload for an unread event belonging to that account and binding, or 204 when no delivery remains. Signed-out and revoked sessions receive 401. CLI and agent credentials cannot manage or redeem browser push.

### `GET /api/badge`
200 `{ "needsInput": number, "unread"?: number }`. `needsInput` counts shared tasks and external sessions needing input. Shared human sessions also receive their own unread event count. Installed app badges use `unread` in shared access and `needsInput` in personal mode.

### Push payload
The display payload is built from the stored inbox row, using the same fields as `GET /api/notifications/stream`. Personal Web Push carries it directly. Shared Web Push carries only `binding` and `eventId`. The worker redeems the reference before displaying or opening an alert. An offline or stale reference cannot display work details.
```json
{ "title": "Needs input", "body": "Say OK · stage", "url": "/tasks/7", "tag": "task-7", "badge": 2 }
```
Needs-input payloads open their task or the inbox. Open in T3 continues the conversation.
`tag` replaces an older notification for the same task (`task-<id>`), tree (`tree-<id>`), schedule (`schedule-<id>`) or external session (`session-<connectionId>:<externalSessionId>`). The service worker sets `renotify` to alert again and closes shown notifications with the same tag on iOS. TTL is 24 hours. Urgency is `high` for needs input and `normal` otherwise. The push service retains the latest pending message for a topic, derived from the tag and, in shared access, the binding.

Clear on view: opening a task, tree or schedule page closes its `task-<id>`, `tree-<id>` or `schedule-<id>` system notification and marks group `task:<id>`, `tree:<id>` or `schedule:<id>` read; opening session detail closes its `session-<connectionId>:<externalSessionId>` notification and the related task notification when present, and marks those groups read (`POST /api/notifications/read` with `groups`). Clicking a notification opens one of these pages. Opening the notifications inbox closes all of them and marks everything read.

Cross-device clear: each device closes shown `task-*` and `session-*` notifications after those groups have been read on the same account. The service worker reconciles after a push, the app reconciles on load and when it becomes visible, and desktop reconciles on each SSE `unread` event. OS dismissal is best effort. WebKit can defer closing recent alerts until a later sync. The account lifecycle also clears device alerts and badges when the host detects logout or session replacement.

When a window is focused and visible on Chromium, the app shows an in-app toast. Safari and Firefox show the system notification for authorized pushes. The service worker updates the app badge and posts `{ "type": "push" }` to open windows so they refetch.

## Notifications inbox

Shared access records each workspace notification event once, including when no device is subscribed. Each account has its own read state. Newly joined accounts see retained history as read, and live work stays visible. Entries older than 30 days are pruned hourly along with their receipts.

`Notification`:
```json
{
  "id": 12,
  "event": "needs_input",
  "taskId": 7,
  "treeId": null,
  "scheduleId": null,
  "externalSessionId": "thread-abc",
  "title": "Say OK",
  "projectTitle": "stage",
  "url": "/tasks/7",
  "createdAt": "2026-11-08T10:00:00Z",
  "readAt": null,
  "currentState": "needs_input",
  "note": null,
  "connectionId": 1,
  "links": { "native": "t3code://threads/env-1/thread-abc" }
}
```
`event`: a NotificationEvent. `taskId` is null for external sessions, tree and schedule events, or once the task is deleted. `treeId` is set on `tree_completed`, `scheduleId` on `schedule_started|schedule_skipped`; each is null once its tree or schedule is deleted. Tree and schedule rows have no `currentState`. `title` and `projectTitle` are snapshots at send time. `currentState` is live and reflects the task state or the observed state of an external session. It is null when unknown. `note`: why a held notification came late (`Steerer did not answer`, or `Steerer: <reason>` from `escalate_to_user`), else null; push bodies end with it.

Task notifications open `/tasks/<id>` or, for `needs_input`, `/tasks/<id>/session`. Both use the same task group key. Tree and schedule notifications open `/trees/<id>` and `/schedules/<id>`. External notifications open `/?inbox`, with the harness destination in the inbox when available. Historical stored URLs are preserved. Emitted destinations derive from the subject and event.

### `GET /api/notifications`
Query: `limit` (default 50, max 200), `before` (id; only older entries), `group` (optional group key from the grouped inbox; restricts history to that task or session). 200 `{ "notifications": Notification[], "unread": number, "nextBefore": number | null }` newest first; `nextBefore` = the `before` for the next (older) page, null when none. `readAt` and `unread` apply to the authenticated recipient. 400 bad `limit` / `before`.

### `GET /api/notifications/groups`
Query: `limit` (default 50, max 200), `cursor` (opaque `nextCursor` from the previous page). 200 `{ "groups": NotificationGroup[], "unread": number, "latestId": number, "nextCursor": string | null }`. 400 bad `limit` / `cursor`.

`NotificationGroup` extends `Notification` with `key` (stable task or session identity), `eventCount` (retained notifications for this group), and `unreadCount`. Its inherited notification fields describe the latest event; `currentState` stays live. Group by task across restarts, or by external session, never by title. Deleted tasks retain their group via their task URL. Identity-less entries remain separate.

Groups whose current state is `needs_input` come first, then other groups; each section is newest-event-ID first. Aggregation precedes pagination, so repeated events cannot consume a page or cause older copies of a group to reappear. `latestId` is the global newest event ID captured before listing, independent of priority order; use it for read-on-open, then the newest observed `latestId` for arrivals while the drawer is open. Events inserted after that boundary remain unread. Unread markers captured on open stay visible until close. The bell still counts unread events.

Expandable history uses `GET /api/notifications?group=...`; it contains only that group's recorded notifications. Unknown current state displays as unavailable rather than falling back to an old event.

### `POST /api/notifications/read`
Body `{ "upTo": number }` marks the recipient's entries through `id <= upTo` read. Alternatively, `{ "groups": string[] }` marks their entries in 1 to 20 groups, such as `task:12` or `session:<connectionId>:<externalSessionId>`. 204 on success. 400 when neither or both are sent, `upTo` < 1, or `groups` is empty, too long or has an empty key.

### `GET /api/notifications/unread-tags`
200 `{ "tags": string[] }`: push tags of groups with unread entries (`task:<id>` → `task-<id>`, `tree:<id>` → `tree-<id>`, `schedule:<id>` → `schedule-<id>`, `session:<connectionId>:<externalSessionId>` → `session-<connectionId>:<externalSessionId>`; identity-less groups have none). Devices close shown notifications whose tag is missing (see "Clear on view").

### `GET /api/notifications/stream`
Server-sent events return inbox rows after a cursor, then live rows and the recipient's unread snapshots. Headers include `Content-Type: text/event-stream` and `X-Accel-Buffering: no`. Shared streams are private and use `Cache-Control: private, no-store, no-transform`. Personal streams use `no-cache, no-transform`. The first line is `retry: 3000`. 400 when the cursor is not an integer >= 0.

Cursor = a notification id: `Last-Event-ID` (reconnect) wins over `?after`. No cursor → cursor = the newest id (no replay). A cursor above the newest id (e.g. a reset database) → cursor = the newest id and `reset: true`.

Events, in order:
- `hello` (`id: <cursor>`): `{ "latestId": number, "reset": boolean }`. `latestId` = newest id at connect (0 when empty).
- `gap` (no id): `{}`, sent when more than 100 rows follow the cursor; only the newest 100 are replayed.
- `notification` (`id: <id>`), per row id > cursor, ascending: `{ "id": 12, "event": "needs_input", "createdAt": "2026-11-08T10:00:00Z", "read": false, "tag": "task-7", "title": "Needs input", "body": "Say OK · stage", "url": "/tasks/7" }`. `tag`, `title`, `body` and `url` are the Push payload of that row; `tag` is `""` for identity-less rows. Replay includes rows already read (`read: true`).
- `unread` (no id) carries `{ "tags": string[], "badge": number }` with the recipient's unread tags and displayed badge count. Sent once after replay, then on changes.
- `access-ended` (no id) carries `{}` and terminates a shared stream when its session loses access. The host clears that session's badge, alerts, and reconnect cursor. A reconnect without valid credentials returns 401.

Each new inbox row, read, prune, or task transition wakes the stream. It sends rows after the most recent row it sent and any changed unread snapshot. A ping every 15 seconds also recomputes the snapshot. Shared streams periodically recheck credentials without extending browser idle expiry. Reconnect after network loss or a server restart with the same session and its `Last-Event-ID`.

## Notification rules

- A shared account's filter determines whether each new event arrives unread and can alert that account's devices. All workspace events remain available in shared history. In personal mode, the owner's filter determines which events are recorded and pushed.

- Tasks: a transition into `needs_input` ("Needs input") or `failed` ("Failed") notifies. Stored `url` = `/tasks/<id>` for stable group identity; push destination = the task URL. Other states (incl. `done` and `interrupted`) do not; older `done` task rows read "Run finished".
- Hold (tree steering, [ADR 0013](adr/0013-tree-steering.md)): a tree member's `needs_input` is held, not recorded, when its tree's `steeringHoldSeconds` is above 0, the tree has an active steerer whose task is `running`, `done` or `needs_input`, and the member's connection offers `respond` or `send`. Both paths hold: the coordinator's transition hook and the observation transaction. A hold is a `notification_holds` row (Task JSON `hold`); the notification row is inserted only at release, because the inbox stream's cursor is id-ordered. No other event is held.
  - The member leaves `needs_input`, or is answered through Stage at the held revision: the hold is cancelled in the same write; nothing notifies.
  - Expiry (`hold.until`) while still unanswered at the same revision: the notification is recorded with note `Steerer did not answer`, pushed and signalled, and `hold_escalated` is enqueued.
  - `POST /api/tasks/:id/escalate`: released at once with note `Steerer: <reason>`.
  - The steerer's binding ends (stopped, replaced, tree finished, task removed), its task stops being `running|done|needs_input`, the member leaves the tree, or the member's connection no longer offers `respond` or `send`: released at once without a note. A connection whose capability check fails keeps its holds until the next check.
  - A release loop ticks every 5 s and on transitions and tree or steering changes; holds are durable, so a restart releases what came due while down. The filter above applies at release: a muted `needs_input` ends the hold without a row.
- Completed: acceptance by inferred completion or by an agent's complete action notifies `completed` ("Task completed", `url` = `/tasks/<id>`, tag `task-<id>`). A person's complete action never notifies, in either access mode; a replayed `requestId` and a reopen never notify.
- Tree completed: when an open tree's last member is accepted by a path that notifies `completed`, `tree_completed` ("Tree completed", title = the tree's title, `url` = `/trees/<id>`, tag `tree-<id>`) notifies once per episode (only for the member with the newest acceptance). A person's acceptance of the last member never notifies.
- Ready: a manual tree member with at least one need whose needs all have user acceptance (see "Tree rules") notifies once per readiness episode ("Ready to start", `url` = `/tasks/<id>`, tag `task-<id>`). Roots and auto members never send it.
- Schedules: see "Schedule rules".
- External sessions: adapters expose an attention feed. Stage also rereads stored active/done sessions to observe their outcomes. Changes into `needs_input`, `done` ("Session finished") or `failed` notify; the inbox destination is retained. The first pass after startup is silent. Later newly discovered sessions notify only if created after startup. Missing sessions and failed reads preserve the last observation; closing an unowned session does not affect acceptance or dependencies. A task's own session follows "Inferred completion".
- Needs-input counts combine tasks with unowned, non-child normalized session observations. Historical notification entries and links remain available.

## State mapping

The adapter maps harness state to Stage observations. Active execution and pending
input take precedence over earlier outcomes. Completed execution maps to done;
unknown or missing replies preserve the last known state. Connection health is
separate. Polling and recovery change acceptance only through inferred completion. See [execution.md](execution.md).

## User acceptance (API v4)

Execution `state` records the run's status. `acceptanceId` (nullable), `acceptanceRevision`, `completedAt` (nullable) and `completionProvenance` (nullable) describe Stage acceptance. Task detail adds `acceptances`, newest first. Tree `counts.completed` counts currently accepted members, and tree `completedAt` is their latest acceptance when all members are accepted. Unaccepted execution in `done` contributes to `counts.done`.

- `GET /api/tasks/:id/completion-preview`: `revision`, `evidenceVersion`, `evidence`, `blockedReason`, `dependents`. Reads Stage's local cache only. Dependents identify claimed work and effective start mode.
- `POST /api/tasks/:id/complete`: `{requestId, revision, evidenceVersion, note?}`, from the preview. Rejects active `launching|running|needs_input`, mirrored active runs/pending requests, and uncertain launch outcomes. Any known stopped state or unstarted task may be accepted, including interrupted/cancelled/failed work. The optional note is UTF-8, at most 16 KiB.
- `POST /api/tasks/:id/reopen`: `{requestId, revision}`. Clears current acceptance; retains immutable history, execution and existing launch claims. Does not start a run. A successful execution continues in T3; ordinary failed/interrupted/cancelled retries remain available.
- Both writes return `{revision, acceptanceId}`. Exact request replay returns its original result without applying it again, even after a later reopen. Reusing a request ID with changed content, stale revision or changed evidence yields 409; refresh and review before a new submission.
- Acceptance records contain `id, taskId, acceptedAt, capturedAt, provenance, evidence, note`. Evidence captures `session`, `taskId`, `title`, `treeId`, `treeTitle`, `projectId`, `externalSessionId`, `sessionLink`, `branch`, `worktreePath`, `executionState`, `runId`, `replyRunId`, `replyItemId`, `reply`, `replyTruncated`, `replyAt`, `observedAt` and `journalThroughId`. `sessionLink` uses the web destination if available, then the native destination, otherwise null. Cache observation time, earlier/unverified run and missing reply are explicit. No live read is required to review, accept or use handoff.
- Confirmation and launch claims serialize in SQLite. Reopen before claim blocks it; claim before reopen continues with pinned inputs. Completing again cannot launch an existing attempt twice. Manual trees become ready; automatic trees retain automatic release. Closed trees stay closed.
- `provenance`: `user` (complete action, by a person or an agent; `actor` tells which), `historical_settlement` (migration), `harness_settled` (T3 settle) or `harness_archived` (OpenCode or Codex archive, Claude Code removal).
- Inferred completion: when a poll observes a task's session closed in its harness (T3 settled, OpenCode or Codex archived, Claude Code session removed with `claude rm` after its turn ended; observation `closed.signal`, also on an idle observation, which leaves the task's state as it was), Stage completes the task in one transaction only if its state is `done`, it has no current acceptance, the session is not active, the observation has an execution ID, the task's connection has `autoComplete`, the completion preview has no `blockedReason`, and no earlier decision exists for that execution. It records the preview's evidence with an empty note, `acceptedAt` = now (never T3's settle time), the same task/tree/journal/readiness updates as user completion (journal `task_completed`: the harness closure's event: `Completed from T3 settle; accepted evidence saved in Stage.`, `Completed from OpenCode archive; accepted evidence saved in Stage.` `Completed from Codex archive; accepted evidence saved in Stage.` or `Completed from Claude Code removal; accepted evidence saved in Stage.`), then triggers the release runner. Failed, interrupted or cancelled execution never infers. Reopen records a decision for the session's current execution, so a still-closed session does not complete it again; a new execution can.
- Polling: unaccepted `done` tasks stay in the throttled stale poll (at most once a minute) so a later closure is seen; accepted ones are polled only for an hour after their last update.
- The historical acceptance migration captured evidence for tasks that were in `settled` at that rollout. Migration 0035 preserves current acceptance IDs. When reading older evidence, Stage uses legacy `threadId` and `threadLink` if the corresponding new fields are absent, maps `executionState: "settled"` to `done` and ignores `artifacts` and `historicalSettledAt`. Stored evidence and acceptance request bytes remain intact.
- People and agents complete and reopen ([ADR 0011](adr/0011-agent-operation-parity.md)); inferred completion is the only server-side path. Agents use `get_completion_preview`, `complete_task` and `reopen_task` (CLI `task preview|complete|reopen`). A complete or reopen whose `Stage-Caller-Session` belongs to the task gets 403 `{"code": "self_action_forbidden"}`: an agent reports its own outcome with a note. Best effort: a request naming no caller session passes. An agent's acceptance records actor kind `agent`, its caller session in `cause.session` and the request ID; the event reads "Completed by agent" or "Reopened by agent". Agent completion releases dependents like a person's. Ordinary task updates cannot set acceptance.

## Tree rules

- Needs (create `needs`, `PUT /api/tasks/:id/needs`, tree `needs`), 409 unless noted: change only in `backlog` (an unchanged set is a no-op in any state); not with a start time (400); each need exists (404), is not the task, is in the task's tree, and does not (transitively) need the task (no loops). An accepted need is allowed.
- Joining a tree without needs (create with `treeId`, add to tree, adopt) makes a root, parallel to the other roots; `needs` chain the task after those members, and tasks that need the same member run in parallel. Nothing defaults needs: chaining is always explicit.
- Create with `needs` and no `treeId` joins the needs' tree; needs outside any tree are pulled in; with no tree among them a new `auto` tree is created, titled after the first need. Needs in different trees (or another tree than `treeId`) 409. `needsSession` adopts that session first (see "Adopt session") and adds it as the first need. One transaction.
- Tasks inherit the tree's start mode by default (`startMode` omitted or null; CLI/MCP also accept `inherit`). Set shared policy on the tree, and explicit task `auto` or `manual` only for an exception. In an auto tree a task created or joined after its needs already have user acceptance launches at once; agent guidance treats that as expected and does not steer to `manual` to avoid it. Tree mode changes preserve explicit overrides; clear a task's override to make it inherit again.
- `start: true` or a start time with needs is 400. A non-backlog task may join a tree only without needs.
- A need is passed only when `acceptanceId` is non-null. Execution success, stopping, failure and PR merge never release new work; a harness closure releases work only through inferred completion. Complete the need in Stage or edit dependencies.
- `readiness` (backlog members only, else null): `blocked` while a need lacks user acceptance; `ready` when every need has user acceptance, there is no start time, and the task is manual or has no needs. Roots read `ready` (auto never launches them; the user starts them, or Start ready).
- Release runner (on user or inferred completion/reopening, every transition out of `backlog`, every tree change, at startup and every 10s): launches every `auto` member whose needs all have user acceptance (no start time, not adopted); a `manual` member with needs that became ready gets `readyAt` and a `ready: needs completed` event once per episode and notifies `ready`. `readyAt` clears when it stops being ready or launches.
- Splice (remove from tree, delete, move to another tree): each dependent drops the task and takes over its needs (A→X→B becomes A→B, deduplicated); the task loses its needs and leaves the tree; an emptied tree is archived. Splicing an unfinished middle task can make dependents ready at once.
- Moving between trees = remove + add (no merge). `POST /api/trees/:id/dissolve` dissolves a tree.
- Launch claims atomically check dependencies, tree status and effective policy, then pin acceptance IDs. Reopen blocks future claims; already claimed attempts continue with their original snapshots. Unknown launch outcomes reconcile with the same attempt, request ID and pins. A fresh retry checks current acceptance again.

## Done, archive and delete

- A tree is `open`, `done` (marked done by hand: the remaining tasks will not be worked on) or `archived`. A task is archived when `archivedAt` is set. "Closed" = an archived task, or a member of a done or archived tree.
- Marking done or archiving needs no member (or the task itself) in `launching|running|needs_input` (409 naming it). Members keep their states. Only standalone tasks are archived one by one; a tree member is closed with its tree.
- A closed tree releases nothing (no auto launch, no `readiness`, no `ready` notification, no due one-shot start), and rejects structural changes with 409 (`tree N is done: reopen it first`): add or move a task into or out of it, set needs, remove from tree, delete a member, create a task with its `treeId`, adopt into it, change `startMode`, start ready. Rename, status, delete and dissolve stay allowed. An archived task waits too (no due one-shot start) and cannot join a tree (409 `unarchive it first`).
- Closed tasks and trees leave the default lists (`GET /api/tasks`, `GET /api/trees`), so they leave the Dashboard; `closed=true|all` and `status=done|archived|all` list them.
- Activity may unarchive a standalone task. A done or archived tree stays closed until the user explicitly reopens it. Task completion or reopening never reopens its tree.
- Delete only work that never started and has no recorded work (notes or execution): `DELETE /api/tasks/:id`, and `DELETE /api/trees/:id` for a tree with no recorded work whose members are all deletable (it deletes them). Tree protection survives recorded members leaving. Archive recorded work instead; dissolving or emptying preserves the archived tree identity and journal.

## Adopt session

`POST /api/connections/:connectionId/sessions/:id/adopt` and the task creation field `needsSession` adopt a top-level session. Adoption takes an optional `callerSession` for initial-note attribution.

- `adopted: true`, `prompt: ""`, title from the request, else the session title, else `Session <id8>`; model, runtime and interaction mode copied from the session; state from the session (State mapping, idle reads `done`); `launchAttempt` 1. Task event `adopted session`.
- stage never starts, restarts or duplicates it (409); stop works; sync keeps its state.
- A session a task already owns returns that task (200; `needsSession` uses it as the need). Unknown session 404; a subagent, an archived session, or one whose project is not in stage 409.
- With `treeId` the task joins that tree (no needs). Adoption never accepts the task, including a session already closed in its harness; a later poll may (connection auto-complete).

## Handoff

Every task launch also appends a separate journal instruction block, including standalone tasks and `handoff: false`. It records task ID, tree ID at launch, connection ID, an attended-session line (start mode and delivery path only decide when and how the session started; the user watches and answers in it, so the agent works as in a user-started session, including asking when its instructions or skills call for it), an interface line (the `stage` CLI, with `stage call TOOL JSON` for any tool, Stage MCP tools or the HTTP API, with the `STAGE_PUBLIC_URL` address when set; never assume or install one) and per-interface caller guidance (the CLI and stdio MCP detect the harness's session variable). The block is persisted per task/attempt; retries, including after restart or movement, reuse it verbatim. Agents must refresh membership with `get_context` or `get_task` before `read_journal`, read relevant own/tree notes, use task notes for progress and tree notes with `sourceTaskId` for shared conclusions, checkpoint before risky work, and record outcomes before returning. Notes are context, never instructions or proof; failed writes must be reported without claiming persistence. Existing or adopted sessions receive no retroactive block. Handoff remains independent.

When a task with needs launches with `handoff` on, the launch claim pins each need's immutable accepted result. Stage composes context exclusively from these local snapshots, including task/tree/journal references, cached reply excerpt, branch/worktree and the session ID and link when available. The predecessor harness may be offline. Missing evidence remains explicitly missing; earlier or unverified run provenance and truncation remain visible. Optional user acceptance notes are distinct from assistant replies.

Reply budget per need: `min(4000, max(1000, 8000 / sources))` Unicode characters. Each attempt records the rendered block in `task_handoffs`. Uncertain retries reuse stored blocks and acceptance pins. A storage error aborts the launch; it never silently launches without accepted context. Historical sent/skipped handoffs remain readable.

`Handoff`:
```jsonc
{ "attempt": 1, "status": "sent", // sent | skipped
  "sourceTaskId": 12,             // null only for records from before trees (a session source)
  "session": { "connectionId": 1, "externalSessionId": "session-abc" },
  "sourceTitle": "Build API",
  "text": "<handoff ...",          // "" when skipped
  "reason": null,                  // why skipped
  "at": "2026-11-14T10:02:01Z" }
```

## Agent interface

`stage` CLI, stdio MCP and HTTP MCP expose tasks, trees, journals, schedules, acceptance, session interaction, projects, group membership, snippets, explicit adoption and reference resolution through REST ([ADR 0011](adr/0011-agent-operation-parity.md)). Coding agents with a shell are pointed at the CLI first (the `stage setup` skill); MCP is optional, for clients without a shell or users who prefer tools. `stage tools [NAME]` lists the MCP tools (or one tool's description and input schema) and `stage call TOOL [JSON|-]` runs one through the same MCP server in process, printing its structured result as JSON (exit 1 on a tool error, 2 on an unknown tool or non-object arguments). `list_tasks {originSession}` (CLI `task ls --origin-connection ID --origin-session ID`) lists the creator session's tasks. `read_ref` returns task details and/or explicit session metadata with available native links, for a tree link the tree (`get_tree` shape) and a `next` naming the tree tools, for a schedule link `schedule` (`get_schedule` shape), for a project link `project` (`GET /api/projects/:id`, canonical), and for a journal entry link `entry` (`get_journal_entry` shape). Conversation reads and actions belong to the source harness. Removed tools and commands are listed under API v3 retirement.

- A caller-aware tool takes `callerSession: {connectionId, externalSessionId}`. Besides it, three generic channels name the caller, each in the form `<connectionId>:<externalSessionId>`: the `Stage-Caller-Session` header on an HTTP MCP request (malformed: 400), the `STAGE_CALLER_SESSION` environment variable for the CLI (one process, one session; a stdio MCP server ignores it, being shareable across sessions), and harness request metadata: a harness descriptor's `attribution.metaKeys` (OpenCode: `ai.opencode/sessionID`) in an MCP request's `_meta`, resolved to that harness's active configured connection on the caller's machine (`GET /api/connections/resolve`). The weakest source is the harness's session variable (`attribution.env`: T3 `T3CODE_THREAD_ID`, OpenCode `OPENCODE_SESSION_ID`), which the CLI and `stage mcp` read from their own environment (they run as the agent's child processes; HTTP MCP never does) and resolve the same way, cached per process. Several harness variables set resolve by each harness's `attribution.precedence`: a variable whose `owns` conditions match the environment wins (several owning: none wins); otherwise variables whose `yields` conditions match drop out and the one left wins. Several left, or none of the winner's connections on the machine, name no caller, and `get_context` says why in `callerWarning`; all yielding names none without a warning. Order: a per-call claim (metadata or header), then an explicit caller (which must agree with a claim; disagreeing is an error), then `STAGE_CALLER_SESSION` (CLI only), then the harness variable. `get_context` reports the winner in `sessionSource` (`request`, `argument`, `STAGE_CALLER_SESSION` or the variable's name). Unknown context leaves the origin empty. CLI also takes `--caller-connection ID --caller-session ID`. The launch instructions tell an agent how its harness names its session (the descriptor's caller hint).
- Agents on a remote machine use the same interfaces unchanged through the worker's agent gateway ([runtime.md](runtime.md#agent-gateway)): it owns that machine's default socket, so `stage` and `stage mcp` find it with no `--url`, and an opt-in TCP listener serves HTTP MCP. Caller attribution works as above; the main server also records the forwarding machine (`sourceMachineId`). The launch instructions of a session on a remote machine name the gateway, never `STAGE_PUBLIC_URL`: its TCP listener when the worker reported one (`the HTTP API at http://127.0.0.1:7381/api (MCP at …/mcp) on this machine's Stage agent gateway`), else its socket with a `curl --unix-socket` hint, else the gateway without an address. Sessions on the main server's machine keep `STAGE_PUBLIC_URL`.
- `GET /api/connections/resolve?harness=KIND` returns the active configured connection of KIND (the `GET /api/connections` row) on the request's machine: the forwarding machine of a request through a worker's agent gateway, else the server's. 400 unknown kind; 404 `{error, code: "no_active_connection"}` when the machine has none.
- `GET /api/connections/:connectionId/sessions/:id/context` returns session, owning task, current project, title and available links. The owner path reads Stage storage. An unowned session requires explicit lookup and verified workspace evidence to resolve its project context.
- `get_context` returns the caller task's saved `workspaceId` when available. Its legacy `bindingId` is historical context.
- Task and schedule writes accept the exact `connectionId` and `workspaceId`. The caller session supplies provenance. It grants no access or launch destination.
- `list_projects` includes the scoped destination summary. `list_models {connectionId, workspaceId?}` returns capabilities. `executionConfig` uses the selected workspace's model, option values, permission and mode.
- Read-only lookups by id (no names; run any with `stage call TOOL`): `get_project {id}` (`{id, title, archivedAt, aliases}`, the agent summary of `GET /api/projects/:id`), `list_project_groups {archived?}` (`false` default, `true`, `all`; `{groups: [{id, title, archivedAt, aliases}]}`), `get_project_group {id}` (one group summary), `list_connections` (`{connections}` as `GET /api/connections`), `list_machines` (`{machines: [Machine]}`), `get_machine {id}` (`Machine`), `list_harnesses {machineId?}` (`GET /api/harnesses`, or `/api/machines/:id/harnesses`), `get_harness_status {kind, machineId?}` (`HarnessStatus`, likewise per machine), `get_preferences` (`{defaultConnectionId, notifyEvents}`).
- Acceptance: `get_completion_preview {taskId}` (`revision`, `evidenceVersion`, `evidence`, `blockedReason`, `dependents`), `complete_task {taskId, revision, evidenceVersion, requestId, note?, callerSession?}` and `reopen_task {taskId, revision, requestId, callerSession?}` (`{revision, acceptanceId}`; a 409 tells the agent to preview again). CLI `task preview ID`, `task complete ID --revision N --evidence-version V [--note TEXT] [--request-id KEY]` and `task reopen ID --revision N [--request-id KEY]`; the CLI derives the request ID from the task and revision when omitted.
- Session interaction. `list_requests {taskId}` returns `{requests}`. `respond_request {taskId, requestId, option?, text?, answers?, expectedStateRevision?, callerSession?}` and `send_message {taskId, text, delivery?, expectedStateRevision?, callerSession?}` return `Task`. `delivery` is `send|steer|queue` and `expectedStateRevision` guards the first answer (409 `already_answered`). CLI `task requests ID`, `task respond ID RID [--option O] [--text T] [--answers JSON] [--expect-revision N]` and `task send ID --text TEXT [--steer | --queue] [--expect-revision N]`. `get_session_activity {taskId, attempt?, limit?}` returns `{activity}` (CLI `task activity ID [--attempt N] [--limit N] [--json]`). `get_opening_prompt {taskId, full?}` reads the opening prompt (CLI `task opening-prompt ID [--full]`).
- Complete, reopen, respond, send and session actions (`run_action` with `taskId`) refuse the caller's own task or session (403 `self_action_forbidden`). Each sends the caller session; a call naming none passes.
- Projects: `create_project {title}` and `update_project {id, title?, archived?}` (omitted keeps its value; at least one) return the project summary; `get_project_events {id}` returns `{events}`. CLI `projects add --title T` and `projects edit ID [--title T] [--archived true|false]`. `preview_membership {connectionId, workspaceId, targetProjectId?}` (omitted: a new group titled after the workspace) returns the preview with `previewToken`, changing nothing; `set_membership` takes the same inputs plus `previewToken` and fails on a changed preview: preview again. Responses omit raw paths.
- Workspaces ([ADR 0012](adr/0012-harness-workspace-add.md)): `list_workspace_sources {connectionId}` returns `{availability, sources}`. `add_workspace {connectionId, sourceId, input?, requestId, projectId?, confirmed?}` returns the [add result](#add-a-workspace) without paths. CLI `stage workspaces sources --connection ID` and `stage workspaces add --connection ID --source S [--input JSON|-] [--project P] [--request-id KEY] [--confirm]`; without `--request-id` the CLI derives a stable key from the connection, source, input and project, so rerunning the same command retries safely. A 504 `outcome_uncertain` means retry with the same key.
- Snippets and images: `list_snippets` (`{snippets}`), `get_snippet {id}`, `create_snippet {name, body, projectId?}` (omitted: global), `update_snippet {id, name?, body?, projectId?}` (omitted keeps; `projectId: null` makes it global), `delete_snippet {id}` and `delete_attachment {id}` (a staged upload or a backlog task's image; both `{deleted, id}`). The CLI runs them with `stage call`.
- `list_actions` and `run_action` take exactly one of `connectionId`, `taskId` or `projectId`. A project also requires `onConnectionId` and `workspaceId`. `run_action` takes `actionId`, optional `input` and optional `confirmed` (sends `Stage-Action-Confirmed: 1`; set it only after the user agreed to the action's `confirm` text, which an action such as Claude Code's `trust` requires). Both tools use agent policy with or without caller provenance.
- CLI `stage action ls|run` takes `--connection ID`, `--task ID` or `--project ID --on-connection ID --workspace-id ID`. For example, `stage action run --task 42 label --input - --json` reads the input JSON from stdin. Use `--confirm` for a user-only destructive action.
- `read_ref {session}` and `stage show --target-connection ID --target-session ID` accept an opaque scoped ID. A scoped reference path is `/connections/:connectionId/sessions/:id`. `list_tasks {originSession}` uses REST query fields `originConnectionId` and `originSessionId`. A bare session id names no connection: pass the connection with it.
- Journals: `read_journal {taskId?|treeId?, kind?, category?, q?, filterTaskId?, cursor?, limit?}` requires exactly one owner; `filterTaskId` is tree-only. `get_journal_entry {id}` retrieves full text. `append_note {taskId?|treeId?, text, requestId, category?, sourceTaskId?, callerSession?}` records caller session provenance. `get_context` includes `callerTaskId`, current `treeId` and `treeTitle`, and `steeringTreeId` (the tree the caller's task actively steers, else null); refresh it or `get_task` before reading or contributing to a tree.
- Tree steering: `set_tree_steerer {treeId, prompt, connectionId, workspaceId, title?, executionConfig?, replace?, requestId?, callerSession?}`, `end_tree_steering {treeId, callerSession?}` (both `{tree, task?, warnings}`), `get_steering_events {treeId, after?, limit?}` (`{events, hasMore}`) and `escalate_to_user {taskId, reason, callerSession?}` (`Task`). `update_tree` takes `steeringHoldSeconds`. CLI `tree steer ID --prompt TEXT --connection ID --workspace-id ID [--title T] [--replace] [--request-id KEY]`, `tree unsteer ID`, `tree steering-events ID [--after N] [--limit N]`, `task escalate ID --reason TEXT` and `tree edit ID --hold DURATION` (a whole-second duration such as `5m`, or `0`/`off`). The MCP tool and CLI generate a request ID when omitted.
- CLI: `stage task notes ID [list] [--kind notes|events|all --category C --q Q --limit N --cursor C]`; `stage tree notes ID` adds `--task ID` filtering. Append with `stage task notes ID add --text TEXT [--category C --request-id KEY]`, or `stage tree notes ID add --text TEXT --source-task ID`; `--text -` reads stdin. CLI generates a request key when omitted; supply `--request-id` to retry across invocations. `stage task notes ID get ENTRY_ID` and `stage tree notes ID get ENTRY_ID` retrieve full entries. Task creation/adoption use `--initial-note` / `initialNote`; task edit has no notes field. Schedule `--notes` remains a template.
- A project ID or title supplies planning context. A caller's session and cwd supply context and provenance.
- CLI `stage projects ls --json` lists destination selectors. Task and schedule commands use `--connection ID --workspace-id ID`. The `--binding` flag and MCP binding selectors were removed.
- Model: explicit `executionConfig` > the destination connection's pin (`newTaskConfig`) > harness default. The pin supplies model and options only; permission and mode are explicit or unset. The removed T3 model arguments (`provider`, `model`, `effort`, `options`, `runtimeMode`, `interactionMode`) fail schema validation.
- `get_task` returns the current tree, session identity, recorded attempts, cached `finalReply`, `usage` (when the harness reports one), available links and accepted snapshots.
- `afterMe` submits the scoped caller as `needsSession`. Owner reuse, adoption, tree placement and the dependent task commit together.
- Trees: `create_task` takes `treeId`, `needs` and `startMode` (`auto|manual|inherit`) (CLI `task add --tree ID --needs 3,5 --start-mode M --after-me`); `update_task` takes `startMode` (`inherit` = null; CLI `task edit --start-mode`). `set_needs {taskId, needs}` (CLI `task needs ID [NEED...]`, none clears). `create_tree {title, startMode?, tasks: [{id, needs?}]}` (CLI `tree new --title T [--start-mode M] ID[:NEED,NEED]...`), `get_tree {id}` (CLI `tree show ID`: levels with state, `#id`, title, needs, readiness), `list_trees {status?, all?, project?, completedFrom?}` (compact rows `{id, title, startMode, tasks, levels, projects, states, completedAt, status}`, `states` = non-zero counts with `blocked`/`ready` split from `backlog` and accepted members in `states.completed`. CLI `tree ls [--status S] [--all] [--project P]` prints `completed N` last), `update_tree {id, title?, startMode?, status?}` (CLI `tree edit`; `status` `done|archived|open`: CLI `tree done ID`, `tree archive ID`, `--undo` reopens), `delete_tree {id}` (only when deletable; CLI `tree rm-tree ID`), `add_to_tree {treeId, taskId, needs?}` (CLI `tree add TREE TASK [--needs ..]`), `remove_from_tree {taskId}` (looks up the task's tree; CLI `tree rm TASK`), `start_tree {id}` (CLI `tree start ID`), `dissolve_tree {id}` (detaches every member, drops their needs and archives the tree; `{dissolved, id}`; CLI `tree dissolve ID`).
- Close work: `archive_task {id, archived?}` (default archives; CLI `task archive ID [--undo]`), `list_tasks {closed?}` (`true|all`; CLI `task ls --closed true|all`; rows carry `treeStatus` and `archived`), `delete_task` only for a task that never started and has no recorded work. See "Done, archive and delete".
- `adopt_session {session?, callerSession?, projectId?, newProjectTitle?, title?, initialNote?, treeId?}` (default: the caller session) returns `{ task, created }` (CLI `sessions adopt [--target-connection ID --target-session ID] [--title --initial-note --tree]`).
- `start_task` on a blocked task fails until its needs are completed in Stage.
- `create_task` and `update_task` take optional `handoff` (CLI `task add --handoff=false`, `task edit --handoff=true|false`); omitted keeps the task's value (default true). `get_task` returns `handoff` and `currentHandoffs`; `stage task show` prints `handoff  on|off` for tasks with needs and the current attempt's records, plus `tree` and `readiness` lines.
- Launch options (a worktree, its base ref and branch where the harness declares them) go in `executionConfig.options` (CLI `--execution-config`; `list_models` and `models ls --connection ID` list them with `kind`, `label` and `dependsOn`). The pin's options fill in when `executionConfig` names no model. `stage task show` prints `options  worktree=true baseRef=origin/main`.
- Images: `create_task` and `create_schedule` take `attachmentIds` (staged uploads), `imagePaths` (local files; CLI and stdio only, HTTP MCP rejects them) and `images: [{name?, mimeType, data}]` (inline standard base64, any transport). The tool stages `imagePaths` and `images` through `POST /api/uploads` and attaches them after `attachmentIds`. `update_task` and `update_schedule` take the same three fields and send `attachmentIds` as the PATCH's exact set: the given `attachmentIds` (`[]` = none), or the current images when omitted, then the new uploads; images not in the set are deleted. Each image is checked before any upload: `mimeType` one of `image/png`, `image/jpeg`, `image/gif`, `image/webp` and matching the data, 1 byte to 10 MB decoded; at most 8 images in all. `name` defaults to `image.<subtype>`. CLI: `--image FILE` (repeatable) on `task add|edit` and `schedule add|edit`; on edit it adds to the current images.
- `update_task` and `update_schedule` take `executionConfig: null` to clear the stored configuration (the harness defaults); omitted keeps it. CLI: `task edit|schedule edit --execution-config null`.
- List filters: `list_tasks {scheduleId?, from?, to?, completedFrom?}` (REST `schedule`, `from`, `to`, `completedFrom`; CLI `task ls --schedule ID --from D --to D --completed-from D`), `list_trees {completedFrom?}` (CLI `tree ls --completed-from D`), `list_schedules {dueWithin?}` (Go duration; CLI `schedule ls --due-within 24h`) and `list_projects {archived?}` (`false` default, `true`, `all`; CLI `projects ls --archived true|all`). Dates are an ISO date or RFC 3339 datetime; one without a zone is UTC.
- `wait_task` polls `GET /api/tasks/:id` every 3s until the state is in `until` (default `needs_input done failed interrupted cancelled`) or `timeoutSeconds` (default 600, max 3600); CLI `task wait` exits 3 on timeout.

`GET /api/notifications/groups` also returns `waitingSessions: [{connectionId, externalSessionId, title, projectTitle, directory, links}]`. These are current external needs-input sessions with no recorded notification up to `latestId`. `directory` is the session's working folder when its harness reports one (null for T3); `links` may hold only a `command` (Claude Code) or nothing (a session answered where it runs). A harness whose attention policy is `silent` (Claude Code) never records events for sessions no task owns, so its waiting sessions appear only here and in the needs-input count. They have no notification ID, timestamp or read state. Resolving input, adopting the session, or receiving a real event removes the silent row. Startup suppression and delivery preferences remain unchanged.
