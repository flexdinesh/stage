# Server runtime contract

How one Stage server process listens, owns its data, reports readiness and
version, and which requests it accepts. Desktop, the CLI, stdio and HTTP MCP,
the PWA and OS services all rely on this. Code: `server/internal/{serve,listen,instance,filelock,guard,buildinfo,config}`.

## One process, many listeners

`stage serve` serves one handler set, store and background runtime
(sync, links, schedules, notifications, the T3 connection) on every
configured listener. The listeners are not separate instances.

`STAGE_LISTEN` takes a comma list; `--listen` can be repeated (or take a
comma list), and when given it replaces `STAGE_LISTEN` entirely. Each item is:

| Item | Meaning |
| --- | --- |
| `host:port` | TCP. An empty host means all interfaces. |
| `unix:/abs/path.sock` | An explicit Unix socket, mode `0660`. `unix:///abs/path.sock` also works. |
| `unix` | The private default socket for this data dir (below), mode `0600`. |

With nothing configured the listener is `127.0.0.1:7380`, as before. Single
values keep their old meaning. Empty items and duplicates are errors.

| Profile | Listeners |
| --- | --- |
| Foreground / compose (unchanged) | `127.0.0.1:7380` (compose: `0.0.0.0:7380` inside the container) |
| Per-user service ([service.md](service.md)) | `stage serve --managed`: listeners from `service.env` `STAGE_LISTEN`, default `unix`, which is required (TCP is opt-in: `unix,127.0.0.1:7380`) |

## Machine profile and hosted harnesses

