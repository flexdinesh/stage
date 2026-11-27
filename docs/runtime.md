# Server runtime contract

How one Stage server process listens, owns its data, reports readiness and
version, and which requests it accepts. Desktop, the CLI, stdio and HTTP MCP,
the PWA and OS services all rely on this. Code: `server/internal/{serve,listen,instance,filelock,guard,buildinfo,config}`.

## One process, many listeners

`stage serve` serves one handler set, store and background runtime
(sync, links, schedules, notifications, the T3 connection) on every
configured listener. The listeners are not separate instances.

Personal is the default, including desktop and trusted home LANs with
workers. Anyone who can reach a personal product listener has implicit
operator access. Current bind defaults still apply. The [shared access
ADR](adr/0009-shared-access.md) defines opt-in named access for one trusted
workspace. Members can run code through configured harnesses. The
installation owner's OS account protects secrets and recovery.

Migration 0040 introduces a durable installation ID distinct from the
process `instanceId` and the build identity (`stage` or `stage-dev`). Stage
resolves the database's mode after migration and before starting the
runtime. Shared state is sticky across restarts, including when the opt-in is
omitted. An explicit false setting requires restarting with shared
configuration. During account-service delivery, internal shared startup
opens only the owner control socket. Product listeners and work runners stay
closed until shared transport enforcement is available. The public opt-in
ships with the integrated feature.

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

### Shared setup and owner recovery

Set `STAGE_PUBLIC_URL` to the origin people use in their browser. Use an
HTTPS proxy for access from other machines. The proxy can use a protected
Stage product socket or a private TCP upstream. For local HTTP testing, set
`STAGE_PUBLIC_URL=http://127.0.0.1:7380` and include `127.0.0.1:7380` in
`STAGE_LISTEN`. A personal server on a private socket needs no browser URL.

On the first shared start, Stage issues a first-admin setup link valid for
30 minutes and prints it once to the operator's terminal. Managed, container,
and redirected foreground runs write it to `<data dir>/owner/bootstrap.txt`
with mode `0600`. Normal logs identify the file, and service status and Copy
Diagnostics never include its contents. Stage removes the file on shutdown,
restart or setup reissue. On a pending restart, Stage prints the reissue
command and keeps any valid link until the owner replaces it.

Run `stage owner reissue-setup` on the server as the installation owner to
replace the setup link. After claim, use
`stage owner recover-admin --email EMAIL` to issue a 30-minute reset link for
an active admin. The command prints the link for private delivery. Set
`STAGE_DATA_DIR` to the server's data dir if it differs from the default.
Commands connect to `<data dir>/owner/control.sock`, a separate `0600` Unix
socket in an owner-only directory. Shared startup requires the data directory
to be owned by the Stage process user with mode `0700`. Keep the owner paths
private when configuring volumes or proxies. Product Unix and TCP listeners
do not carry owner control.

On the Docker image, run the commands through `docker exec CONTAINER /stage
owner …` as the container's Stage user. Read the first setup link privately
on the Docker host with `docker cp CONTAINER:/data/owner/bootstrap.txt - |
tar -xO`, using the configured data directory if it differs from `/data`.

## Machine profile and hosted harnesses

