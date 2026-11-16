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
4. Open the database and guard its schema ([below](#schema-and-backups)): a schema newer than this binary exits 78; pending migrations on a non-empty database are preceded by a backup (a failed backup exits 1, unmigrated). Migrate, start T3, recover launching tasks (`RecoverLaunching`, so a restart relaunches nothing), and start the background loops.
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
| 1 | any other failure (bind error, database, shutdown timeout) |
| 2 | bad command line (for example `--managed` with `--listen`) |
| 75 | the data dir is held by another server |
| 78 | invalid configuration (`STAGE_*`/`T3_*` values, `service.env`, socket path), or a database schema newer than this binary |

systemd does not restart on 75 or 78 (`RestartPreventExitStatus`). launchd
cannot exclude exit codes, so a `--managed` server on macOS logs the reason
and exits 0 for those two.

## Schema and backups

Migrations are goose versions (`server/internal/db/migrations`, tracked in
`goose_db_version`). `stage version --json` `schema` is the highest this
binary knows; `/api/health` `schema` is the database's.

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
- **`stage setup mcp`** passes `STAGE_URL` to the stdio server only when one was given explicitly; otherwise the stdio server resolves the server itself. `--http` needs an http(s) URL; if the server is only on a socket, pass `--url`.

## Health and version

`GET /api/health` (contract in [api.md](api.md#health)):

- It answers 200 with build and instance metadata whenever the server and its
  database work.
- T3 being offline is still 200, with `t3.connected: false`. "Stage is live
  and ready" is therefore separate from "T3 is reachable".
- 503 means the database query failed.
- It does not expose paths or the pid.

Every `/api` response carries `Stage-Api-Version: <n>` (`buildinfo.APIVersion`,
currently 1). The number changes only on breaking API changes.

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
| Desktop webview (bridge B) | Host `stage.invalid`, Origin `mygo://localhost`, on the socket |
| Tailscale serve / Caddy | its origin is `STAGE_PUBLIC_URL` or listed in `STAGE_ALLOWED_ORIGINS` |

**Upgrade note:** a proxied hostname that is neither `STAGE_PUBLIC_URL`'s host
nor listed in `STAGE_ALLOWED_ORIGINS` now gets 403. Add it before upgrading.
