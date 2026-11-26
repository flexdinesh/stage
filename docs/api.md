# stage REST API

Base path `/api`. JSON in and out (`Content-Type: application/json`). Field names camelCase. TypeScript mirror: `packages/app/src/api/types.ts` (change both together).

## Conventions

- Timestamps: ISO 8601 strings (UTC, e.g. `"2026-11-08T10:00:00Z"`). Nullable fields are `null`, never omitted.
- IDs: app ids (`id`, `projectId`, `taskId`, `treeId`, `needs`) are JSON numbers. T3 ids (`externalProjectId`, `externalSessionId`) are strings.
- `createdBy`: the user id (number) of the row's creator on `Project`, `Task`, `Tree`, `JournalEntry`, `Schedule`, `Machine` and the `GET /api/harnesses` machine. Always emitted; see [Users](#users).
- `sourceMachineId`: the machine whose worker agent gateway forwarded the create, on `Project`, `Task`, `Tree`, `JournalEntry` and `Schedule`; `null` when not through a gateway. Always emitted; see [Users](#users).
- Lists are wrapped: `{ "projects": [...] }`, `{ "tasks": [...] }`, ...
- PATCH bodies are partial: omitted fields are unchanged; `null` clears a nullable field.
- Repeatable list query params accept repetition or commas: `?state=running&state=needs_input` == `?state=running,needs_input`.
- `from` / `to` query params: ISO date (`2026-11-01`) or datetime. `from` inclusive, `to` exclusive. Filter on `createdAt` unless stated.
- Substring filters (`q`) match case-insensitively, non-ASCII letters included; every character is literal (`%`, `_` are not wildcards). Folding of rare Unicode characters may differ between the SQLite and Postgres engines.
- Errors: non-2xx responses have body `{ "error": string }`. Some 409s and the worker routes' 401s add a machine-readable `"code"`.
- Every response carries `Stage-Api-Version: <n>` (currently `6`; changes only on breaking API changes). The Go client (CLI, stdio MCP) refuses a response with a different value; a missing header is accepted.
- Request origin policy (`/api` and `/mcp`; [runtime.md](runtime.md#request-origin-policy)): requests with a Host that is not allowed, cross-site requests and untrusted cross-origin writes get 403; a non-empty write body that is not `application/json` (or `multipart/form-data` for uploads) gets 415.

| Status | Meaning |
|---|---|
| 400 | invalid body or query (bad enum, missing required field, bad JSON) |
| 401 | worker routes only: bad, used or expired pairing code, missing, invalid or revoked machine token; any route: a machine token outside `/api/worker/*` ([Machines](#machines)) |
| 403 | rejected by the request origin policy (Host, cross-site, cross-origin) |
| 404 | resource not found |
| 409 | conflict: action not allowed in current state, duplicate unique value, T3 setup refused (other T3 environment, auto-pair unavailable) |
| 415 | write body not `application/json` / `multipart/form-data` |
| 502 | T3 call failed (T3 error message in `error`), or the `t3` CLI failed (auto-pair) |
| 503 | T3 not connected (no valid token) or DB unavailable |
| 500 | unexpected |

## Enums

| Name | Values |
|---|---|
| TaskState | `backlog` `launching` `running` `needs_input` `done` `settled` `failed` `interrupted` `cancelled` (`done`: observed execution finished; `settled` remains historical) |
| ThreadStatus (T3) | `idle` `preparing` `queued` `starting` `running` `waiting` `completed` `interrupted` `failed` `cancelled` `rolled_back` |
| RuntimeMode | `approval-required` `auto-accept-edits` `auto` `full-access` (task default `full-access`) |
| InteractionMode | `default` `plan` (default `default`) |
| SendMode | `auto` `queue` `steer` `restart` (default `auto`) |
| StartMode | `auto` (a tree member launches once its needs are completed in Stage) `manual` (it reads `ready` and notifies; the user starts it). New tree default `auto`; a task's own `startMode` null inherits the tree's |
| Readiness | `blocked` (a need lacks user acceptance) `ready` (every need has user acceptance, no start time, and manual or no needs); backlog tree members only, else null |
| NotificationEvent | `needs_input` `failed` `ready` `done` `settled` |
| ProjectSource | `app` (created via stage) `t3` (imported from T3) |
| ThreadSource | `app` (thread belongs to a task) `t3` (any other thread) |
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
  "apiVersion": 8,
  "instanceId": "9f2c4e1a7b3d5c60",
  "startedAt": "2026-11-16T09:00:00Z",
  "schema": 4,
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

## Users

Stage has no sign-in and no API auth (browser and API access stay behind the proxy boundary). One user exists, the default user (id 1), and every write is attributed to it ([architecture.md](architecture.md#users)). Sign-in identities will map to users later; machine tokens (ADR 0002) are machine principals, never users.

**Source machine.** A write that arrived through a worker's agent gateway ([Agent gateway route](#apiworkergatewayapi-and-apiworkergatewaymcp)) also records the forwarding machine: `sourceMachineId` on `Task`, `Tree`, `JournalEntry`, `Schedule` and `Project`, next to `createdBy` (still the acting user). `null` when the write did not come through a gateway (the UI, an agent on the main server's machine, background work), for derived journal events (tree, membership, needs and task-state events; [storage.md](storage.md#journal-events)), discovered projects and schedule-spawned tasks (the schedule spawns them, also on Run now). The machine may since have been removed.

### `GET /api/me`
200 `User`, the acting user:
```json
{ "id": 1, "displayName": "Default user", "createdAt": "2026-11-22T10:00:00Z" }
```

## Machines

A machine runs harnesses ([ADR 0002](adr/0002-machines-and-workers.md)). Machine 1 is the local machine, inside this server's process; its default label names where the server runs (the hostname, or the `STAGE_PUBLIC_URL` host in a container, else `Stage server`; [runtime.md](runtime.md#machine-profile-and-hosted-harnesses)), set once and kept across renames. A remote machine runs `stage worker` and is added by pairing. Hierarchy: user, machines, harness connections, sessions.

```jsonc
// Machine
{
  "id": 2,
  "label": "work laptop",
  "kind": "remote",              // local (id 1) | remote
  "online": false,               // local: always true; remote: its worker channel is live
  "status": "offline",           // online | offline | needs_update (its worker last connected with another apiVersion; cleared when it connects again)
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
A round trip through the machine's worker channel. 200 `{ "machineId": 2, "latencyMs": 3, "version": "v1.5.0" }` (`version`: the build the worker reports). Machine 1 answers at once (`latencyMs` 0, the server's version). 409 `machine_offline` without a live channel; 504 `lost_reply` when no reply came within 10s (the channel dropped or the worker did not answer); 502 with the worker's error `code` when it answered with an error. 404 unknown or removed.

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

- requires `Stage-Api-Version` equal to the server's `apiVersion`; missing or different: 409 `{error, code: "api_version_mismatch"}` (also on join, so a machine is never created for a worker that cannot connect);
- except join, requires `Authorization: Bearer <machine token>`. Missing: 401 `machine_token_required` with `WWW-Authenticate: Bearer realm="stage-worker"`. Malformed, wrong, or its machine removed: 401 `machine_token_invalid` with `error="invalid_token"`. An unknown worker path needs a token before it gets 404.

A machine token (`stage_mt_<id>_<secret>`, 256 random bits) is a machine principal, not a user. One exists per machine; the server stores only its SHA-256 and compares in constant time. It is accepted only here: `Authorization: Bearer stage_mt_...` on any other `/api` route or on `/mcp` gets 401 `machine_token_not_accepted`. Other `Authorization` values (a proxy's own) are ignored there.

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
The worker channel's down half ([runtime.md](runtime.md#worker-channel), [ADR 0002](adr/0002-machines-and-workers.md)): a `text/event-stream` that stays open. Request headers: `Stage-Version` (the worker's build, recorded as the machine's `version`) and `Stage-Worker-Gateway` (its agent gateway listener URLs, comma-separated, e.g. `unix:///run/user/1000/stage/stage.sock,http://127.0.0.1:7381`; kept in memory for the session and named in launch instructions, see [Agent interface](#agent-interface)). Events:

- `hello` first: `{ "session": "...", "machineId": 2, "heartbeatSec": 15, "timeoutSec": 45 }`.
- `rpc`: an envelope `{ "id", "kind", "deadline"?, "connectionId"?, "call"?, "body"?, "error"? }`. `kind` is a call or `cancel` (the caller gave up on call `id`). Calls: `ping`; the remote machine's adapter calls `adapter.health|features|featuresForWorkspace|workspaces|observe|launch|stop|attention|opening|resolveLink|correlate|events|requests|respond|send` (`connectionId` names the connection), its ops `ops.inventory|checkout`, and `machine.describe` (`{ profile, connections: [{ connectionId, traits }] }`). Bodies and error codes: [execution.md](execution.md#remote-machines).
- `: keepalive` comments every 15s.

A newer stream of the same machine replaces this one (the old stream ends). Removing the machine ends it at once.

#### `POST /api/worker/channel`
The up half: one envelope per request (at most 32 MiB), header `Stage-Channel-Session: <hello.session>` (400 without it). `kind`: `heartbeat` (about every 15s) or `result` (a reply to an `rpc` call, by `id`; late, duplicate or unknown ids are dropped): 204. A worker-initiated call gets 200 with a `result` envelope: `ping`, `machine.changed` (what the worker hosts changed; the server describes it again), and callbacks that name the waiting `rpc` call in `call`: `checkpoint.save` and `launch.image` (of `adapter.launch`), `events.observation` (of `adapter.events`). A callback whose call no longer waits gets a result with error `call_ended`. A session that was replaced or ended: 409 `channel_replaced` (reconnect the stream). Any message counts as contact.

#### `/api/worker/gateway/api/...` and `/api/worker/gateway/mcp`
A worker's agent gateway forwards the local `/api/...` and `/mcp` requests of agents on its machine here ([runtime.md](runtime.md#agent-gateway)), any method, with the machine token and `Stage-Api-Version` (the worker route checks above apply first). The server serves the rest of the path as that same request on its own `/api` or `/mcp`, with the same handlers, bodies, statuses and streams, and with the token's machine as the forwarding machine: writes record it as `sourceMachineId` ([Users](#users)). The `Authorization` header is dropped before the inner request; `Stage-Caller-Session` and body caller fields (`callerSession`, `sourceSession`, `originSession`) pass unchanged.

- Machine management and per-machine harness setup changes are refused: `POST /api/machines/pairings`, `DELETE /api/machines/pairings/{id}`, `PATCH` and `DELETE /api/machines/{id}`, `POST /api/machines/{id}/ping`, `PUT /api/machines/{id}/harnesses/{kind}/settings`, `POST /api/machines/{id}/harnesses/{kind}/actions/{action}` and `DELETE /api/machines/{id}/harnesses/{kind}/connection` get 403 `gateway_forbidden` (a token that could mint pairing codes could outlive its revocation). Reads (`GET /api/machines`, `GET /api/machines/{id}/harnesses...`) work.
- `POST /api/connections/:connectionId/observations` pushes only for the forwarding machine's own connections; another machine's is 403 `gateway_forbidden`.
- `/api/worker/*` and any path that is neither `/api/...` nor `/mcp`: 404.

## Harness connections (API v6)

A connectable harness (a harness, `connected` or `hosted`, this server composes a connector for on the machine, and the machine allows; `connectable` in `GET /api/harnesses`) is set up through four generic routes. Its descriptor's `setup` describes them: `fields` (`[{id, label, kind: text|url|secret|bool, help?}]`), `actions` (`[{id, label, input?: [field]}]`) and `disconnect` (bool). Hosts render that schema unless they register a harness-specific panel for the kind; product code never branches on the kind. `{kind}` unknown: 404. A kind the machine does not allow (`available: false`: a hosted harness on a profile without local processes, or on a remote machine for now): all four routes answer 409 `harness_unavailable` with that `reason` as the message, and the harness is never reached. A kind without connection setup here: 409. A hosted harness has no connection until its connect action creates one; a launch on a connection of a kind its machine does not allow answers 409 `connection_unavailable` with the reason.

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
  "reason": "",
  "fields": [{ "id": "url", "value": "http://127.0.0.1:3773", "set": false, "source": "default" }],
  "actions": [{ "id": "pair", "available": true }, { "id": "auto-pair", "available": false, "reason": "not_local" }],
  "details": { "...": "the harness's own setup state, below" }
}
```
- `connectionId`, `configured`, `connected`, `lastError`: as in health, from the same connector (`lastError` and `reason` are omitted when empty). `state` is a harness-defined word (T3: T3State; OpenCode: `unconfigured`, `connected`, `unavailable`, `rejected`; Codex: `connected`, `not_connected`, `signed_out`, `unreachable`, `not_installed`). `reason`: why it is not connected.
- `fields`: each setting's current value (`source`: `saved` or `default`). No environment variable sets a field. A secret's `value` is always `""`; `set` says whether one is stored.
- `actions`: whether each setup action can run now, with a `reason` when not.
- `details`: the harness's setup panel state (T3: the T3 details; OpenCode: the OpenCode details; Codex: the Codex details). Generic clients ignore it.
- Refreshes a stale probe first (bounded, T3: 3s; OpenCode health check: 4s; Codex: the daemon CLI's 20s). Codex reuses a probe for 5s; `?refresh=1` forces one.

### `PUT /api/harnesses/{kind}/settings`
Body: one strict JSON object keyed by setup field id; `null` resets a setting to its default. 200 `HarnessStatus` (freshly probed), plus `replaced` (the retired connection, as in `GET /api/connections`) when the change retired the active connection. 400 invalid or unknown field. 409 the harness's own conflicts (below).

### `POST /api/harnesses/{kind}/actions/{action}`
Body: the action's input object (strict; empty means `{}`). 200 `HarnessStatus` (not re-probed), plus `replaced` as above. 404 unknown action. Errors are the harness's (below).

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
  "lastError": null,
  "lastFullSyncAt": "2026-11-08T10:00:00Z",
  "reachable": true,
  "checkedAt": "2026-11-08T10:00:05Z",
  "probeError": null,
  "t3": { "environmentId": "env_123", "label": "controlbox", "serverVersion": "0.1.9" },
  "autoPair": { "available": true, "reason": null },
  "sync": { "running": false, "startedAt": null, "projects": 0, "threads": 0 }
}
```
`environmentId`, `clientId`, `tokenExpiresAt`, `lastError`, `lastFullSyncAt`, `checkedAt`, `probeError`, `t3`, `autoPair.reason` and `sync.startedAt` are nullable.

- `connected`: a valid token T3 has not rejected (same as the T3 entry of `/api/health` `harnesses`). T3 is configured only when paired: a stored token or a saved admin token. An unpaired T3 is never contacted in the background (no probes or sync until paired).
- `state` (T3State), the first problem in this order: `unreachable` (nothing answered as T3 at `t3Url`), `unpaired` (no token), `expired` (token past expiry), `rejected` (T3 answered 401 to the token), else `connected`.
- `t3UrlSource`: `settings` (saved through the `url` setting) or `default` (`http://127.0.0.1:3773`). `publicUrl` is the saved Browser URL (`publicUrl` setting), else `t3Url`. `adminTokenConfigured`: an admin token is saved.
- `reachable`, `checkedAt`, `probeError`, `t3`: the last unauthenticated probe of `{t3Url}/.well-known/t3/environment` (`probeError` is a short reason such as `connection refused`, `timed out`, `not a T3 server (HTTP 404)`). Probes are cached 10s, success or failure, and concurrent refreshes share one request. The status route refreshes a stale probe (waiting at most 3s); `/api/health` never probes.
- `autoPair`: whether the `auto-pair` action can work. `reason` when not: `not_local` (`t3Url` is not loopback: `localhost`, `127.0.0.0/8`, `::1`), `cli_not_found` (no `t3` CLI: `T3_BIN`, then `PATH`, `~/.local/bin`, `~/.bun/bin`, `~/.local/share/pnpm`, `~/.npm-global/bin`, `~/.volta/bin`, `/usr/local/bin`, `/opt/homebrew/bin`), `fake` (`T3_FAKE=1`).
- `sync`: the running full sync (`projects` and `threads` mirrored so far); all zero when none runs.
- `connectionId`: this machine's active T3 connection (0 when none). It changes when another T3 replaces it.

Instances: each T3 environment is its own harness connection (`GET /api/connections`); one T3 connection is active per machine at a time (these routes set up the local machine's). There is no default harness: a fresh install has no T3 connection until the first successful pairing creates one (or a refreshed status finds a valid token and the T3 identity). The first successful pairing stamps the active connection with that environment (a server paired before identities existed is stamped the first time it reads a valid token and the T3 identity). While paired, the T3 URL may change only to an address serving the same environment. Pairing a different environment replaces the connection: the old one retires with its tasks, sessions, bindings and mirror (history only: never polled, linked or launched on again), a new connection becomes active, and unlaunched backlog tasks and schedules move to it. Nothing is bound on the new connection until the user rebinds. Pairing a retired connection's environment again revives it (its bindings and tasks come back). A connection that never held anything is restamped in place instead. Moved instances: when a disconnected T3 connection's environment is active and connected on another machine (the T3 was paired again from a worker), the server retires the disconnected one onto it: unlaunched backlog tasks, schedules and the default connection move there, and its tasks stay as history. It checks at startup, every full sync interval (5 minutes), after a pairing, when a worker connects and after `POST /api/sync`; it never retires a connection on an offline worker (its state is unknown) or one that is still connected.

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
Refresh external workspace discovery and task-owned sessions, then import deliberate harness projects (see Projects). Stage project titles remain Stage-owned; changed paths on existing bindings require confirmation. External active-session discovery belongs to the notification watcher. No historical thread sweep. Synchronous.
200 `SyncResult`:
```json
{ "projects": 12, "threads": 776, "finishedAt": "2026-11-08T10:00:00Z" }
```
409 sync already running. 503 not connected. 502 T3 error.

## Projects

Stage owns project IDs, titles and archive state. Creating a project needs no
harness. Discovery never renames or deletes Stage projects. The sync pass imports
deliberate harness projects through the project identity ladder: a matching
workspace path on the same machine binds to the existing project, the one
project bound on another machine to the same Git origin remote and subproject
gains it, ambiguous matches wait for a manual bind, a new workspace creates a
project, and a discovered project with no tasks follows its harness archive
until a user archives or unarchives it
([architecture.md](architecture.md#project-import)). Existing T3 projects keep
their Stage IDs and migrate to explicit bindings.

`Project`: `{id, title, bindings, archivedAt, createdAt, createdBy, sourceMachineId, updatedAt, lastActivityAt, origin, importedAt, repos}`.
`bindings` contains active `{id, projectId, connectionId, harness,
externalProjectId, workspace, revision, status, observedWorkspace, origin}` records.
Status is `available`, `missing` or `changed`. Binding `origin` is `manual`,
`imported` (discovery created the project for it), `path` (discovery bound it
to the project at that path on the same machine) or `remote` (discovery bound
it to the one project bound on another machine to the same Git origin remote
and subproject). Project `origin` is `discovered` or null;
`importedAt` is when discovery created it. `repos` lists the distinct
normalized origin remotes (`host/owner/name`) of its bound workspaces. The
remote is a cross-machine match key, never proof within one machine
([architecture.md](architecture.md#project-import)). Paths belong to bindings, never to Stage identity. Activity
includes tracked sessions, tasks and journals.

- `GET /api/projects?archived=false|true|all`: `{projects: Project[], imports: [{connectionId, harness, count, at}]}`. `imports` counts, per connection, the projects discovery created in the last 7 days, newest first.
- `POST /api/projects`: `{title}`, returns 201 Project. Title required, trimmed.
- `GET /api/projects/:id`: Project or 404.
- `PATCH /api/projects/:id`: `{title, archived}`, returns Project.
- `GET /api/harnesses`: the harness kinds this server knows and what this machine can run: `{machine: {id, label, createdBy, profile: {kind, localProcesses}}, harnesses: [{kind, name, hosting, closures: [{signal, provenance, setting, completed, via}], setup: {fields, actions, disconnect}, attribution: {metaKeys, env?, precedence?: {owns?, yields?}, origin?: {env?}}, launch, sessionHint?, wraps?: {origins}, available, reason?, connectable}]}`. `sessionHint`: what names one of the harness's sessions when a user tracks it (Codex: a thread ID or a pasted `codex resume` command; Claude Code: a session UUID, the 8-character ID `claude agents` shows, or a pasted `claude attach` or `--resume` command); hosts show it as the Track session field's hint, and the field keeps its generic copy when it is absent. `setup` and `connectable`: see [Harness connections](#harness-connections-api-v6). `launch`: `reserved` or `correlated`, how a launch learns its session identity ([execution.md](execution.md#execution-contract-v2)). `attribution`: how the harness names an agent's session: `metaKeys` are MCP `_meta` keys it sends per call, `env` the variable it sets to the session id in agent processes, which the CLI and stdio MCP read (see [Agent interface](#agent-interface)). `precedence` ranks `env` when several harness variables are set: `owns` and `yields` are `[{env, value?}]` conditions (`value` absent: any non-empty value). `origin`: the harness records the client that started each session, so another harness may wrap it; `env` is the variable holding that origin in its agents (Claude Code `CLAUDE_CODE_ENTRYPOINT`). `wraps`: the harness runs other harnesses' sessions and leaves these `origins` in them (T3: `t3-code`, `T3 Code`); Stage refuses to track such a session on the wrapped harness (409 `session_refused`, "Track it through T3"), and when the wrapper's `env` is set (or a wrapped harness's origin variable holds one of its origins) the wrapped harness's `env` yields to it ([execution.md](execution.md#harness-registry)). `profile.kind`: `desktop` (a managed per-user service: the server's, or on a remote machine the worker service), `server` (foreground `stage serve` or `stage worker run`) or `container` (the Docker image). `hosting`: `connected` (Stage connects to the harness's service; available on every profile) or `hosted` (the harness runs as processes on this machine; available only when `profile.localProcesses`, else `available: false` with a `reason`; a remote machine reports its worker's last described profile, and the reason names the worker's fix, `stage worker run --local-harnesses` or the worker service; before its worker described it since the server started, every kind is available and setup calls answer 503 `machine_offline` or `machine_setup_starting`). `closures`: the closure signals the harness reports and the copy for each: `setting` labels its auto-complete toggle, `completed` and `via` describe an inferred completion with that `provenance`. Clients name harnesses and closures from this list, never from a list of their own; an unknown kind reads as its kind.
- `GET /api/connections`: `{connections: [{id, machineId, harness, configured, autoComplete, active, label, retiredAt, forgottenAt, tasks, bindings}]}`, active first. `machineId`: the machine that runs the instance (one local machine today). One connection per harness instance (a T3 environment, an OpenCode service); at most one active per harness on each machine. Select the returned IDs; never assume one (the T3 connection changes when another T3 replaces it). Both harnesses can be planned before setup. `autoComplete` (default true): a closure in this harness may complete a done task (see "Inferred completion"). `label`: the instance's name (T3 label, OpenCode address). `retiredAt`: when another instance replaced it (null while active); a retired connection keeps its tasks, sessions and bindings as history, is never polled, linked or launched on, and is `configured: false`. `forgottenAt`: set by DELETE. `tasks`, `bindings`: tasks and active bindings it holds.
- `PUT /api/connections/:connectionId/auto-complete`: strict `{autoComplete: boolean}`. 200 `{connection}`; 400 missing/invalid body; 404 unknown connection.
- `PUT /api/connections/:connectionId/new-task-config`: strict `{config: {model, options} | null}`, the model and options new tasks on the connection start with (`newTaskConfig` on its row; null or an empty config unpins). Checked against the connection's capabilities (no project). Pairing another instance of the harness carries the pin to the replacement. 200 `{connection}`; 400 unknown or replaced connection, a model or option value the connection does not offer, or a `permission` / `mode` (never pinned).
- `DELETE /api/connections/:connectionId`: forget a retired connection, or an active one that is disconnected (`configured: false`) on a machine that answers (the local machine or an online worker): it retires (the default connection is cleared; its tasks, backlog and schedules stay as history) and is forgotten at once. Removed outright when no task, session or launch references it; otherwise its tasks and history stay (still naming it), its identity is cleared (that instance can no longer revive it; pairing it again starts a new connection), its bindings are deactivated, and its discovered projects and shared paths are dropped. 204. 404 unknown or already forgotten. 409 the connection is connected (`Disconnect it first`), or its machine is offline.
- `POST /api/connections/:connectionId/observations`: an external bridge or hook pushes observations of the connection's sessions, for a harness Stage cannot poll or stream well ([execution.md](execution.md#execution-contract-v2) "Push"). Strict body `{observations: Observation[]}`, 1 to 100 (body at most 1 MiB); `Observation` is the adapter contract's JSON (`execution.Observation`: `session: {connectionId, externalSessionId}`, `project`, `title`, `state`, `active`, `stateReason?`, `closed?`, `updatedAt`, plus the optional `links`, `reply`, `usage`, `branch`, `directory`, timestamps). Each goes through the same validation and ingest path as an `execution.EventSource` event: the harness's policies, the task transition, inferred completion. `updatedAt` orders snapshots: one older than the stored observation never replaces it. 200 `{results: [{status, reason?}]}`, one per observation in order: `ingested` (the owning task took it), `ignored` (no task owns the session, left to discovery; or its task is launching and the launch reconciles it), `invalid` (malformed: unknown state or reason, contradictory activity, missing identity or `updatedAt`, a reason the harness may not report; nothing changes; `reason` says why). Polling continues and reconciles. 400 bad JSON, unknown field, an empty or oversized batch, or any observation whose session is on another connection (nothing is ingested); 404 unknown connection; 409 `connection_retired` (a replaced connection) or `connection_unavailable` with the reason, as for a launch (no adapter hosted for it; its machine does not allow the harness; or its machine is offline or needs an update: an offline worker is an offline harness, so nothing changes); 403 `gateway_forbidden` through a worker's agent gateway for a connection of another machine. A storage failure is 500 after the observations before it were ingested; pushing the batch again is safe. No MCP tool or CLI command pushes.
- Retired connections: binding a project, creating or moving a task or schedule to a retired connection returns 409 `code: "connection_retired"`. Tasks carry `harness`, `connectionActive` and `connectionLabel`; project bindings carry `connectionActive` and `connectionLabel`. A task on a retired T3 connection has null T3 links.
- `GET /api/connections/:connectionId/projects`: live discovery, returns
  `{projects: [{connectionId, externalId, title, workspace, projectId, repo}]}`.
  `projectId` is the current explicit owner, or null. `repo` is the normalized
  origin remote, `""` when unknown.
- `PUT /api/projects/:id/binding`: `{connectionId, externalProjectId, workspace}`.
  Revalidates the exact external project and path on the selected harness before binding. One active
  binding per project/connection; one Stage owner per external workspace.
  Rebinding retires the previous revision. Conflicts return 409.
- `DELETE /api/projects/:id/binding`: unbind T3, retaining history. Unbinding
  (either route) declines the workspace: discovery never binds it again until a
  manual bind. Rebinding a project away from a workspace declines it too.
- Removed: `POST /api/connections/t3/projects` (registering a directory in
  T3). Stage never asks a harness to create a project ([ADR 0007](adr/0007-harness-boundary.md)
  decision 11): add it in the harness, then refresh discovery. It answers
  404.

Use `GET /api/shared-paths` to read declared filesystem mappings. PUT to the same route with `{connectionId, harnessRoot, stageRoot}` to set a mapping. An empty Stage root removes it. `POST /api/projects/:id/bindings/:connectionId/match` checks live inventory and returns `{bound, suggestions, projects}` (`suggestions`: the reason per external project id). It walks the same ladder as import: the same path, or a shared Git common directory and subproject (below a shared path, or in place for a harness local to its machine), on the same machine, or the same Git origin remote and subproject as the one project bound on another machine, binds when it places exactly one unbound workspace in this project. Any other Git remote match (with the same subproject when both are known) requires confirmation. DELETE the scoped binding to unbind.

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

Instance check: an address change is checked against the stored identity evidence (bindings and observed sessions): a service that serves them is the same service at a new address and keeps the connection (as does a change on a connection that holds nothing). Otherwise 409 `code: "instance_changed"`, or `code: "instance_returned"` when the service proves to be a retired connection's (it serves that connection's workspaces and sessions), and nothing changes; resend with `newInstance: true` to confirm: the current connection retires (as for T3, see Instances), and a new one becomes active, or the proven retired one revives. A different service with the same repository at the same path is never bound silently. `connectionId` in the status changes with it.

Codex's setup (hosted; [ADR 0006](adr/0006-codex-app-server-harness.md)): field `binary` (the Codex CLI path; empty or `null` uses `codex` on PATH), action `connect` (`{}`), disconnect. `GET /api/harnesses/codex/status` `state`: `connected`, `not_connected`, `signed_out` (the daemon answers but Codex has no login), `unreachable` (the daemon is down or not answering), `not_installed` (the CLI is missing). `details`: `{binary, cliVersion, appServerVersion, codexHome, daemon: running|stopped|unknown, account: {type, email, plan} | null, fix, checkedAt}`. `fix` is a shell command that fixes the current problem (`codex login`, `codex app-server daemon start`), else `""`.

- `POST /api/harnesses/codex/actions/connect`: starts the daemon if it is down, reads the account and connects the Codex home as the connection's identity (label `Codex · <email>`). 409 `codex_not_installed`, `codex_signed_out` or `codex_unavailable` with the problem as the message; nothing changes.
- `PUT /api/harnesses/codex/settings`: `{binary}`. A connected Codex reconnects through the new CLI on next use.
- `DELETE /api/harnesses/codex/connection`: deactivates the adapter; the daemon keeps running and its threads stay in Codex.

Codex tasks: respond and send report unavailable (answer in Codex); every Codex session's links carry `command`, the `cd <dir> && codex resume <id>` line to copy. Adoption accepts a thread id or that command; a thread T3 started (originator `T3 Code`, which T3 wraps) is refused with "This Codex session was started by T3. Track it through T3."

Request `/api/connections/:connectionId/capabilities?projectId=ID` for workspace-specific options. A REST create with one binding can omit connectionId. Multiple bindings require an explicit connectionId. An unbound request without connectionId is 400 (no default harness). Task and schedule edits can change the destination with an explicit connectionId, executionConfig and workspace before launch. The scoped session context route supports explicit adoption previews. Task detail includes a bounded finalReply, the session's `usage` when its harness reports one, and links.web, links.native, links.service or links.command when available. `links.command` is a shell command that resumes the session in the harness's own client; hosts offer to copy it (Copy resume command) when there is no destination to open. Capabilities may list harness-wide `options` (any model) beside each model's `options`; an `executionConfig` may set the union, and option ids never collide (capabilities that reuse an id are rejected).

External deletion or connection failure retains Stage metadata, tasks and history.
An observed path change requires explicit confirmation. Future unclaimed launches
use the active binding; claimed attempts and uncertain retries retain their original
connection, external project, workspace and binding revision. Restoring connectivity
cannot redirect a retry.

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

Session lookup/adoption uses `/api/connections/:connectionId/sessions/:id`.
The connection must have a registered adapter for a first-time adoption. Adoption of an unbound session accepts
`projectId` or `newProjectTitle`, with binding and task creation in one transaction.
A failed adoption leaves neither a project nor a binding behind.

Tasks and schedules can be saved without a binding. Launch returns 409
`binding_required` or `connection_unavailable` before claiming an attempt.
A scheduled occurrence blocked this way becomes one untimed backlog task with an
actionable error. Further recurring occurrences are suppressed while it remains
pending. Repair alone never starts it; start it manually.

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
  "settledAt": null,
  "acceptanceId": null, "acceptanceRevision": 0, "completedAt": null, "completionProvenance": null,
  "runStartedAt": null,
  "runEndedAt": null,
  "lastWordPreview": null,
  "lastWordAt": null,
  "artifactLinks": [],
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
- `links`: where the session opens, from the harness adapter: `web`, `native` (an app scheme), `service`, `command` (a copyable resume command); each omitted when the harness has none. T3: `web` = `publicUrl/<environmentId>/<externalSessionId>` (the Browser URL), `native` = `t3code://threads/<environmentId>/<externalSessionId>`.
- `finishedAt`: first entry into `done|failed|interrupted|cancelled` (per launch attempt). `settledAt`: entry into `settled`.
- `runStartedAt` / `runEndedAt`: the task session's elapsed run span, from its observation; null without a session or before one is observed.
- `lastWordPreview` / `lastWordAt`: the task session's latest reply as one plain line (markdown stripped, at most 240 characters) and its time, from the observation; null without a session or before a reply is read.
- `artifactLinks`: the artifacts the harness reports for the session as `{label, url}` (every harness, deduped by URL), without live PR status; `[]` when none.

`TaskEvent`:
```json
{ "id": 3, "taskId": 7, "fromState": "launching", "toState": "running", "detail": "thread status running", "at": "2026-11-08T10:00:01Z" }
```
`fromState` null for the creation event.

`TaskDetail` = `Task` + `{ "events": TaskEvent[] (oldest first), "finalReply"?: {text, truncated, updatedAt, runId}, "tree": TreeDetail | null, "batch": CreatedTasks | null, "currentHandoffs": Handoff[], "usage"?: Usage, "reported"?: {branch?, directory?} }`. `reported`: the branch and directory the current session's harness reports (its observation), each omitted when unreported and the object omitted when it reports neither. `usage`: the current session's usage, omitted when its harness reports none; `Usage` = `{ "turns", "inputTokens" (uncached), "cacheReadTokens", "cacheWriteTokens", "outputTokens", "workingMs" (finished turns; 0 unknown), "costUsd": number | null }`, cumulative and best effort ([execution.md](execution.md)). `costUsd` is a cost the harness itself reports, else null; Stage never prices tokens, and Claude Code reports none. `stage task show` prints it as one line (`usage    1 turn · 44.3k in (27.6k cached) · 4.9k out · 23s working`, plus `$1.23` when a cost is reported). `tree`: the task's tree (see "Trees"); null outside one. `currentHandoffs`: the current launch attempt's handoff records, one per need, by need id (see "Handoff"); `[]` before the first launch, without needs, or with `handoff` off. `batch`: the tasks the task's origin session created (see "Session batch"); null when `originSession` is null. `finalReply`: the session's latest reply excerpt, omitted before one is read (API v8 removed `thread`).

### `GET /api/tasks`
Query (all optional): `originConnectionId` with `originSessionId` (the creator session; `originThreadId` is 400 since v6), `project` (project id), `state` (repeatable TaskState), `q` (case-insensitive substring of title or prompt), `from`, `to` (createdAt), `settledFrom` (`completedAt >= settledFrom`; legacy query name retained in v4), `closed` (`false` default: open tasks only, not archived and not in a done or archived tree; `true`: only those; `all`: both; else 400).
200 `{ "tasks": Task[] }` ordered by `updatedAt` desc.

### `POST /api/tasks`
Body `CreateTaskRequest`:
```jsonc
{
  "projectId": 1,                // required
  "title": "Say OK",             // required
  "prompt": "reply OK",          // required
  "connectionId": 1,             // the harness connection; omitted only when the project's bindings or the default connection choose one (else 400)
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

### `GET /api/tasks/:id`
200 `TaskDetail`. 404.

### `PATCH /api/tasks/:id`
Body `UpdateTaskRequest`: any of `projectId title prompt connectionId executionConfig` `attachmentIds` `startLocal` `startTz` `handoff` `startMode`. `executionConfig` replaces the stored config; a `connectionId` change needs one. The removed T3 model inputs are 400 (see create).
In `backlog` all fields are editable; otherwise only `title` (409 for others). `attachmentIds` is the exact image set: the task's own ids to keep plus staged uploads to add; the task's images not listed are deleted (400 for ids that are neither, more than 8).
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
(`launch`, `stop`, `adopt`, `reply`, `opening`, `attention`, `images`,
`respond`, `send`) to `{available, reason?}`; an unavailable one says why.
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

For an adapter with workspace-scoped catalogs, pass an optional Stage
`projectId` to read the models and agents for its verified binding on that
connection. Omitting it requests connection-wide capabilities.

### `POST /api/tasks/:id/stop`
Interrupt the task's scoped session through its adapter. State updates via the
poller. A task without an execution session returns 409.

Stop bumps `updatedAt` and re-arms polling of a `failed|interrupted` task for
10 minutes. It then polls the session once. An adopted session waiting in
Backlog can advance when execution begins in its harness. Execution observations
alter acceptance only through inferred completion.
200 `Task`. 502 / 503.

### Session interaction (API v6)

A harness may answer what its session waits on and take messages (optional
operations `respond` and `send` in its capabilities). Each route needs a task
with an execution session (409 otherwise); a harness without the operation,
or reporting it unavailable, answers 409 `{"code": "operation_unavailable"}`
with the reason, and nothing else is limited. Claude Code offers `respond`
(its approvals and questions, through the hook its launches carry) and
`send` (a follow-up to a finished background session, or one whose turn
ended asking you: [execution.md](execution.md#claude-code-adapter)); T3,
OpenCode and Codex offer neither. The app answers and sends on task detail
(answers also on needs-input rows); no MCP tool or CLI command answers or
sends.

- `GET /api/tasks/:id/requests` (operation `respond`): 200 `{"requests": [{id, kind: approval|question|input, title, detail?, preview?, options?: [{id, label, available, reason?}], textOption?, questions?: [{id, header?, prompt, options?: [{label, description?}], multi?, custom?}], createdAt}]}`. `preview`: the command, path, URL or input the request acts on (monospaced). `textOption`: the option whose answer may carry `text`. A `question` request lists its `questions`, answered together. Claude Code: approvals offer `allow`, `always:<i>` (one per Claude suggestion; the label names the rule and where Claude writes it) and `deny` (`textOption`; Claude reads "Denied in Stage: <reason>", or "Denied in Stage." without one); `AskUserQuestion` is a question request whose questions are all `custom`. A session with no open hook (answered in Claude, adopted, launched before Stage could answer) lists none: answer it with the resume command.
- `POST /api/tasks/:id/requests/:rid` body `{"option"?: string, "text"?: string, "answers"?: {questionId: string[]}}` (one required; `answers` for a question request: every question, option labels or one custom text, several only when `multi`): answers the request, then polls the session once. 200 `Task`; 400 (also an answer that does not fit the request); 403 `{"code": "agent_forbidden"}` when the request carries `Stage-Caller-Session` or comes through a worker's agent gateway (only the user answers; listing stays open); 404 the request is no longer pending (answered in the harness, superseded or expired).
- `POST /api/tasks/:id/messages` (operation `send`) body `{"text": string}` (`SendMessageRequest`: nonblank, at most 32 KiB): sends the session a message, records a "Follow-up sent from Stage" task event (not the text), then polls once. 200 `Task`; 400; 403 `{"code": "agent_forbidden"}` as for answers (only the user sends); 409 `{"code": "session_busy"}` with the reason when the session can't take a message now (Claude Code: working, waiting on a permission prompt or question, stopped, failed, removed, a terminal or SDK session, or its folder is gone; nothing was sent).

### `POST /api/tasks/:id/duplicate`
New `backlog` task with the same project, title, prompt, images (copied), execution config (launch options included); no copied notes, no tree, no needs, no start time, no schedule.
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
  "sourceThreadId": "thread_abc",
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

`kind` is `note|event`. `createdBy`: the note's author, or the user behind an event Stage writes directly (acceptance, duplicate); derived events (task state, tree, membership and needs; [storage.md](storage.md#journal-events)) carry the default user. For notes, exactly one of `taskId` and `treeId` denotes ownership. Task events can also carry a `treeId` snapshot; tree events have `treeId` and optional `sourceTaskId`. Provenance and IDs never change. Empty `category` means uncategorized; `eventType` is empty for notes. Nullable provenance fields are always emitted.

### `GET /api/journal/:id`

200 full `JournalEntry`, with `truncated: false`. 404 missing entry.

### `POST /api/tasks/:id/journal`, `POST /api/trees/:id/journal`

Body `{ "text": string, "requestId": string, "category"?: string, "sourceTaskId"?: number, "sourceSession"?: {connectionId, externalSessionId} }`. Text must be nonblank UTF-8, at most 16 KiB. Category is optional: empty (uncategorized), `finding`, `decision`, `blocker`, or `outcome`; other values are 400 for append and reads. `requestId` is required and scoped to the owner: retry identical content and provenance to return the existing entry; different content with the same key is 409. A tree note's `sourceTaskId` must currently belong to that tree; a committed retry still succeeds after it moves. 201 full entry for both append and retry; 400 invalid input, 404 missing owner/source, 409 stale provenance or conflicting retry.

Appending is allowed on closed work. It records context without reopening, scheduling, launching, or sending a session message. There is no edit/delete note endpoint. Notes and execution protect their task and associated tree from deletion, including after the task leaves.

## Trees

A tree is a DAG of tasks: each member's `needs` are members that must be completed by the user in Stage before it starts. It is the only way to express dependencies. A task is in at most one tree; trees may span projects. Rules in "Tree rules".

`Tree`:
```json
{ "id": 3, "title": "Ship auth", "startMode": "auto", "status": "open", "closedAt": null, "createdAt": "2026-11-08T09:59:00Z", "createdBy": 1, "sourceMachineId": null, "updatedAt": "2026-11-08T10:00:00Z", "deletable": false }
```
`status`: `open | done | archived` (see "Done, archive and delete"); `closedAt`: when it was marked done or archived (null while open); `deletable`: the tree and every member have no recorded work and no member started (`DELETE` removes the tree and its tasks).
`TreeDetail` = `Tree` + `TreeTimes` + `{ "tasks": Task[] (by id), "edges": [{ "taskId": 9, "needId": 5 }], "levels": [[5, 7], [9]], "events": TaskEvent[] }`. `levels`: member ids per level, roots first; a task's level is the longest need path to a root; each level by id. `events`: the members' 50 newest task events, newest first (`{ id, taskId, fromState, toState, detail, at }`), retained temporarily for compatibility. New tree Activity reads the journal.

`TreeTimes` = `{ "startedAt": string | null, "lastActivityAt": string }`. `startedAt`: the earliest member `launchedAt` (null before any launch). `lastActivityAt`: the latest tree metadata, journal entry, current member update or member thread update (so an agent working counts as activity).

`TreeSummary` = `Tree` + `TreeTimes` + `{ "counts": TreeCounts, "levels": number, "projects": string[] (member project titles, sorted), "settledAt": string | null }`. `counts`: `{ total, backlog, blocked, ready, launching, running, needsInput, done, settled, failed, interrupted, cancelled }` (`blocked` and `ready` split `backlog`). `settledAt`: the latest member acceptance once every member is accepted, else null (legacy field name).

### `GET /api/trees`
Query (all optional): `status` (`active` default: open trees with an unaccepted member; `done`: marked done, or open with every member accepted; `archived`; `all`; else 400), `all=1` (= `status=all`), `settledFrom` (with `active`: also open trees whose latest acceptance is at or after it), `project` (a member is in that project). 200 `{ "trees": TreeSummary[] }`, `lastActivityAt` desc.

### `POST /api/trees`
Body `{ "title": string, "startMode"?: StartMode, "tasks": [{ "taskId": number, "needs"?: number[] }] }`: creates the tree, moves each listed task into it (a member of another tree is spliced out there), then sets each listed `needs` (omitted keeps the task's needs). One transaction. 201 `TreeDetail`. 400 blank title, no tasks, a task listed twice, bad `startMode`. 404 task / need. 409 tree rule broken.

### `GET /api/trees/:id`
200 `TreeDetail`. 404.

### `PATCH /api/trees/:id`
Body: any of `title`, `startMode` (`auto|manual`), `status` (`open|done|archived`; applied first). 200 `TreeDetail`. 400. 404. Switching to `manual` makes released members ready (each notifies once); switching to `auto` launches them on the next reconcile. `status` `done` or `archived` 409 while a member is `launching|running|needs_input` (`#N is running: stop it first`); `startMode` 409 on a done or archived tree; `open` reopens or unarchives.

### `DELETE /api/trees/:id`
Deletes the tree and every member task only when `deletable`: no recorded work in the tree or its members and no member ever started. 204. 404. 409 otherwise; archive instead. Protection survives a task leaving the tree.

### `POST /api/trees/:id/dissolve`
Detaches the tasks, clears their needs, and archives the tree. Its identity and journal remain readable. 204. 404.

### `POST /api/trees/:id/tasks`
Body `{ "taskId": number, "needs"?: number[] }`: adds the task (a member of another tree moves: it is spliced out there and loses its needs), then sets `needs` when given. 200 `TreeDetail`. 404 tree / task / need. 409 tree rule broken.

### `DELETE /api/trees/:id/tasks/:taskId`
Splices the task out: its dependents take over its needs; the task stays, standalone. An emptied tree is archived. 204. 404 (also when the task is not in that tree).

### `POST /api/trees/:id/start`
"Start ready": starts every `ready` member (same as `POST /api/tasks/:id/start` each). 200 `{ "started": Task[], "errors": [{ "taskId": number, "error": string }] }`. 404. 409 on a done or archived tree.

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
  "executionConfig": { "model": "[\"claude\",\"claude-sonnet-5-5\"]", "options": [{ "id": "worktree", "value": true }, { "id": "baseRef", "value": "origin/main" }], "permission": "full-access", "mode": "" },
  "notes": "",
  "attachments": [],
  "cron": "0 2 * * *",
  "tz": "America/New_York",
  "paused": false,
  "nextRunAt": "2026-11-11T20:30:00Z",
  "lastRunAt": "2026-11-10T20:30:00Z",
  "lastTaskId": 41,
  "lastTaskState": "settled",
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
Body `CreateScheduleRequest`: shared task config (`projectId connectionId title prompt executionConfig attachmentIds originSession`; the removed T3 model inputs are 400 as on tasks), `notes` (initial-note template for each spawned task), required `cron` and `tz`, optional `paused` (default false). Example: `"cron": "0 2 * * *", "tz": "America/New_York"`. `initialNote`, `treeId`, `needs`, `needsSession`, `startMode`, `start`, `startLocal`, `startTz` are 400; spawned tasks are standalone.
201 `Schedule`. 400 (invalid cron or zone, plus task field rules). 404 project.

### `GET /api/schedules/:id`
200 `ScheduleDetail`. 404.

### `PATCH /api/schedules/:id`
Body: any of the create fields (`attachmentIds` is the exact image set, as for tasks). Changing `cron`, `tz` or `paused` recomputes `nextRunAt` from now (resuming never fires a missed time). Spawned tasks are not changed.
200 `Schedule`. 400. 404.

### `DELETE /api/schedules/:id`
204. Spawned tasks are kept (their `scheduleId` becomes null). 404.

### `POST /api/schedules/:id/run`
Run now: spawn a task and start it immediately, ignoring `paused` and the overlap rule; records a `manual` run; `nextRunAt` unchanged. Launch errors land on the task (`failed` + `lastError`).
201 `Task`. 404.

### `POST /api/schedules/preview`
Body `{ "cron": "0 2 * * 1-5", "tz": "America/New_York" }`. 200 `{ "next": [ISO, ISO, ISO] }` (next three fires, UTC). 400 with the parse error message.

### Schedule rules
- The scheduler ticks every 15s (and on start).
- One-shot: a `backlog` task with `startAt <= now` is launched (`startAt` cleared); the `launching` event detail reads `scheduled start` or `scheduled start, late by 12m` when more than a minute late (e.g. server was down: late starts are not skipped). Manual Start now also clears `startAt`.
- Recurring, per unpaused schedule with `nextRunAt <= now`, in one transaction (holds the database write lock):
  - `now - nextRunAt > 2m` (server was down): record one `missed` run (`detail`: `missed N fires`), advance `nextRunAt` to the first fire after now. Nothing starts.
  - A previous spawned task still `backlog` (pending launch), `launching`, `running` or `needs_input`: record `skipped` (`detail`: `#41 still running`), advance. `done` but unsettled does not block.
  - Else spawn a task (config + images copied, `scheduleId`, `startAt = fireAt`, `startTz = tz`), record `started`, advance. The one-shot pass then launches it (a crash in between still launches it on restart).
- Fires are computed in the schedule's `tz`; a fire is never run twice (`nextRunAt` advances in the same transaction).
- Scheduled starts and skips do not notify; a failed launch notifies as usual (`failed`).

## Images

`Attachment`: `{ "id": 4, "name": "error.png", "mimeType": "image/png", "sizeBytes": 48211 }`. Types: `image/png|jpeg|gif|webp` (sniffed from the bytes), max 10 MB each, max 8 per task or follow-up. Stored in the database. An upload is *staged* until a task create/update attaches it or a follow-up sends it; staged uploads older than 24h are deleted hourly. A launch delivers a task's images the harness's way ([execution.md](execution.md): T3 uploads, Codex input items, Claude Code files); images are accepted only where the connection's `images` operation is available (`GET /api/connections/:connectionId/capabilities`, scoped to the project): a task or schedule create with `attachmentIds`, an update that adds an image, and an update that moves images to another connection are 400 with the operation's reason elsewhere, and answer the harness error (502; 409 `connection_unavailable` without an adapter) when its capabilities can't be read. The MCP tools gate the same way.

### `POST /api/uploads`
Multipart form, field `file` (one image). 201 `Attachment` (staged). 400 not an accepted image type, empty, over 10 MB.

### `GET /api/attachments/:id`
200 raw image bytes (`Content-Type` = `mimeType`, immutable cache, `nosniff`). 404.

### `DELETE /api/attachments/:id`
Deletes a staged upload, or an image of a `backlog` task. 204. 404. 409 when the task has left backlog (images are read-only after launch).

## Explicit session lookup and adoption

`GET /api/connections/:connectionId/sessions/:id/context` (see Agent interface) is the session read; API v8 removed the T3 thread read `GET /api/connections/:connectionId/sessions/:id` (404).

`POST /api/connections/:connectionId/sessions/:id/adopt` accepts `{title?, initialNote?, treeId?, projectId?, newProjectTitle?}`. Returns Task: 201 new, 200 existing owner. Revalidates current eligibility; never creates a second task for a session. Archived and child sessions cannot be newly adopted. Stage refuses a session another registered harness wraps (its origin among that harness's `wraps`): 409 `session_refused` with where to track it (a Claude Code session T3 started: "This Claude Code session was started by T3. Track it through T3."). Session context lookups and resolving such a session from a pasted link or ID (`GET /api/refs/resolve`) answer the same 409. The UI previews before confirming. See Adopt session below.

### `GET /api/tasks/:id/opening-prompt`

Adopted tasks only (400 otherwise). Returns `{item: {itemId, text, truncated, updatedAt, ...} | null}`: the first own user message, skipping inherited history. Default 2000-character preview; `full=true` expands only that opening message, bounded to 1,000,000 characters. No arbitrary item lookup or paging. Missing task/session: 404; T3 errors use the ordinary gateway error contract. The task's cached final reply is independent of this live read.

## API v8 changes

Breaking, so `apiVersion` is 8 and v7 clients refuse this server. T3 is no longer the default harness (ADR 0007 decision 12): nothing outside the harness's own adapter and setup treats a connection as T3.

- Removed inputs, 400 naming the replacement (`<field> was removed in API v8; send executionConfig with connectionId`): `providerInstanceId`, `model`, `modelOptions`, `runtimeMode` and `interactionMode` on task and schedule create and update. Send `executionConfig` with `connectionId`.
- `connectionId` is required on task and schedule create unless the project's bindings or the default connection choose one (the "legacy unbound T3" fallback is gone: an unbound project without `connectionId` is 400). An omitted `executionConfig` runs on the harness defaults.
- Removed routes: `GET /api/catalog` (404; use `GET /api/connections/:connectionId/capabilities`), `GET /api/connections/t3/projects` (use the connection id), and the T3 thread read `GET /api/connections/:connectionId/sessions/:id` (404; use `…/context`).
- Removed fields: `thread` on task detail (use `session`, `links`, `finalReply`, `runStartedAt` / `runEndedAt` and `lastWordPreview`, now derived from the session's observation for every harness).
- References: a bare session id or `…/sessions/<id>` names no connection: 400 without `connectionId`. T3 web and `t3code://` links resolve only through the T3 connection's adapter.
- MCP: `list_models` requires `connectionId`; the removed T3 model arguments fail schema validation. CLI: `--provider`, `--model`, `--effort`, `--option`, `--runtime` and `--mode` are errors naming `--connection ID --execution-config JSON`; `models ls` needs `--connection`.
- Worktree as a harness launch option (#420): `workspace` on task and schedule create and update is 400 `workspace was removed in API v8; set the harness's launch options in executionConfig.options (worktree, baseRef, branch where the connection declares them; see its capabilities)`. Tasks and schedules drop `workspace`, task detail drops `currentLaunchBase`, the harness descriptor drops `worktree`, and capability operations `predecessorBase` and `worktreeBase` are gone. Options gain `kind`, `label` and `dependsOn` (see [capabilities](#get-apiconnectionsconnectionidcapabilities)). Predecessor bases are gone: a dependent starts where its own launch options say, and the handoff names the predecessor's branch. MCP: `workspaceType`, `baseRef`, `branch`, `base` and `baseTaskId` fail schema validation; CLI: `--workspace`, `--base-ref`, `--branch`, `--base` and `--base-task` are errors naming `--execution-config`. Storage: migration 0033 moves each stored workspace into the execution config's options (T3 `worktree`, `baseRef`, `branch`; Claude Code and OpenCode `worktree`; Codex none) (a T3 predecessor-based task keeps its stored fallback `baseRef` and `branch`), as well as unfinished launch attempts' intents, and journals on each predecessor-based task that can still start (not finished or archived) where its worktree now starts (T3: from its `baseRef`, or, with none stored, that one must be set before it starts, since T3 rejects a worktree without one; Claude Code and OpenCode: the harness default); migration 0034 drops `tasks.workspace`, `schedules.workspace` and `task_launch_bases`.
- Storage (migration 0032): the T3-only `threads` mirror is dropped (sessions live in `execution_observations`), and a fresh install has no connection: the seeded T3 connection 1 is removed when it was never configured and nothing references it. A never-ran T3 session (idle, no run) adopts as `backlog`; T3 idle after a run still reads `done` (the T3 adapter decides it). Project `lastActivity` no longer counts external sessions. The T3 status's `lastFullSyncAt` is recorded by the T3 setup after any successful full sync of its connection.

## API v7 changes

Breaking, so `apiVersion` is 7 and v6 clients refuse this server. New-task defaults move from preferences to each connection ([Preferences](#preferences)). Removed: `PUT /api/preferences/pinned` (404) and `pinned` / `lastUsed` on `GET /api/preferences`; creating a task no longer records a last-used model. Added: `defaultConnectionId` on preferences and `PUT /api/preferences/default-connection`; `newTaskConfig` on connections and `PUT /api/connections/:connectionId/new-task-config`. A create without `connectionId` starts on the default connection when the project is available there. Migration 0024 moves the old global pin to the main machine's T3 connection and sets no default.

Observation push (#420; additive, no version bump): `POST /api/connections/:connectionId/observations` lets an external bridge or hook push observations of an existing connection ([Harness connections](#harness-connections-api-v6)).

Session interaction answers (#375; additive, no version bump): requests gain `preview`, `textOption` and `questions`, responses `answers`; Claude Code offers `respond`; and the respond route answers 403 `agent_forbidden` to agent callers.

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
What a pasted reference names. `ref` accepts: a stage task, tree or session link (`…/tasks/<id>`, `…/trees/<id>`, `…/sessions/<id>`; any host, query and fragment ignored, a sub-path prefix allowed), the markdown link the web UI copies (`[stage task #12: Title](…/tasks/12)`, `[stage tree #4: Title](…/trees/4)`), a desktop deep link (`stage://tasks/<id>`, `stage://trees/<id>`, `stage://sessions/<id>`, also under `stage://localhost/`; any `stage-<suffix>://` developer scheme), a scoped session path (`…/connections/<connectionId>/sessions/<id>`), a harness's own session link (a T3 web or `t3code://` link, a Claude Code or Codex resume command), which the connection's adapter resolves (`execution.LinkResolver`; with `connectionId` that connection's, else each active connection's), `#12` or `12` (task), or any other single token (an external session id, with `connectionId` only). A session reference that names no connection (a bare id, `…/sessions/<id>`) without `connectionId` is 400. Stage paths win over harness links on the same host. 200:
```json
{ "kind": "task", "taskId": 12, "session": { "connectionId": 1, "externalSessionId": "th-1" }, "links": { "web": "…", "native": "…" }, "state": "running" }
```
`session`: a task's session (null before launch; API v6 removed `threadId`); a session's `taskId` is the task that owns it (null for sessions started outside stage). A tree link returns `{ "kind": "tree", "treeId": 4, "taskId": null, "session": null, "links": {} }` (a `connectionId` selection does not apply). A session stage has not observed yet is read from its harness once when connected. 400 not a reference; 404 unknown task, tree or session.

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

## Preferences

The default connection and the global notification filter. New-task defaults live with each connection: its model pin is `newTaskConfig` on [`GET /api/connections`](#harness-connections-api-v6) (`PUT /api/connections/:connectionId/new-task-config`). A new task starts on the default connection and prefills the model and options pinned there, else the harness default. Permission and mode are never pinned: they start at the harness default.

A task or schedule created without `connectionId` starts on the default connection when its project has an available binding there, else on the project's only active binding, else (with a default set) on its first available binding (the main machine's first, then by machine and connection id). With no default and several bindings it is 400. The web app's new-task form preselects the same connection.

### `GET /api/preferences`
200 `{ "defaultConnectionId": number | null, "notifyEvents": NotificationEvent[] }`. `defaultConnectionId` is null when unset or when its connection is no longer active (its machine removed); pairing another instance of its harness moves it to the replacement. `notifyEvents` (default all: `["needs_input","failed","ready","done","settled"]`, in that order) is the global filter of "Notification rules".

### `PUT /api/preferences/default-connection`
Body: `{ "connectionId": number | null }` (null clears it).
200 `Preferences`. 400 unknown or replaced connection.

### `PUT /api/preferences/notifications`
Body: `{ "events": NotificationEvent[] }` (`needs_input|failed|ready|done|settled`; duplicates dropped; `[]` = all off). Applies to every device.
200 `Preferences`. 400 unknown event or missing `events`.

## Notifications (Web Push)

The server sends Web Push (VAPID, RFC 8291 aes128gcm) to every subscribed browser. The VAPID key pair is generated on first start and stored in the database; the VAPID subject is `STAGE_PUBLIC_URL` when it is https, else `https://github.com/flexdinesh/stage`.

### `GET /api/push/config`
200 `{ "publicKey": string }` (VAPID public key, base64url, uncompressed P-256 point; the browser's `applicationServerKey`).

### `POST /api/push/subscriptions`
Body: browser `PushSubscription.toJSON()` subset `{ "endpoint": string, "keys": { "p256dh": string, "auth": string } }`. Upsert by `endpoint` (keys and user agent refreshed). 204. 400 missing field or non-https endpoint.

### `DELETE /api/push/subscriptions`
Body `{ "endpoint": string }`. 204 (also when unknown). 400 missing endpoint.

### `POST /api/push/test`
Body `{ "endpoint": string }`. Sends a test notification to that subscription synchronously. 204. 404 unknown endpoint. 502 push service rejected it (`error` = status and body); a 404/410 from the push service also deletes the subscription.

### `GET /api/badge`
200 `{ "needsInput": number }`: tasks in `needs_input` + external threads needing input (see below).

### Push payload
JSON, shown by the service worker. Built from the stored inbox row by one server function, shared with `GET /api/notifications/stream`:
```json
{ "title": "Needs input", "body": "Say OK · stage", "url": "/tasks/7", "tag": "task-7", "badge": 2 }
```
Needs-input payloads open their task or the inbox. Open in T3 continues the conversation.
`tag` replaces an older notification for the same task (`task-<id>`) or thread (`thread-<connectionId>:<externalSessionId>`); the service worker sets `renotify` so a replacement alerts again. iOS ignores tag replacement (WebKit bug 258922), so the service worker also closes shown notifications with the same tag (`tag`, or the copy in `data.tag`) before showing the new one. `badge` = `GET /api/badge` at send time. TTL 24h; urgency `high` for needs input, `normal` otherwise. Header `Topic` = first 32 chars of base64url(sha256(tag)): the push service keeps only the latest undelivered push per task/thread.

Clear on view: opening a task page closes its `task-<id>` system notification and marks group `task:<id>` read; opening session detail closes its `thread-<id>` notification and the related task notification when present, and marks those groups read (`POST /api/notifications/read` with `groups`). Clicking a notification opens one of these pages. Opening the notifications inbox closes all of them and marks everything read.

Cross-device clear (lazy): every device closes shown `task-*` / `thread-*` notifications whose tag is not in `GET /api/notifications/unread-tags`, i.e. viewed on any device. The service worker does this after showing each push; the app does it on load, when it becomes visible, and on every push; the desktop app does it on every `unread` event of `GET /api/notifications/stream`. No dismiss pushes are sent: iOS revokes subscriptions for pushes that show no notification, and Chromium/Firefox only tolerate a small budget of them. WebKit ignores `close()` on notifications younger than ~30s, so those clear at a later sync. Other tags (e.g. `test`) are never closed by sync.

Service worker: when a window of the app is focused and visible on a Chromium browser, it skips the system notification (the app shows an in-app toast instead); other browsers (Safari, Firefox) always show it, since a push without a notification there can revoke the subscription. Either way it updates the app badge and posts `{ "type": "push" }` to every open window so the app refetches at once.

## Notifications inbox

Every notification the notifier sends is stored first (also when no browser is subscribed), so the app can show in-app toasts and an inbox. Entries older than 30 days are pruned hourly.

`Notification`:
```json
{
  "id": 12,
  "event": "needs_input",
  "taskId": 7,
  "externalSessionId": "thread-abc",
  "title": "Say OK",
  "projectTitle": "stage",
  "url": "/tasks/7",
  "createdAt": "2026-11-08T10:00:00Z",
  "readAt": null,
  "currentState": "needs_input",
  "connectionId": 1,
  "links": { "native": "t3code://threads/env-1/thread-abc" }
}
```
`event`: `needs_input|ready|done|failed|settled`. `taskId` null for external threads (or once the task is deleted). `title` / `projectTitle` are snapshots at send time. `currentState` is live: the task's state, or for an external thread `settled` when settled, else its `observedState`; null when unknown.

Task notifications retain `/tasks/<id>` for group identity and navigation. External notifications open `/?inbox`, with native T3 navigation in the inbox. Historical stored URLs are preserved; emitted destinations derive from task/session identity.

### `GET /api/notifications`
Query: `limit` (default 50, max 200), `before` (id; only older entries), `group` (optional group key from the grouped inbox; restricts history to that task/thread). 200 `{ "notifications": Notification[], "unread": number, "nextBefore": number | null }` newest first; `nextBefore` = the `before` for the next (older) page, null when none. `unread` remains the global unread event count. 400 bad `limit` / `before`.

### `GET /api/notifications/groups`
Query: `limit` (default 50, max 200), `cursor` (opaque `nextCursor` from the previous page). 200 `{ "groups": NotificationGroup[], "unread": number, "latestId": number, "nextCursor": string | null }`. 400 bad `limit` / `cursor`.

`NotificationGroup` extends `Notification` with `key` (stable task/thread identity), `eventCount` (retained notifications for this group), and `unreadCount`. Its inherited notification fields describe the latest event; `currentState` stays live. Group by task across restarts, or by external thread, never by title. Deleted tasks retain their group via their task URL. Identity-less entries remain separate.

Groups whose current state is `needs_input` come first, then other groups; each section is newest-event-ID first. Aggregation precedes pagination, so repeated events cannot consume a page or cause older copies of a group to reappear. `latestId` is the global newest event ID captured before listing, independent of priority order; use it for read-on-open, then the newest observed `latestId` for arrivals while the drawer is open. Events inserted after that boundary remain unread. Unread markers captured on open stay visible until close. The bell still counts unread events.

Expandable history uses `GET /api/notifications?group=...`; it contains only recorded `needs_input`, `ready`, `done`, `failed`, and `settled` notifications, not every lifecycle transition. Unknown current state displays as unavailable rather than falling back to an old event.

### `POST /api/notifications/read`
Body `{ "upTo": number }`: marks every unread entry with `id <= upTo` read. Or `{ "groups": string[] }` (1 to 20 group keys, e.g. `task:12`, `thread:<connectionId>:<externalSessionId>`): marks every unread entry in those groups read. 204. 400 when neither or both are sent, `upTo` < 1, or `groups` is empty, too long or has an empty key.

### `GET /api/notifications/unread-tags`
200 `{ "tags": string[] }`: push tags of groups with unread entries (`task:<id>` → `task-<id>`, `thread:<id>` → `thread-<id>`; identity-less groups have none). Devices close shown notifications whose tag is missing (see "Clear on view").

### `GET /api/notifications/stream`
Server-sent events: inbox rows after a cursor, then live rows and unread snapshots (the desktop app's native notifications and badge). Headers `Content-Type: text/event-stream`, `Cache-Control: no-cache, no-transform`, `X-Accel-Buffering: no`; first line `retry: 3000`. 400 when the cursor is not an integer >= 0.

Cursor = a notification id: `Last-Event-ID` (reconnect) wins over `?after`. No cursor → cursor = the newest id (no replay). A cursor above the newest id (e.g. a reset database) → cursor = the newest id and `reset: true`.

Events, in order:
- `hello` (`id: <cursor>`): `{ "latestId": number, "reset": boolean }`. `latestId` = newest id at connect (0 when empty).
- `gap` (no id): `{}`, sent when more than 100 rows follow the cursor; only the newest 100 are replayed.
- `notification` (`id: <id>`), per row id > cursor, ascending: `{ "id": 12, "event": "needs_input", "createdAt": "2026-11-08T10:00:00Z", "read": false, "tag": "task-7", "title": "Needs input", "body": "Say OK · stage", "url": "/tasks/7" }`. `tag`, `title`, `body` and `url` are the Push payload of that row; `tag` is `""` for identity-less rows. Replay includes rows already read (`read: true`).
- `unread` (no id): `{ "tags": string[], "badge": number }`: the `GET /api/notifications/unread-tags` tags and `GET /api/badge` `needsInput`. Sent once after the replay, then only when it changes.

Live: every new inbox row, read (`POST /api/notifications/read`), prune and task transition wakes the stream; it sends rows after the last one sent (no cap), then `unread` if changed. `: ping` every 15s, which also recomputes `unread` (external threads' needs-input changes reach the badge within 15s). The stream ends when the server shuts down; reconnect with `Last-Event-ID`.

## Notification rules

- Filter: an event not in `preferences.notifyEvents` (read when the event is sent) is dropped entirely: no inbox row (so no toast or bell entry) and no push, on any device. If the filter cannot be read, the event notifies.

- Tasks: a transition into `needs_input` ("Needs input"), `done` ("Done"), `failed` ("Failed") or `settled` ("Settled") notifies. Stored `url` = `/tasks/<id>` for stable group identity; push destination = the task URL. Other states (incl. `interrupted`) do not.
- Ready: a manual tree member with at least one need whose needs all have user acceptance (see "Tree rules") notifies once per readiness episode ("Ready", `url` = `/tasks/<id>`, tag `task-<id>`). Roots and auto members never send it.
- External sessions: adapters expose an attention feed. Stage also rereads stored active/done sessions to observe their outcomes. Changes into `needs_input|done|failed` notify; the inbox destination is retained. The first pass after startup is silent. Later newly discovered sessions notify only if created after startup. Missing sessions and failed reads preserve the last observation; settling or archiving an unowned session completes nothing and releases no dependencies. A task's own session follows "Inferred completion".
- Needs-input counts combine tasks with unowned, non-child normalized session observations. Historical notification entries and links remain available.

## State mapping

The adapter maps harness state to Stage observations. Active execution and pending
input take precedence over historical settlement. Completed execution maps to done;
unknown or missing replies preserve the last known state. Connection health is
separate. Polling and recovery change acceptance only through inferred completion. See [execution.md](execution.md).

## User acceptance (API v4)

Execution `state` and `settledAt` remain observations. `acceptanceId` (nullable), `acceptanceRevision`, `completedAt` (nullable) and `completionProvenance` (the current acceptance's provenance, nullable) describe Stage acceptance. Task detail adds `acceptances`, newest first. Legacy tree `counts.settled` counts currently accepted members, and tree `settledAt` is their latest acceptance when all members are accepted. Unaccepted execution `done|settled` contributes to `counts.done`.

- `GET /api/tasks/:id/completion-preview`: `revision`, `evidenceVersion`, `evidence`, `blockedReason`, `dependents`. Reads Stage's local cache only. Dependents identify claimed work and effective start mode.
- `POST /api/tasks/:id/complete`: `{requestId, revision, evidenceVersion, note?}`. Rejects active `launching|running|needs_input`, mirrored active runs/pending requests, and uncertain launch outcomes. Any known stopped state or unstarted task may be accepted, including interrupted/cancelled/failed work. The optional note is UTF-8, at most 16 KiB.
- `POST /api/tasks/:id/reopen`: `{requestId, revision}`. Clears current acceptance; retains immutable history, execution and existing launch claims. Does not start a run. A successful execution continues in T3; ordinary failed/interrupted/cancelled retries remain available.
- Both writes return `{revision, acceptanceId}`. Exact request replay returns its original result without applying it again, even after a later reopen. Reusing a request ID with changed content, stale revision or changed evidence yields 409; refresh and review before a new submission.
- Acceptance records contain `id, taskId, acceptedAt, capturedAt, provenance, evidence, note`. Evidence captures task/title/project/tree, execution/thread/branch/worktree references, bounded cached reply with run/item IDs, timestamps, truncation, available artifact links, and the latest journal entry ID at capture. Cache observation time, earlier/unverified run and missing reply are explicit. No live read is required to review, accept or use handoff.
- Confirmation and launch claims serialize in SQLite. Reopen before claim blocks it; claim before reopen continues with pinned inputs. Completing again cannot launch an existing attempt twice. Manual trees become ready; automatic trees retain automatic release. Closed trees stay closed.
- `provenance`: `user` (complete action), `historical_settlement` (migration), `harness_settled` (T3 settle) or `harness_archived` (OpenCode archive, Claude Code removal).
- Inferred completion: when a poll observes a task's session closed in its harness (T3 settled, OpenCode archived, Claude Code session removed with `claude rm` after its turn ended; observation `closed.signal`, also on an idle observation, which leaves the task's state as it was), Stage completes the task in one transaction only if its state is `done`, it has no current acceptance, the session is not active, the observation has an execution ID, the task's connection has `autoComplete`, the completion preview has no `blockedReason`, and no earlier decision exists for that execution. It records the preview's evidence with an empty note, `acceptedAt` = now (never T3's settle time), the same task/tree/journal/readiness updates as user completion (journal `task_completed`: the harness closure's event: `Completed from T3 settle; accepted evidence saved in Stage.`, `Completed from OpenCode archive; accepted evidence saved in Stage.` or `Completed from Claude Code removal; accepted evidence saved in Stage.`), then triggers the release runner. Failed, interrupted or cancelled execution never infers. Reopen records a decision for the session's current execution, so a still-closed session does not complete it again; a new execution can.
- Polling: unaccepted `done|settled` tasks stay in the throttled stale poll (at most once a minute) so a later closure is seen; accepted ones are polled only for an hour after their last update.
- Migration accepts only tasks currently `settled`, with `historical_settlement` provenance and cache-only evidence. A previous settlement timestamp on running work does not qualify. Existing IDs, notes, histories, execution links, policies and overrides survive.
- Completion/reopening are user-facing REST actions; inferred completion is the only server-side path. There is no CLI/MCP completion or reopen tool. Agents report outcomes using notes; ordinary task updates cannot set acceptance.

## Tree rules

- Needs (create `needs`, `PUT /api/tasks/:id/needs`, tree `needs`), 409 unless noted: change only in `backlog` (an unchanged set is a no-op in any state); not with a start time (400); each need exists (404), is not the task, is in the task's tree, and does not (transitively) need the task (no loops). An accepted need is allowed.
- Joining a tree without needs (create with `treeId`, add to tree, adopt) makes a root, parallel to the other roots; `needs` chain the task after those members, and tasks that need the same member run in parallel. Nothing defaults needs: chaining is always explicit.
- Create with `needs` and no `treeId` joins the needs' tree; needs outside any tree are pulled in; with no tree among them a new `auto` tree is created, titled after the first need. Needs in different trees (or another tree than `treeId`) 409. `needsSession` adopts that session first (see "Adopt session") and adds it as the first need. One transaction.
- Tasks inherit the tree's start mode by default (`startMode` omitted or null; CLI/MCP also accept `inherit`). Set shared policy on the tree, and explicit task `auto` or `manual` only for an exception. In an auto tree a task created or joined after its needs already have user acceptance launches at once; agent guidance treats that as expected and does not steer to `manual` to avoid it. Tree mode changes preserve explicit overrides; clear a task's override to make it inherit again.
- `start: true` or a start time with needs is 400. A non-backlog task may join a tree only without needs.
- A need is passed only when `acceptanceId` is non-null. Execution success, stopping, failure and PR merge never release new work; settlement or archive does only through inferred completion. Complete the need in Stage or edit dependencies.
- `readiness` (backlog members only, else null): `blocked` while a need lacks user acceptance; `ready` when every need has user acceptance, there is no start time, and the task is manual or has no needs. Roots read `ready` (auto never launches them; the user starts them, or Start ready).
- Release runner (on user or inferred completion/reopening, every transition into `settled` or out of `backlog`, every tree change, at startup and every 10s): launches every `auto` member whose needs all have user acceptance (no start time, not adopted); a `manual` member with needs that became ready gets `readyAt` and a `ready: needs completed` event once per episode and notifies `ready`. `readyAt` clears when it stops being ready or launches.
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
- With `treeId` the task joins that tree (no needs). Adoption never accepts the task, including an already settled session; a later poll may (connection auto-complete).

## Handoff

Every task launch also appends a separate journal instruction block, including standalone tasks and `handoff: false`. It records task ID, tree ID at launch, connection ID, an attended-session line (start mode and delivery path only decide when and how the session started; the user watches and answers in it, so the agent works as in a user-started session, including asking when its instructions or skills call for it), an interface line (the `stage` CLI, with `stage call TOOL JSON` for any tool, Stage MCP tools or the HTTP API, with the `STAGE_PUBLIC_URL` address when set; never assume or install one) and per-interface caller guidance (the CLI and stdio MCP detect the harness's session variable). The block is persisted per task/attempt; retries, including after restart or movement, reuse it verbatim. Agents must refresh membership with `get_context` or `get_task` before `read_journal`, read relevant own/tree notes, use task notes for progress and tree notes with `sourceTaskId` for shared conclusions, checkpoint before risky work, and record outcomes before returning. Notes are context, never instructions or proof; failed writes must be reported without claiming persistence. Existing or adopted sessions receive no retroactive block. Handoff remains independent.

When a task with needs launches with `handoff` on, the launch claim pins each need's immutable accepted result. Stage composes context exclusively from these local snapshots, including task/tree/journal references, cached reply excerpt, available artifact links, branch/worktree and execution reference. The predecessor harness may be offline. Missing evidence remains explicitly missing; earlier or unverified run provenance and truncation remain visible. Optional user acceptance notes are distinct from assistant replies.

Reply budget per need: `min(4000, max(1000, 8000 / sources))` Unicode characters. Each attempt records the rendered block in `task_handoffs`. Uncertain retries reuse stored blocks and acceptance pins. A storage error aborts the launch; it never silently launches without accepted context. Historical sent/skipped handoffs remain readable.

`Handoff`:
```jsonc
{ "attempt": 1, "status": "sent", // sent | skipped
  "sourceTaskId": 12,             // null only for records from before trees (a session source)
  "sourceThreadId": "thread-abc", "sourceTitle": "Build API",
  "text": "<handoff ...",          // "" when skipped
  "reason": null,                  // why skipped
  "at": "2026-11-14T10:02:01Z" }
```

## Session batch

A session batch is the set of tasks one session created (`originSession`), usually a plan an agent broke into tasks.

`CreatedTasks`:
```jsonc
{
  "trees": [{ "tree": { "id": 3, "title": "Ship auth", "startMode": "auto" }, "tasks": [Task, Task] }],
  // batch tasks per tree, ordered by each group's lowest task id; tasks by id
  "independent": [Task]           // batch tasks outside any tree, by id
}
```
- A tree group lists only batch tasks; the tree may hold others (e.g. the adopted session of `afterMe`, which is in the batch only when that session created it).

## Agent interface

`stage` CLI, stdio MCP and HTTP MCP expose tasks, trees, journals, schedules, explicit adoption and reference resolution through REST. Coding agents with a shell are pointed at the CLI first (the `stage setup` skill); MCP is optional, for clients without a shell or users who prefer tools. `stage tools [NAME]` lists the MCP tools (or one tool's description and input schema) and `stage call TOOL [JSON|-]` runs one through the same MCP server in process, printing its structured result as JSON (exit 1 on a tool error, 2 on an unknown tool or non-object arguments). `get_task` returns the final reply and grouped `batch`; `list_tasks {originSession}` (CLI `task ls --origin-connection ID --origin-session ID`) lists the creator session's tasks. `read_ref` returns task details and/or explicit session metadata with available native links, or for a tree link the tree (`get_tree` shape) and a `next` naming the tree tools. Conversation reads and actions belong to the source harness. Removed tools and commands are listed under API v3 retirement.

- A caller-aware tool takes `callerSession: {connectionId, externalSessionId}`. Besides it, three generic channels name the caller, each in the form `<connectionId>:<externalSessionId>`: the `Stage-Caller-Session` header on an HTTP MCP request (malformed: 400), the `STAGE_CALLER_SESSION` environment variable for the CLI (one process, one session; a stdio MCP server ignores it, being shareable across sessions), and harness request metadata: a harness descriptor's `attribution.metaKeys` (OpenCode: `ai.opencode/sessionID`) in an MCP request's `_meta`, resolved to that harness's active configured connection on the caller's machine (`GET /api/connections/resolve`). The weakest source is the harness's session variable (`attribution.env`: T3 `T3CODE_THREAD_ID`, OpenCode `OPENCODE_SESSION_ID`), which the CLI and `stage mcp` read from their own environment (they run as the agent's child processes; HTTP MCP never does) and resolve the same way, cached per process. Several harness variables set resolve by each harness's `attribution.precedence`: a variable whose `owns` conditions match the environment wins (several owning: none wins); otherwise variables whose `yields` conditions match drop out and the one left wins. Several left, or none of the winner's connections on the machine, name no caller, and `get_context` says why in `callerWarning`; all yielding names none without a warning. Order: a per-call claim (metadata or header), then an explicit caller (which must agree with a claim; disagreeing is an error), then `STAGE_CALLER_SESSION` (CLI only), then the harness variable. `get_context` reports the winner in `sessionSource` (`request`, `argument`, `STAGE_CALLER_SESSION` or the variable's name). Unknown context leaves the origin empty. CLI also takes `--caller-connection ID --caller-session ID`. The launch instructions tell an agent how its harness names its session (the descriptor's caller hint).
- Agents on a remote machine use the same interfaces unchanged through the worker's agent gateway ([runtime.md](runtime.md#agent-gateway)): it owns that machine's default socket, so `stage` and `stage mcp` find it with no `--url`, and an opt-in TCP listener serves HTTP MCP. Caller attribution works as above; the main server also records the forwarding machine (`sourceMachineId`). The launch instructions of a session on a remote machine name the gateway, never `STAGE_PUBLIC_URL`: its TCP listener when the worker reported one (`the HTTP API at http://127.0.0.1:7381/api (MCP at …/mcp) on this machine's Stage agent gateway`), else its socket with a `curl --unix-socket` hint, else the gateway without an address. Sessions on the main server's machine keep `STAGE_PUBLIC_URL`.
- `GET /api/connections/resolve?harness=KIND` returns the active configured connection of KIND (the `GET /api/connections` row) on the request's machine: the forwarding machine of a request through a worker's agent gateway, else the server's. 400 unknown kind; 404 `{error, code: "no_active_connection"}` when the machine has none.
- `GET /api/connections/:connectionId/sessions/:id/context` returns session, owning task, current project, title and available links. The owner path reads Stage storage. An unowned session requires explicit lookup and a verified workspace binding to infer its project.
- Task and schedule creates accept an optional destination `connectionId` and distinct `originSession`. Agent creation uses the sole project binding. Multiple bindings require a choice. Unbound projects use the caller's connection or sole configured connection. Starting requires a binding.
- `list_projects` includes configured connections. `list_models {connectionId}` (required) returns capabilities. `executionConfig` uses the connection's model, option values, permission and mode.
- `read_ref {session}` and `stage show --target-connection ID --target-session ID` accept an opaque scoped ID. A scoped reference path is `/connections/:connectionId/sessions/:id`. `list_tasks {originSession}` uses REST query fields `originConnectionId` and `originSessionId`. A bare session id names no connection: pass the connection with it.
- Journals: `read_journal {taskId?|treeId?, kind?, category?, q?, filterTaskId?, cursor?, limit?}` requires exactly one owner; `filterTaskId` is tree-only. `get_journal_entry {id}` retrieves full text. `append_note {taskId?|treeId?, text, requestId, category?, sourceTaskId?, callerSession?}` records caller session provenance. `get_context` includes `callerTaskId`, current `treeId` and `treeTitle`; refresh it or `get_task` before reading or contributing to a tree.
- CLI: `stage task notes ID [list] [--kind notes|events|all --category C --q Q --limit N --cursor C]`; `stage tree notes ID` adds `--task ID` filtering. Append with `stage task notes ID add --text TEXT [--category C --request-id KEY]`, or `stage tree notes ID add --text TEXT --source-task ID`; `--text -` reads stdin. CLI generates a request key when omitted; supply `--request-id` to retry across invocations. `stage task notes ID get ENTRY_ID` and `stage tree notes ID get ENTRY_ID` retrieve full entries. Task creation/adoption use `--initial-note` / `initialNote`; task edit has no notes field. Schedule `--notes` remains a template.
- Project: explicit `project` (id or unambiguous exact title) > the caller session's explicitly bound project. Cwd is context only; paths across machines are not verified identity.
- Destination without `connectionId`: the default connection when the project is available there, else its only binding, else (with a default set) its first available binding; then the rules above.
- Model: explicit `executionConfig` > the destination connection's pin (`newTaskConfig`) > harness default. The pin supplies model and options only; permission and mode are explicit or unset. The removed T3 model arguments (`provider`, `model`, `effort`, `options`, `runtimeMode`, `interactionMode`) fail schema validation.
- `get_task` returns the grouped creator batch, current tree, cached `finalReply`, `usage` (when the harness reports one), available links and accepted snapshots.
- `afterMe` submits the scoped caller as `needsSession`. Owner reuse, adoption, tree placement and the dependent task commit together.
- Trees: `create_task` takes `treeId`, `needs` and `startMode` (`auto|manual|inherit`) (CLI `task add --tree ID --needs 3,5 --start-mode M --after-me`); `update_task` takes `startMode` (`inherit` = null; CLI `task edit --start-mode`). `set_needs {taskId, needs}` (CLI `task needs ID [NEED...]`, none clears). `create_tree {title, startMode?, tasks: [{id, needs?}]}` (CLI `tree new --title T [--start-mode M] ID[:NEED,NEED]...`), `get_tree {id}` (CLI `tree show ID`: levels with state, `#id`, title, needs, readiness), `list_trees {status?, all?, project?}` (compact rows `{id, title, startMode, tasks, levels, projects, states, settledAt, status}`, `states` = non-zero counts with `blocked`/`ready` split from `backlog`; CLI `tree ls [--status S] [--all] [--project P]`), `update_tree {id, title?, startMode?, status?}` (CLI `tree edit`; `status` `done|archived|open`: CLI `tree done ID`, `tree archive ID`, `--undo` reopens), `delete_tree {id}` (only when deletable; CLI `tree rm-tree ID`), `add_to_tree {treeId, taskId, needs?}` (CLI `tree add TREE TASK [--needs ..]`), `remove_from_tree {taskId}` (looks up the task's tree; CLI `tree rm TASK`), `start_tree {id}` (CLI `tree start ID`). Dissolving a tree is REST/web only.
- Close work: `archive_task {id, archived?}` (default archives; CLI `task archive ID [--undo]`), `list_tasks {closed?}` (`true|all`; CLI `task ls --closed true|all`; rows carry `treeStatus` and `archived`), `delete_task` only for a task that never started and has no recorded work. See "Done, archive and delete".
- `adopt_session {session?, callerSession?, projectId?, newProjectTitle?, title?, initialNote?, treeId?}` (default: the caller session) returns `{ task, created }` (CLI `sessions adopt [--target-connection ID --target-session ID] [--title --initial-note --tree]`).
- `start_task` on a blocked task fails until the user completes its needs in Stage.
- `create_task` and `update_task` take optional `handoff` (CLI `task add --handoff=false`, `task edit --handoff=true|false`); omitted keeps the task's value (default true). `get_task` returns `handoff` and `currentHandoffs`; `stage task show` prints `handoff  on|off` for tasks with needs and the current attempt's records, plus `tree` and `readiness` lines.
- Launch options (a worktree, its base ref and branch where the harness declares them) go in `executionConfig.options` (CLI `--execution-config`; `list_models` and `models ls --connection ID` list them with `kind`, `label` and `dependsOn`). The pin's options fill in when `executionConfig` names no model. `stage task show` prints `options  worktree=true baseRef=origin/main`.
- `imagePaths` reads local files and uploads them (`POST /api/uploads`); CLI and stdio only (HTTP MCP rejects them).
- `wait_task` polls `GET /api/tasks/:id` every 3s until the state is in `until` (default `needs_input done settled failed interrupted cancelled`) or `timeoutSeconds` (default 600, max 3600); CLI `task wait` exits 3 on timeout.

`GET /api/notifications/groups` also returns `waitingSessions: [{connectionId, externalSessionId, title, projectTitle, directory, links}]`. These are current external needs-input sessions with no recorded notification up to `latestId`. `directory` is the session's working folder when its harness reports one (null for T3); `links` may hold only a `command` (Claude Code) or nothing (a session answered where it runs). A harness whose attention policy is `silent` (Claude Code) never records events for sessions no task owns, so its waiting sessions appear only here and in the needs-input count. They have no notification ID, timestamp or read state. Resolving input, adopting the session, or receiving a real event removes the silent row. Startup suppression and delivery preferences remain unchanged.