The way the server runs decides which harnesses it may run
([execution.md](execution.md#harness-registry)). Connected harnesses (a
service Stage connects to: T3, OpenCode) run on every profile. Hosted
harnesses run as processes on this machine and need a profile that allows
local processes.

| Profile | When | Local processes |
| --- | --- | --- |
| `desktop` | `stage serve --managed` (the per-user service the desktop app ensures) | yes |
| `server` | foreground `stage serve` | only with `--local-harnesses` or `STAGE_LOCAL_HARNESSES=1` (desktop dev, tests) |
| `container` | `STAGE_MACHINE_PROFILE=container`, set by the Docker image | never, whatever else is set |

`GET /api/harnesses` reports the profile and, per harness, whether it is
available here with the reason when not. The profile is not stored: it is
computed at each start. Every connection belongs to a machine
(`harness_connections.machine_id`); one local machine (`machines` id 1)
exists until workers do. While its label is unset (migration 0022 unsets the
old `This computer` seed), each start names it from where the server runs:
the host of `STAGE_PUBLIC_URL` in the `container` profile (a container's
hostname is a random id), the hostname otherwise, else `Stage server`. A
rename is never replaced.

## Identity and paths

The installation identity (`buildinfo.Identity`, default `stage`) is fixed at
build time:

```sh
go build -ldflags "-X github.com/flexdinesh/stage/server/internal/buildinfo.Identity=stage-dev" ./cmd/stage
```

A developer installation built this way can never use the stable paths.
Release builds use `stage`, developer builds (`mise run dist`) `stage-dev`
([release.md](release.md)). The identity derives:

| What | Path |
| --- | --- |
| Default data dir | `$XDG_DATA_HOME/<id>`, else `~/.local/share/<id>` (macOS: `~/Library/Application Support/<id>`). `STAGE_DATA_DIR` overrides it. |
| Shared socket dir | Linux: `$XDG_RUNTIME_DIR/<id>/`. macOS: `~/Library/Application Support/<id>/run/`. |
| Default socket | `<shared>/stage.sock` for the identity's default data dir; `<shared>/stage-<8 hex of sha256(data dir)>.sock` for any other data dir. On Linux without `XDG_RUNTIME_DIR`: `<data dir>/run/stage.sock`. |
| T3 client name | `<id>`, plus ` dev` with `STAGE_ENV=dev` |
| Service unit / label | `<id>.service`; launchd label `buildinfo.ServiceLabel`, default `io.github.flexdinesh.<id>` |
| Service config, definition, logs | see [service.md](service.md#paths-per-identity) |

The data dir is canonical: symlinks are resolved after the directory is
created, so one directory has one identity however it is spelled.

The data dir is required on both storage engines: it holds `stage.lock`,
the `<data dir>/run` sockets and logs, and on SQLite `stage.db` and
`backups/`. `STAGE_DATABASE_URL` selects the engine: unset is SQLite at
`<data dir>/stage.db`; `postgres://` or `postgresql://` is Postgres, only in
a binary built with the `postgres` tag (the Docker image; example:
`examples/postgres/`); any other value,
or a Postgres URL in a binary without the tag, exits 78 before the data dir
is touched. The per-user service, and so the desktop app, is SQLite only
([service.md](service.md#serviceenv), [storage.md](storage.md#engines-and-selection)).
No log line or error prints the URL.

Socket paths over the OS limit (107 bytes on Linux, 103 on macOS) are
rejected with an error at parse time instead of failing at bind. The fix is a
shorter explicit `unix:/path`.

## Permissions

- Data dir: created `0700`.
- Default socket: its directory is created `0700`. It must be a real
  directory (not a symlink), owned by this user, with no group or other bits;
  otherwise the server refuses to start. The socket is `0600` and its lock
  `0600`.
- Explicit sockets: `0660`, as before (a proxy in the socket's group can reach
  it). Their directory is the operator's responsibility.

## Ownership and locks

Ownership is proven by `flock(2)` locks that the process holds for its whole
lifetime. The kernel releases them when the process dies, so neither a PID
file nor a socket probe is ever the proof.

| Lock | Guards | Holder record |
| --- | --- | --- |
| `<data dir>/stage.lock` | One server per data dir, on any listen addresses: no duplicate scheduler or sync on one `stage.db`. | JSON: pid, instanceId, identity, version, commit, startedAt, listener URLs, and for `--managed` `managed`, `manager` (`systemd`/`launchd`) and `service` (unit or label). Informational (error messages, `stage service status`). |
| `<socket>.lock` | One server per socket path. A live lock also tells clients a server owns the socket. | none |

Startup order:

1. **Lock the data dir.** If it is held, exit at once (status 75) with `data dir … is in use by another stage server (pid N, version V, listening on …)`, plus a hint to run `stage service stop` when the holder is the managed service. The server does not wait. It also starts nothing and binds nothing.
2. **Open every listener in order.** For a socket, take its lock. Under the lock, an existing socket file that refuses connections is stale (its owner died) and is replaced. A regular file is refused. So is a socket that answers (a server predating the lock) or one that cannot be probed. If any bind fails, the listeners already opened are closed, their sockets removed and their locks released. The database has not been touched yet.
3. Record the holder in `stage.lock`.
4. Open the database and guard its schema ([below](#schema-and-backups)). The startup log names the engine (`database engine=sqlite path=…` or `database engine=postgres`), never the URL. On Postgres, connecting and taking the database's ownership lock ([storage.md](storage.md#single-owner)) are bounded at 30 s (unreachable: exit 1, for the restart policy to retry; held by another server: exit 75). A schema newer than this binary exits 78; pending migrations on a non-empty database are preceded by a backup (a failed backup exits 1, unmigrated). Log one warning naming any removed harness environment variable still set (`T3_URL`, `T3_PUBLIC_URL`, `T3_ADMIN_TOKEN`, `OPENCODE_URL`, `OPENCODE_PUBLIC_URL`, `OPENCODE_WEB_URL`, `OPENCODE_USERNAME`, `OPENCODE_PASSWORD`: no longer read; set the connection up in Settings > Connections). Migrate, start T3 (its URL: the one saved with the T3 `url` setting (`PUT /api/harnesses/t3/settings` or `stage harness t3 set url=URL`), else `http://127.0.0.1:3773`; an unpaired T3 is never contacted in the background), recover launching tasks (`RecoverLaunching`, so a restart relaunches nothing), and start the background loops.
5. Serve all listeners, then send `READY=1` to systemd.

`filelock.Acquire` retries for up to 500 ms. A client briefly probing a
socket lock (below) therefore cannot fail a server that is starting. A real
second owner is still refused within that time.

Locks need a local filesystem; `flock` is unreliable on some network
filesystems. Lock files are never deleted, because deleting them would let
two owners lock different inodes.

## Cleanup ownership

| Artifact | Created by | Removed by |
| --- | --- | --- |
| Socket file | the server | the server on shutdown. After a crash, the next server removes it under the socket lock. |
| `*.lock` files | the server | never (they are empty or informational) |
| Default socket dir, data dir | the server | never |
| `<data dir>/backups/stage-schema*.db` | the server, before a migration | the server, keeping the newest 3 |

## Shutdown

On SIGINT or SIGTERM:

1. Send `STOPPING=1` to systemd.
2. Background loops and transcript (SSE) streams end, so open streams do not delay shutdown.
3. The HTTP server stops accepting connections and drains in-flight requests for up to 100 s. In-flight launches are bounded at 90 s. Service stop timeouts must exceed 100 s: compose `stop_grace_period: 2m`, systemd `TimeoutStopSec=110s`, launchd `ExitTimeOut 110`.
4. T3 and the database close, sockets are removed and their locks released.
5. The data-dir lock is released last.

An explicit desktop Quit does not stop the server.

## Exit status

| Status | Meaning |
| --- | --- |
| 0 | clean shutdown (SIGINT/SIGTERM) |
| 1 | any other failure (bind error, database, shutdown timeout, database ownership lost while running) |
| 2 | bad command line (for example `--managed` with `--listen`) |
| 75 | the data dir is held by another server, or (Postgres) the database's ownership lock is ([storage.md](storage.md#single-owner)) |
| 78 | invalid configuration (`STAGE_*`/`T3_*` values, `service.env`, socket path; a `STAGE_DATABASE_URL` that is not `postgres://`/`postgresql://`, or a Postgres URL in a binary built without the `postgres` tag), or a database schema newer than this binary |

systemd does not restart on 75 or 78 (`RestartPreventExitStatus`). launchd
cannot exclude exit codes, so a `--managed` server on macOS logs the reason
and exits 0 for those two.

## Schema and backups

Migrations are goose versions per engine (`server/internal/db/sqlite/migrations`,
`server/internal/db/postgres/migrations`, same version numbers, tracked in
`goose_db_version`). `stage version --json` `schema` is the highest this
binary knows and `engines` the engines built in (`["sqlite"]`, or
`["sqlite","postgres"]` with the `postgres` tag); `/api/health` `schema` is
the database's and `engine` the running engine. The backups below are
SQLite's; Postgres backups are the operator's ([storage.md](storage.md#backups)).

- Database newer than the binary (a downgrade): refused, exit 78,
  `database schema N is newer than this Stage (knows M): install a newer
  Stage or restore a backup (docs/install.md#recovery)`. Nothing is written.
- Older and non-empty: `VACUUM INTO
  <data dir>/backups/stage-schema<N>-<UTC yyyymmddThhmmssZ>.db` (dir `0700`,
  file `0600`, logged), then migrate. Only the newest 3 backups are kept. If
  the backup fails (for example a full disk), the server exits 1 with the
  database untouched and no partial backup.
- Fresh or up to date: no backup.

## Readiness and discovery

- **systemd:** with `NOTIFY_SOCKET` set (`Type=notify`), the server sends `READY=1` once every listener serves and `STOPPING=1` when shutdown begins. Without it, nothing is sent (foreground, launchd).
- **Polling:** connections that arrive during startup wait in the listen backlog until serving starts. `stage status --wait DUR` polls `/api/health` until it answers 200. `stage service start --wait DUR` waits for the service the same way and also checks that the data-dir holder is the service's own process.
- **Client resolution** (CLI, `stage mcp`, and `stage status`):
  1. `--url`
  2. `STAGE_URL`
  3. The default socket of the data dir from `STAGE_DATA_DIR` (or the identity default), used only when its `.lock` is held. A stale socket file left by a crash is skipped, never dialed.
  4. `http://127.0.0.1:7380`

  If both a socket service and a TCP server are running, the socket wins.
  Resolution is read-only: no agent command starts a server.
- **`stage service`** ignores `--url`/`STAGE_URL`: it targets this
  installation's service and its socket only ([service.md](service.md)).
- **`stage setup mcp`** (and `stage setup --mcp`) passes `STAGE_URL` to the stdio server only when one was given explicitly; otherwise the stdio server resolves the server itself. `--http` needs an http(s) URL; if the server is only on a socket, pass `--url`. `stage setup --check` reports which server the CLI resolves and whether it answers with this API version.

## Health and version

`GET /api/health` (contract in [api.md](api.md#health)):

- It answers 200 with build and instance metadata whenever the server and its
  database work.
- A harness being offline is still 200 (`harnesses[].connected: false`).
  "Stage is live and ready" is therefore separate from "a harness is
  reachable".
- It never calls a harness, so it answers at once while T3 hangs (desktop
  polls it every 5s). `harnesses` reads each connector's memory-only summary
  (T3: a valid token T3 has not rejected). T3's reachability (a 10s-cached probe of
  `{T3 URL}/.well-known/t3/environment`), the setup state and auto-pair are
  `GET /api/harnesses/t3/status` ([api.md](api.md#t3-details)), which refreshes a
  stale probe for at most 3s.
- 503 means the database query failed.
- `engine` is `sqlite` or `postgres`; `stage status` shows it next to the
  database state.
- It does not expose paths, the pid or the database URL.

Every `/api` response carries `Stage-Api-Version: <n>` (`buildinfo.APIVersion`,
currently 2). The number changes only on breaking API changes.

API v2 replaces mutable task notes with append-only task/tree journals and
`initialNote` on task creation/adoption. Old task notes are discarded; existing
task events stay. Historical tree recording begins at rollout. Schedule notes
remain templates for each spawned task. Upgrade clients and server together.

- The Go client (CLI, stdio MCP) refuses any response whose version differs:
  `stage server at … speaks API vN, this client vM`. A response without the
  header (a server older than this contract) is accepted.
- A different build `version` is not an error. `stage status` reports it, and
  desktop shows a non-blocking banner for it (ADR 0001).
- `stage status [--wait DUR] [--json]` exits 0 when ready (T3 offline
  included), 1 when unreachable, unhealthy or on another API version, and 3
  when `--wait` expires.

Version stamping: `mise run build` sets `Version` (from `git describe`) and
`Commit`. Other builds report `version: "dev"`, with the commit taken from
Go's embedded VCS info when it is available. The Docker image reports `dev`.

## Request origin policy

`/api/` and `/mcp` pass through `guard.Policy`. Static UI routes do not. This
is request-origin hygiene and **not authentication**: any process that can
reach a listener directly has full access, and CORS is not a security
boundary. For remote use, the deployment boundary remains an authenticating
HTTPS reverse proxy, with Stage itself unreachable except through it.

Checks, in order:

1. **Host (anti DNS rebinding).** Allowed:
   - IP literals (a rebound name never produces one);
   - `localhost`;
   - hostnames from `STAGE_ALLOWED_ORIGINS`;
   - the hostname of `STAGE_PUBLIC_URL`;
   - on Unix listeners only, `stage` (the Go client) and `stage.invalid` (desktop bridge B). Single-label `stage` can be spoofed on a LAN, so it is refused over TCP.

   Any other host gets `403 host "…" not allowed: add its origin to STAGE_ALLOWED_ORIGINS …`, and the server logs it once per host. The port is ignored.
2. **Fetch metadata.** A request with `Sec-Fetch-Site: cross-site` or `same-site` gets 403 unless its `Origin` is trusted. This applies to GET too, so other sites cannot embed `/api` images or streams.
3. **Unsafe methods.** POST, PUT, PATCH and DELETE go through Go's `http.CrossOriginProtection`. The trusted origins are the allowed origins, plus `mygo://localhost` (the desktop webview) on Unix listeners only. Failure: 403.
4. **Body type.** A non-empty body on an unsafe method must be `application/json` or `multipart/form-data`, else 415. This closes the `text/plain` and form-encoded CSRF vectors.

Trusted origins are `STAGE_ALLOWED_ORIGINS` (a comma list of
`http(s)://host[:port]`; invalid entries stop startup) plus the origin of
`STAGE_PUBLIC_URL`. `X-Forwarded-*` headers are never trusted. A proxy that
rewrites Host to loopback still works, because the browser's Origin is
trusted.

| Client | Why it passes |
| --- | --- |
| CLI / stdio MCP over a socket | Host `stage` on a Unix listener; no Origin |
| CLI over TCP, HTTP MCP clients | loopback IP Host; no Origin. `/mcp` also keeps the MCP SDK's own localhost check, so HTTP MCP through a proxy still gets 403. |
| Browser / PWA, same origin | `Sec-Fetch-Site: same-origin` |
| Vite dev proxy | Host `localhost:5173`; same origin |
| Desktop webview (bridge B), this machine | Host `stage.invalid` on the socket; the bridge strips `Origin` and `Sec-Fetch-*` after checking them itself (a cross-site request or another page origin never reaches the server) |
| Desktop webview (bridge B), remote server | the saved URL's Host over TCP, no `Origin` or `Sec-Fetch-*` (stripped as above): passes when that host is an IP literal, `localhost`, in `STAGE_ALLOWED_ORIGINS` or `STAGE_PUBLIC_URL`'s; otherwise 403, which the desktop explains ([desktop.md](desktop.md#servers)) |
| Tailscale serve / Caddy | its origin is `STAGE_PUBLIC_URL` or listed in `STAGE_ALLOWED_ORIGINS` |

**Upgrade note:** a proxied hostname that is neither `STAGE_PUBLIC_URL`'s host
nor listed in `STAGE_ALLOWED_ORIGINS` now gets 403. Add it before upgrading.

## Worker routes

`/api/worker/*` is the one place Stage authenticates requests itself
([ADR 0002](adr/0002-machines-and-workers.md#5-security-model),
[api.md](api.md#worker-routes-apiworker)). A remote machine's `stage worker`
calls it: join with a one-time pairing code, then every other route with its
machine token. Everything else stays unauthenticated behind the deployment
boundary.

Order for a worker request:

1. **`guard.Policy`**, unchanged. A worker dials `STAGE_PUBLIC_URL`, so its
   Host is that URL's host (always allowed), and as a non-browser client it
   sends no `Origin` or `Sec-Fetch-*` and a JSON body. A DNS-rebinding Host or
   a cross-site browser request is still refused with 403 before any
   credential is read.
2. **API version.** `Stage-Api-Version` must equal the server's: 409
   `api_version_mismatch` otherwise.
3. **Credential.** Join takes only the code in its body (an `Authorization`
   header is 400). Other worker routes need `Authorization: Bearer
   stage_mt_...`; the machine id it names is the request's principal (a
   machine, never a user). 401 otherwise.

Machine tokens are refused everywhere else: a Bearer value starting with
`stage_mt_` on any other `/api` route or on `/mcp` is 401, after the origin
policy. Agents on a worker's machine reach `/api` and `/mcp` only through
the worker route `/api/worker/gateway/...` ([Agent gateway](#agent-gateway)),
so the proxy bypass stays the one prefix. Other `Authorization` values are ignored, so a proxy that forwards its
own credentials keeps working. Pairing codes and tokens are stored only as
SHA-256 (high-entropy secrets need no slow hash) and compared in constant
time; the id prefix selects the row.

**Behind an authenticating reverse proxy:** the proxy keeps user auth on the
UI, `/api` and `/mcp`, and passes `/api/worker/*` through without it,
forwarding the `Authorization` header unchanged. Creating a pairing code
(`POST /api/machines/pairings`) stays behind user auth. Pairing requires
`STAGE_PUBLIC_URL` (409 `public_url_required`), the address workers reach.
The full proxy rules: [Remote main server and workers](#remote-main-server-and-workers).
Without a proxy, any process that reaches a listener already has full API
access, and the token adds nothing to or takes nothing from that.

## Worker channel

A worker dials out; the main server never dials a worker. One channel per
machine: an SSE stream down (`GET /api/worker/channel`) and one POST per
message up (`POST /api/worker/channel`), both worker routes
([api.md](api.md#get-apiworkerchannel), [ADR 0002](adr/0002-machines-and-workers.md)).
Code: `server/internal/workerchan` (protocol, hub, stream and POST
serving), `server/internal/api` (routes and auth), `server/internal/worker`
(client), `server/internal/remote` (the remote machine's calls,
[execution.md](execution.md#remote-machines)).

- **Session.** Each stream gets a session id in its `hello` event; up POSTs
  name it in `Stage-Channel-Session`. A newer stream of the same machine
  replaces the older (the old stream ends, its POSTs get 409
  `channel_replaced`).
- **Liveness.** The server writes a `: keepalive` comment every 15s and the
  worker POSTs a heartbeat every 15s. Either side treats 45s of silence as a
  dead channel: the server closes the session, the worker reconnects.
- **RPC.** The hub sends a call as an `rpc` event with an absolute deadline
  (the caller's context deadline, else 45s) and waits for the `result` by id.
  When the caller's context ends first the hub sends `cancel` (best effort)
  and returns. Outcomes: a body, the worker's typed error, offline (no live
  channel when called) or lost reply (deadline passed or the channel dropped
  in flight: unknown outcome, never failure). Results after that are dropped.
  Calls: `ping` (`POST /api/machines/{id}/ping`, `stage machine ping ID`)
  and the remote machine's adapter, ops and describe calls.
- **Worker-initiated calls.** An up POST may carry a call; its result is the
  POST's response. A callback names the waiting request (`call`) and runs
  only while that request waits (`checkpoint.save`, `launch.image`,
  `events.observation`); after that it gets `call_ended`. A callback still
  running when its request stops waiting is cancelled, and the request
  returns only after it ended. `machine.changed`
  names no request. An up message is at most 32 MiB (launch images travel
  down one per `launch.image` reply).
- **Attach.** Each new session is described (`machine.describe`) and the
  coordinator polls the machine's connections at once.
- **Presence.** Online means a live channel, kept in memory, so a main
  restart shows every worker offline until it reconnects (seconds).
  `machines.last_seen_at` and `version` are written on connect, on
  disconnect and at most once a minute while connected (never moved back).
  A worker refused with `api_version_mismatch` while presenting a valid token
  shows `status: needs_update` until it connects again (memory only).
- **Shutdown.** Server shutdown closes every stream; workers reconnect to the
  next instance with backoff.

## Worker process

`stage worker` makes this machine a worker of a main server
(`server/internal/worker`, `server/internal/cli/worker.go`).

| Command | Does |
| --- | --- |
| `stage worker join <link> [--label L]` | Redeems the pairing link's code (`POST /api/worker/join`) and writes the worker config. Refuses when already joined (`stage worker leave` first). Plain `http://` links are accepted (LAN and loopback mains). |
| `stage worker run [--listen ADDR]` | The foreground worker: holds the worker dir, serves the [agent gateway](#agent-gateway), connects the channel and reconnects until stopped. Over the channel it serves this machine's adapters, fs/process work and connection setup (`remote.Host`, `workersetup`; harness credentials in `credentials.json` beside the config, `0600`). Harness options (`T3_FAKE`, `T3_FAKE_SEED`, `T3_BIN`) come from its environment: the managed worker reads them only from `worker.env`. Connections are set up in Settings > Connections; the T3 admin token, like the T3 token, stays in `credentials.json`. |
| `stage worker status` | Config, the server's view of this machine and the running process. Exit 0 the server says online, 1 not online (or unreachable, or an invalid config), 4 not joined, 7 revoked. |
| `stage worker leave [--local]` | Removes this machine on the main server (`POST /api/worker/leave`), then deletes the local config and state. A revoked token counts as already removed. `--local` only deletes locally. |
| `stage worker service ...` | The worker service ([service.md](service.md#worker-service)). |

All take `--config PATH` and `--json`. Worker commands never use
`--url`/`STAGE_URL`: the main server's URL comes from the config.

- **Worker dir:** `<data dir>/worker` (the identity's data dir, runtime
  defaults above; `STAGE_DATA_DIR` moves it), mode `0700`. Config path:
  `--config`, else `STAGE_WORKER_CONFIG`, else `<worker dir>/worker.json`;
  the dir is the config's dir.
- **`worker.json`** (`0600`; group or other bits are refused):
  `{schema, url, machineId, label, token, joinedAt}`. The token never leaves
  this file and the `Authorization` header.
- **`worker.lock`:** flock held for the process lifetime plus a holder record
  (`pid, version, managed, startedAt`). A second worker on the dir exits 75
  and names the holder (the worker service: `stage worker service stop`).
- **`worker.state.json`:** the channel state the process last reached
  (`connecting`, `online`, `needs_update`, `revoked`, `stopped`), its last
  error and when, and its gateway listener URLs (`gateway`; the service's
  readiness and status read it). `stage worker
  status` shows it while a process holds the lock, and asks the main server
  (`GET /api/worker/machine`) whether this machine is online or revoked.
- **Reconnect:** exponential backoff 1s to 60s with jitter, reset after a
  stream that stayed up 30s. `api_version_mismatch` waits the maximum
  interval. 401 (the machine was removed) ends the run as revoked, without
  a retry loop.
- **Exit status:** 0 on SIGINT/SIGTERM; 75 the worker dir or a gateway
  socket is held; 78 not joined, invalid config (including the gateway
  listen list) or revoked; 1 otherwise. Managed on macOS, 75 and
  78 exit 0 (launchd cannot exclude exit codes). Managed, it sends
  `READY=1` once it holds the dir and `STOPPING=1` on shutdown; readiness
  does not wait for the channel.
- One identity runs the main server or a worker service, not both (the
  desktop switches roles on join and leave, [desktop.md](desktop.md#worker)).

## Agent gateway

A worker serves agents on its machine the main server's `/api` and `/mcp`
([ADR 0002](adr/0002-machines-and-workers.md#8-mcp-and-the-agent-gateway)),
so `stage`, `stage mcp` and HTTP MCP clients there work unchanged. Code:
`server/internal/worker/gateway.go` (worker), `server/internal/api/handlers_worker_gateway.go`
(main).

- **Listeners.** `stage worker run --listen ADDR` (repeatable or a comma
  list), else `STAGE_WORKER_LISTEN`; the worker service reads only
  `worker.env`'s `STAGE_WORKER_LISTEN` ([service.md](service.md#worker-service))
  and refuses `--listen`. Same syntax as `STAGE_LISTEN`. Default `unix`, and
  `unix` is required: the private default socket of this identity's data dir
  (`STAGE_DATA_DIR` or the default, [Identity and paths](#identity-and-paths)),
  `0600` in a `0700` dir, locked like a server's socket. Client resolution
  finds it because its lock is held. TCP is opt-in
  (`unix,127.0.0.1:7381`) for HTTP MCP clients; a wildcard host is reachable
  from the network with full agent access, so prefer loopback. A bad list
  exits 78.
- **Startup.** The listeners open after the worker dir is locked and before
  readiness and the channel, and stay open while the channel reconnects or
  the main server is down (requests then get 502). A socket held by another
  process (the main server or another worker of this identity) exits 75
  naming it; one identity runs the main server or a worker. They
  close on shutdown and when the worker stops as revoked.
- **Origin policy.** Each request first passes `guard.Policy` as on a
  server listener, with no extra trusted origins: Host `stage` on the socket,
  an IP literal or `localhost` over TCP; cross-site browser requests and
  form-encodable bodies are refused. Only `/api/...` and `/mcp` are
  forwarded; other paths are 404.
- **Forwarding.** To `<worker config url>/api/worker/gateway<path>`, query
  and body unchanged, with `Authorization: Bearer <machine token>`,
  `Stage-Api-Version` and `Stage-Version`, the main server's Host, and
  without `Origin`, `Referer`, `Cookie`, `Sec-Fetch-*` or the caller's own
  `Authorization`. `Stage-Caller-Session` passes. Responses stream through
  unbuffered (SSE, streamable MCP). Main unreachable: 502 `{error, code:
  "gateway_unreachable"}`. The main server's answers (including 401
  `machine_token_invalid` after removal and 409 `api_version_mismatch`)
  pass through.
- **Main server.** The worker route `/api/worker/gateway/{api/...|mcp}`
  checks the API version and the token, then serves the inner path with the
  same `/api` router or MCP handler as a direct request, with the machine
  on the request context (`db.WithMachine`): writes record it as
  `source_machine_id` ([architecture.md](architecture.md#users)). The outer
  request already passed `guard.Policy`; the MCP SDK's loopback Host check
  is skipped for it (a proxy on the same host forwards the public Host).
  Machine management routes and per-machine harness setup changes
  (`PUT`/`POST`/`DELETE /api/machines/{id}/harnesses/...`) are 403
  `gateway_forbidden`; `/api/worker/*`
  and other paths 404 ([api.md](api.md#apiworkergatewayapi-and-apiworkergatewaymcp)).
- **Reported addresses.** The worker sends its listener URLs in the channel
  connect's `Stage-Worker-Gateway` header; the main server keeps them in
  memory for that session and names them in the launch instructions of
  sessions on that machine instead of `STAGE_PUBLIC_URL`
  ([api.md](api.md#agent-interface)). `stage worker status` shows them.
- **Trust.** A machine token grants agent-level API access through the
  gateway. Without a proxy this matches direct access; behind one it is the
  only way past user auth, which is why machine management is refused.
  Removing the machine revokes it at once.

## Remote main server and workers

The distributed deployment: one main server on a host or cluster behind an
authenticating HTTPS proxy, and workers on the machines that run harnesses
([ADR 0002](adr/0002-machines-and-workers.md)). Examples:
`examples/kubernetes/` (SQLite), `examples/postgres/` (compose, Postgres).
User guide: website `/docs/usage/workers` (Multiple machines).

**Main server.**

- **Engine.** SQLite in the data dir by default; Postgres when the Docker
  image gets `STAGE_DATABASE_URL` ([Identity and paths](#identity-and-paths),
  [storage.md](storage.md#engines-and-selection)).
- **One replica.** One server owns one data dir ([Ownership and
  locks](#ownership-and-locks)); a second server on the same volume exits 75.
  On Postgres one server also owns the database: a second server on it exits
  75 whatever its data dir ([storage.md](storage.md#single-owner)).
  Under an orchestrator: one replica, stop the old instance before starting
  the new one (Kubernetes `strategy: Recreate`), no autoscaling.
- **Local volume (SQLite).** `stage.db`, its backups and `stage.lock` live on
  a local (block-backed) filesystem. `flock` and SQLite locking are unreliable
  on NFS and other network filesystems, so they are not supported. On
  Postgres the data dir holds only `stage.lock` and sockets and needs no
  volume; backups are the operator's ([storage.md](storage.md#backups)).
- **Shutdown.** Allow more than the 100s drain ([Shutdown](#shutdown)) before
  a forced kill (Kubernetes `terminationGracePeriodSeconds: 120`, compose
  `stop_grace_period: 2m`).
- **Probes.** `GET /api/health` ([Health and version](#health-and-version)):
  200 while the server and its database work, whatever harnesses or workers
  do. A probe that sends the pod IP as Host passes the origin policy. Pending
  migrations (with a backup first) run before serving, so a startup probe
  needs room for them.
- **`STAGE_PUBLIC_URL`** is the proxy's HTTPS URL. It is required for pairing
  (409 `public_url_required`): pairing links are built from it and workers
  dial it.
- **Image.** The Docker image sets `STAGE_MACHINE_PROFILE=container`: the main
  server's own machine never runs local-process harnesses
  ([Machine profile](#machine-profile-and-hosted-harnesses)). It reports
  build version `dev`; the worker and the main server need the same API
  version, not the same build.

**Proxy.** In front of every listener a network can reach:

- User authentication on the UI, `/api` and `/mcp`, including
  `POST /api/machines/pairings`.
- `/api/worker/*` passes through without user authentication, with
  `Authorization` forwarded unchanged ([Worker routes](#worker-routes)). It
  is the only bypass: the channel, join, leave and the agent gateway route.
- Keep the request's Host, or rewrite it to an IP literal or a host in
  `STAGE_ALLOWED_ORIGINS`. Workers send no `Origin`, so a rewrite to any other
  hostname is 403.
- No response buffering on `GET /api/worker/channel` and
  `/api/notifications/stream` (both send `X-Accel-Buffering: no`), and an
  idle timeout above 15s: the channel's keepalive and heartbeat run every
  15s and 45s of silence ends it ([Worker channel](#worker-channel)).
- Request bodies of at least 32 MiB on `POST /api/worker/channel` (the
  largest up message).

**Workers.** A remote machine joins with `stage worker join <link>` and runs
the worker service ([service.md](service.md#worker-service)); its agents use
the [agent gateway](#agent-gateway). One identity runs the main server or a
worker, not both.

## OpenCode connection

Settings connect OpenCode three ways: the service on the Stage server's machine (Settings or `stage harness opencode run connect-local`), a remote service through a link from `opencode pair` on its host (`stage harness opencode run pair link=LINK`), or an address with optional HTTP Basic credentials. `stage harness opencode disconnect` (or Disconnect in Settings) forgets the address and credentials; tasks and history stay.

- Local: Stage reads the service's registration file, `$XDG_STATE_HOME/opencode/service.json`, else `~/.local/state/opencode/service.json` (macOS too), in the Stage server's environment. It uses the file's `url` and authenticates as `opencode` with its `password`, which is never stored. Each health check re-reads the file and rebuilds the connection when the password or address changed; a new address passes the same continuity check as an edited one.
- Paired: the link's code is redeemed once for a 30-day session token, stored like a password. Stage renews it after 7 days through the service (`POST /api/pair`); a failed renewal keeps the old token, logs a warning and retries after an hour. A server password change on the OpenCode host revokes the token (state `rejected`): pair again.

No environment variable sets the connection: it lives in Settings only. Browser links omit credentials. Stage readiness and project planning are independent of either harness.