The way the server runs decides which harnesses it may run
([execution.md](execution.md#modules-and-the-registry)). Connected harnesses (a
service Stage connects to: T3, OpenCode) run on every profile. Hosted
harnesses run as processes on this machine and need a profile that allows
local processes. Claude Code is one ([execution.md](execution.md#claude-code-adapter)): its
background sessions belong to Claude's per-user daemon, which a launch from
the managed service starts in its own systemd user scope (Linux), so
restarting Stage leaves them running. Sessions Stage launches post
[Claude Code hooks](#claude-code-hooks) to a private loopback listener.
Codex is another
([runtime](#codex-connection)): its app-server daemon outlives Stage too.
A worker hosts both on its machine under the same rule.

| Profile | When | Local processes |
| --- | --- | --- |
| `desktop` | `stage serve --managed` (the per-user service the desktop app ensures), or `stage worker run --managed` (the worker service) | yes |
| `server` | foreground `stage serve` or `stage worker run` | only with `--local-harnesses` or `STAGE_LOCAL_HARNESSES=1` (desktop dev, tests, a worker in a terminal) |
| `container` | `STAGE_MACHINE_PROFILE=container`, set by the Docker image | never, whatever else is set |

`GET /api/harnesses` reports the profile and, per harness, whether it is
available here with the reason when not. A harness that is not available
is refused with that reason everywhere: its setup routes answer 409
`harness_unavailable` without starting anything, the server composes no
setup or adapter for it, and a task on a connection of that kind (one a
desktop run left in the same data dir) cannot start (409
`connection_unavailable`). A remote machine follows its worker's profile:
the worker reports it over the channel, and its unavailable reason names the
worker's fix (`stage worker run --local-harnesses`, or the worker service);
until the worker described it since the server started, every kind is
listed and setup answers that the machine is offline or still starting
([execution.md](execution.md#remote-machines)). The profile is not stored: it is
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
| `<data dir>/claude/hooks-settings.json` and `hooks-settings-head.json` (worker: `<worker dir>/claude/`) | the server (or worker) with its first Claude Code adapter | never: Claude sessions read it again whenever they wake ([Claude Code hooks](#claude-code-hooks)) |
| `<data dir>/claude/images/stage-<requestId>/` (worker: `<worker dir>/claude/images/`) | a Claude Code launch with images ([execution.md](execution.md#claude-code-adapter)) | a rejected launch (its own folder); any launch with images, for folders older than 30 days |

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
currently 9). Health reports the same value in `apiVersion`. Upgrade servers
and workers together for the [API v9 rollout](api.md#api-v9-changes).

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
2. **API version for product requests.** Join, leave, machine status and
   gateway requests require the server's `Stage-Api-Version` (409
   `api_version_mismatch`). The SSE stream and its POST route use the
   independent channel handshake below.
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
`server/internal/workerchan` owns protocol and transport.
`server/internal/api` serves the routes and authenticates callers.
`server/internal/worker` runs the client and `server/internal/remote`
proxies remote machine calls. Each worker runs observation beside its
adapters and delivers batches to the main server.
[Remote machines](execution.md#remote-machines) · [Observation](execution.md#observation)

- **Handshake.** The authenticated opening GET carries the inclusive
  `Stage-Channel-Protocol-Min` and `Stage-Channel-Protocol-Max` integer
  headers. The server uses the highest shared version and advertises its
  `protocolRange` (`min`, `max`),
  `protocolVersion`, harness `kinds` and accepted `channelKinds` in
  `hello`. Malformed ranges get 400 `invalid_channel_protocol`.
  Disjoint ranges or a legacy worker with no range get 409
  `channel_protocol_mismatch`, naming the side to update.
- **Session and ready.** POSTs carry `hello.session` in
  `Stage-Channel-Session`. While negotiating, the worker composes setup
  with the server's advertised kinds and setup ports. Only liveness and
  composition messages may run in this phase. The worker then POSTs
  `kind: ready` with `body: {kinds, channelKinds, connections, details}`,
  where connections hold `{connectionId, traits}`. The server publishes
  the entire snapshot before waking sync and launch scheduling. Further
  ready messages replace the snapshot after setup changes. Duplicate
  snapshots have no effect. A newer stream replaces its predecessor and
  clears its capabilities. Old advertisements and results are discarded
  with 409 `channel_replaced`.
- **Harness actions.** Workers advertise `adapter.act` in `channelKinds`
  and `actor` for the connections that implement it. Scoped setup actions
  use the existing connector channel with the expected connection ID.
  A worker that did not advertise `adapter.act` cannot receive scoped setup
  calls. Stage sends an action once and reports a lost reply as uncertain.
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
  and the remote machine's adapter, ops and connector calls. An absent
  request kind or trait answers `operation_unavailable` before dispatch.
- **Worker-initiated calls.** An up POST may carry a call; its result is the
  POST's response. A callback names the waiting request (`call`) and runs
  only while that request waits (`checkpoint.save`, `launch.image`); after
  that it gets `call_ended`. A callback still running when its request stops
  waiting is cancelled, and the request returns after it ends. An up message
  is at most 32 MiB (launch images travel down one per `launch.image` reply).
  `hello.channelKinds` includes `setup.snapshot`, `setup.settings.get`,
  `setup.settings.save` and `setup.status.error`. `setup.snapshot` supplies
  public settings and active connection identities for the kinds selected
  by the worker. Setup reads needed by composition may run before ready.
- **Observation.** The main advertises `observe.batch` in
  `hello.channelKinds`. The worker advertises `observe.watch` in
  `ready.channelKinds` and `observe.batch` in `ready.upKinds`. Remote observation requires both. The main sends the
  full watch set when the worker attaches and as the set changes. The
  worker acknowledges it with its durable producer ID and any released
  cursor. Batches go up on the authenticated channel independently of
  command requests. A reconnect establishes a new channel session and
  keeps the producer and queued cursors. Update the main server and its
  workers together for this channel change.
  [Observation wire format](api.md#worker-observation)
- **Presence.** The server reports `negotiating` until ready is accepted,
  then `online` while the session remains live. A refusal appears as
  `needs_update` with `statusReason` in Machines until compatible
  negotiation begins. Presence and capabilities are process-local.
  `machines.last_seen_at` and `version` are written on connect, on
  disconnect and at most once a minute while connected (never moved back).
- **Shutdown.** Server shutdown closes every stream; workers reconnect to the
  next instance with backoff.

## Worker process

`stage worker` makes this machine a worker of a main server
(`server/internal/worker`, `server/internal/cli/worker.go`).

| Command | Does |
| --- | --- |
| `stage worker join <link> [--label L]` | Redeems the pairing link's code (`POST /api/worker/join`) and writes the worker config. Refuses when already joined (`stage worker leave` first). Plain `http://` links are accepted (LAN and loopback mains). |
| `stage worker run [--listen ADDR] [--local-harnesses]` | The foreground worker: holds the worker dir, serves the [agent gateway](#agent-gateway), connects the channel and reconnects until stopped. Over the channel it serves this machine's adapters, fs/process work and connection setup (`remote.Host`, `workersetup`; harness credentials by kind and key in schema-2 `credentials.json` beside the config, `0600`; known schema-1 entries upgrade atomically on first use). Its [machine profile](#machine-profile-and-hosted-harnesses) decides whether hosted harnesses (Claude Code, Codex) may run here: the worker service always, a foreground worker with `--local-harnesses` or `STAGE_LOCAL_HARNESSES=1`, a container never; it logs `machine profile` with `localHarnesses=on|off` at start. Harness options (`T3_FAKE`, `T3_FAKE_SEED`, `T3_BIN`) come from its environment: the managed worker reads them only from `worker.env`. Connections are set up in Settings > Connections; the T3 admin token, like the T3 token, stays in `credentials.json`. |
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
  stream that stayed up 30s. `channel_protocol_mismatch` waits the maximum
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

### Worker observation files

The worker keeps public compose inputs in `settings-cache.json`, its last
accepted snapshot in `watch.json`, and pending observations under `outbox/`, beside
the worker config. The files are private (`0600`, under `0700` directories),
atomically replaced and scoped to the current join. The settings cache
carries a schema and join identity, harness kinds, connection identities and
public settings. Harness credentials remain in `credentials.json`. Join
and leave discard the previous join's observation state.

A restarted worker can compose its adapters and resume observation from a
valid cache and watch before the main server is reachable. A first start
without a cache waits for setup. Invalid or mismatched cache data is logged
and recovered through the next successful channel attachment. Worker status
reports `waiting_for_setup`, `waiting_for_watch`, `recovery_needed`,
`sampling` or `degraded` under `process.observation`. On reconnect,
the worker reconciles public settings and connection changes with the main
and applies its fresh watch. Queued records keep their original identity.
Setup mutations and task commands still need the main server.

The outbox keeps the latest redundant sample per scoped session and
preserves distinct entries into `needs_input`, `done` and `failed`, including
an intervening state when needed to distinguish repeated entries. Samples
keep their observed times. Cursors are assigned when a batch is sealed.
Only one sealed batch is in flight at a time. Retries and restarts keep its
IDs and records intact. A partial acknowledgement advances the confirmed
cursor, and the sealed batch remains intact until fully acknowledged.
The watch reply advertises only the cursor through records already removed.
If the main database is restored behind that cursor, the main records the
missing range and requests a fresh sample. The worker can then deliver the
remaining batches under the same producer.

The persisted queue holds at most 16 MiB and 10,000 records, including the sealed
batch. Atomic writes use a temporary file before replacing the queue file.
A normal batch holds at most 100 records and 256 KiB. One larger
record may go alone within both the queue limit and the 32 MiB transport
limit. Under pressure the queue reclaims redundant samples first. If it
cannot retain all records, it accounts for the loss with a durable gap
count and time range. The main requests a fresh sample after ingesting a
gap. Resampling repairs the current view. The gap records the loss of
history that a later read cannot recover. Worker status reports the
producer's cumulative count in `process.observationLost`. A fresh
resample watch clears the degraded state and retains that count.

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
  `gateway_forbidden`, and so is pushing observations
  (`POST /api/connections/{id}/observations`) for another machine's
  connection; `/api/worker/*`
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
  build version `dev`. Workers need a compatible channel protocol for the
  channel and the same API version for the remaining worker routes.

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

## Claude Code hooks

Where Claude Code runs (the main server, or a worker that allows local
harnesses), its setup opens a hook receiver with the first Claude Code
adapter and keeps it until shutdown ([execution.md](execution.md#claude-code-adapter)).
It is not one of the [listeners](#one-process-many-listeners): it serves
only `POST /hooks` on `127.0.0.1` (never another interface), requires
`Authorization: Bearer <token>` (constant-time compare; 401 otherwise),
reads at most 1 MiB, answers 204 with no body before any work, and never
logs or stores a hook's body. The one exception is a `PermissionRequest`
naming a tool: it stays open (Claude waits up to 3600 s; Stage lifts the
listener's 10 s read and write bounds for it) as a request the user can
answer in Stage, with its tool input in memory only, until answered (200
with Claude's decision) or released (204: answered in Claude, superseded,
the session stopped waiting, or shutdown); Claude shows its own prompt
meanwhile ([execution.md](execution.md#claude-code-adapter)).
`<state dir>/hooks-settings.json` (dir `0700`, file `0600`, written atomically when its content changes) is the Claude
settings file every launch passes with `--settings`: one HTTP hook per
event, all with that URL and token (timeout 5 s, `PermissionRequest`
3600 s). It also allows `Read(//<state dir>/images/**)` so sessions
read their launch images without a prompt. `hooks-settings-head.json`
(the predecessor-base variant earlier versions wrote beside it) is
rewritten with the same content when it exists, for the sessions launched
with it, and never created. Claude saves the file's path with each
session and reads it again when it wakes the session, so the file always
names the current listener. A restart keeps the token and binds the port
the file names when it is free; otherwise it takes a free port and
rewrites the file, and sessions launched before reach the new port when
they next wake. While no listener answers (Stage stopped), Claude records a
non-blocking hook error in the session and the turn goes on; polling
catches up. When the receiver cannot open (no state dir, the folder or
port), launches carry no hooks, approvals are answered in Claude Code only,
and the setup status says why.

## Codex connection

Codex is hosted ([ADR 0006](adr/0006-codex-app-server-harness.md)): the server talks to the Codex app-server daemon on this machine, over WebSocket on the daemon's unix control socket (`codex app-server daemon version` reports its path). Connect (Settings or `stage harness codex run connect`) starts the daemon when it is down, reads the Codex login and connects the Codex home as the connection. Stage starts the daemon on demand (at most once every 30 s) and never stops, restarts or updates it: it outlives Stage, keeps running turns across Stage restarts and serves `codex resume` and Codex Desktop. The CLI runs with the server's environment minus caller session variables (`CODEX_THREAD_ID`, `STAGE_CALLER_SESSION`, T3 and Claude session variables), so its threads are never attributed to the server's own caller. The module’s settings keep the CLI path (`stage harness codex set binary=PATH`; empty: `codex` on PATH) and whether this machine connected, so a restart reinstalls the adapter without contacting the daemon until first use. Stage stores no Codex credentials: log in with `codex login`. `stage harness codex disconnect` stops tracking; the daemon and its threads stay. On a worker that allows local harnesses the same setup runs on the worker's machine (its `codex`, login and daemon), with that machine's public settings kept on the main server (`setup.settings.get`/`save`, [execution.md](execution.md#remote-machines)); no Codex credentials cross the channel, and the worker never stops its daemon either.
