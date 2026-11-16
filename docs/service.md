# Per-user service

`stage service` installs and drives an OS per-user service that supervises
the Stage server: a systemd user unit on Linux, a launchd LaunchAgent on
macOS. The server stays an ordinary foreground process (`stage serve
--managed`). Stage adds no supervisor of its own: restarts, logs and login
startup belong to the OS manager. The service runs as you, never root.

The desktop app (a later task) ensures the service through this CLI; the
service keeps running when the desktop window closes or the app quits.
Foreground `stage serve`, compose, and remote `--url`/`STAGE_URL` use are
unchanged. Code: `server/internal/service`, `server/internal/cli/service.go`,
`server/cmd/stage` (`--managed`).

## Commands

| Command | Does |
| --- | --- |
| `stage service start [--exe PATH] [--wait 30s]` | Registers or updates the definition, creates `service.env` if missing, starts the service if it is not running, and waits for readiness. It never restarts a running server; a changed definition is reported as `pendingRestart`. `--wait 0` returns once the start is requested. |
| `stage service restart [--exe PATH] [--wait 150s]` | `start`, and restarts a running server (new instance). |
| `stage service stop [--wait 2m]` | Stops it through the manager and waits until the server exited (the data-dir lock is free). |
| `stage service status` | State, definition, process, data-dir holder, Stage health, T3, log location and problems with fixes. |
| `stage service logs [-f] [-n 100]` | Recent log lines (journald on Linux, the log file on macOS). |
| `stage service enable` / `disable` | Login startup on/off. Neither starts nor stops it. |
| `stage service uninstall [--wait 2m]` | Stops it, turns login startup off and removes the definition. Keeps the data dir, `service.env` and logs. |
| `stage version [--json]` | This binary's identity, version, commit, API version, unit and label. |

Every command but `logs` takes `--json`. Service commands always target this
installation's local service: `--url` is refused and `STAGE_URL` is ignored.
Agent commands (`stage task ...`, `stage mcp`) never start anything; when
nothing answers, their error suggests `stage service start`.

Concurrent commands (two terminals, CLI and desktop) are serialized by a
lock in the config dir (`service.lock`); a second `start` joins the first and
both report the same server.

## Desktop contract

The desktop runs its bundled CLI by absolute path:

```sh
<bundled>/stage service start --exe <bundled>/stage --wait 30s --json
<bundled>/stage service status --json
```

`--json` prints exactly one JSON document on stdout, even on failure.
Mutating commands print a report:

```json
{"schema": 1, "command": "start", "ok": true, "code": 0, "message": "stage 1.2.0 ready (pid 4242) at unix:///run/user/1000/stage/stage.sock",
 "fix": "", "warnings": [], "notes": [], "logTail": [],
 "changes": {"definitionWritten": true, "definitionRemoved": false, "managerReloaded": true, "serviceEnvCreated": true,
             "started": true, "stopped": false, "pendingRestart": false},
 "status": { ... }}
```

`status` (also embedded in reports) is:

```json
{"schema": 1, "identity": "stage", "state": "running", "ready": true,
 "manager": {"kind": "systemd", "reachable": true, "version": "262.2-1-arch", "linger": true},
 "definition": {"name": "stage.service", "path": "...", "present": true, "generated": true, "schema": 1, "current": true,
                "exe": "/usr/bin/stage", "exeExists": true, "loadedFrom": "...", "needsReload": false, "loginStartup": false},
 "process": {"pid": 4242, "restarts": 0, "lastExitStatus": null, "result": "success"},
 "dataDir": "...", "serviceEnv": {"path": "...", "present": true},
 "socket": {"path": "...", "exists": true, "locked": true},
 "lock": {"held": true, "holder": {"pid": 4242, "instanceId": "...", "managed": true, "manager": "systemd", "...": "..."}, "ownedByService": true},
 "stage": {"url": "unix://...", "reachable": true, "ready": true, "version": "1.2.0", "apiVersion": 1, "instanceId": "...", "db": "ok"},
 "t3": {"connected": false, "lastError": "..."},
 "logs": {"kind": "journald", "unit": "stage.service"},
 "cli": {"version": "1.2.0", "apiVersion": 1, "exe": "...", "agentResolvesService": true},
 "problems": [{"code": "t3_offline", "message": "...", "fix": "..."}]}
```

`state` is `not-installed`, `stopped`, `starting`, `running`, `stopping`,
`failed` or `unknown` (manager unreachable); a crash loop (the manager
waiting to restart the process) is `failed`. `ready` means running, healthy
on the socket, and the data-dir holder is the service's own process (not a
foreground server). Problem codes: `manager_unavailable`,
`definition_foreign`, `definition_stale`, `definition_shadowed`,
`needs_reload`, `unit_masked`, `start_limit_hit`, `exe_missing`,
`exe_differs`, `env_mismatch`, `service_env_invalid`, `foreground_holder`,
`version_differs`, `api_mismatch`, `t3_offline`, `linger_off`,
`agent_socket_mismatch`.

| Exit | Meaning |
| --- | --- |
| 0 | ok (status: running and ready) |
| 1 | failed: the server exited or failed, or a manager command failed (status: failed or unhealthy) |
| 2 | usage |
| 3 | `--wait` expired, or another `stage service` command held the lock too long (status: starting or stopping) |
| 4 | status only: not running or not installed |
| 5 | conflict: another (foreground) server holds the data dir |
| 6 | no usable service manager |
| 7 | invalid configuration: refused executable, invalid `service.env`, foreign or shadowed definition, masked unit, run as root |

**T3 is separate.** Stage being ready and T3 being reachable are reported
apart (`stage`, `t3`, the `t3_offline` problem). A missing or offline T3
never fails `start`; it adds a note. Stage does not start or manage T3.

## Paths per identity

The installation identity (`buildinfo.Identity`, [runtime.md](runtime.md))
separates stable and developer installs on every axis.

| What | Linux | macOS |
| --- | --- | --- |
| Definition | `<user unit dir>/<id>.service`: the persistent `systemd/user` dir under `$HOME` in the manager's `UnitPath` (its own `$XDG_CONFIG_HOME/systemd/user`, usually `~/.config/systemd/user`) | `~/Library/Application Support/<id>/<label>.plist` (canonical, bootstrapped on start) |
| Login startup | `systemctl --user enable` (`WantedBy=default.target`) | copy at `~/Library/LaunchAgents/<label>.plist` |
| Name | unit `<id>.service` | label `buildinfo.ServiceLabel`, default `io.github.flexdinesh.<id>` (packaging may set it with `-ldflags -X`) |
| Config | `$XDG_CONFIG_HOME/<id>/service.env`, else `~/.config/<id>/service.env` | `~/Library/Application Support/<id>/service.env` |
| Command lock | `<config dir>/service.lock` | same |
| Data dir | the identity default (runtime.md), always | same |
| Socket | the data dir's private default socket (runtime.md) | same |
| Logs | journald (`journalctl --user -u <id>.service`) | `~/Library/Logs/<id>/stage.log` (+ `.1`, `.2`), `launchd.stderr.log` |

**Pinned environment.** On first registration, the shell's absolute
`XDG_DATA_HOME` and `XDG_CONFIG_HOME` are written into the definition when
they differ from the manager's environment (launchd agents never have
`XDG_*`; the systemd user manager has only its own). The service then
resolves the same data and config dirs as the shell that registered it,
and every `stage service` command derives paths from the definition plus
the manager's environment, not the current shell. A later shell with other
values gets an `env_mismatch` warning; to rebind, `uninstall` then `start`.

## service.env

The service's only configuration: a `0600` `KEY=VALUE` file (comments with
`#`, values optionally quoted). `stage service start` creates it from a
template (`STAGE_LISTEN=unix`) and never overwrites it. Apply edits with
`stage service restart`.

- Allowed keys: `STAGE_LISTEN`, `STAGE_PUBLIC_URL`, `STAGE_ALLOWED_ORIGINS`,
  `T3_URL`, `T3_PUBLIC_URL`, `T3_ADMIN_TOKEN`, `T3_FAKE`, `T3_FAKE_SEED`.
  Unknown keys, `STAGE_DATA_DIR` and `STAGE_ENV` are errors.
- `STAGE_LISTEN` must include `unix` (the socket desktop and CLI use); add
  TCP for a browser with `unix,127.0.0.1:7380`.
- A file readable or writable by group or others is refused (it may hold
  `T3_ADMIN_TOKEN`).
- `--managed` ignores those keys, `STAGE_DATA_DIR` and `STAGE_ENV` in the
  inherited environment (`systemctl --user import-environment`,
  `launchctl setenv` can leak them), so the service never depends on a shell
  or on webview storage. A missing file means the template defaults.
- T3 credentials: pairing (Settings, or `t3 pair`) stores Stage's T3 token in
  `stage.db`, so a service needs nothing else. `T3_ADMIN_TOKEN` is optional
  (auto-renew) and is never assumed inherited by GUI launches.
- `stage service start` validates the file before starting, so a bad file is
  an error there (exit 7) instead of a restart loop.

## Definitions

Linux unit (generated; local overrides belong in a drop-in, `systemctl
--user edit <id>.service`, which rewrites never touch):

```ini
[Unit]
Description=Stage server (stage)
StartLimitIntervalSec=5min
StartLimitBurst=10

[Service]
Type=notify
ExecStart=/usr/bin/stage serve --managed
SyslogIdentifier=stage
Restart=on-failure
RestartSec=2s
RestartSteps=5
RestartMaxDelaySec=60s
RestartPreventExitStatus=75 78
TimeoutStartSec=120s
TimeoutStopSec=110s

[Install]
WantedBy=default.target

[X-Stage]
Generator=stage-service
Schema=1
Exe=/usr/bin/stage
```

macOS plist: `ProgramArguments` `<exe> serve --managed`, `RunAtLoad`,
`KeepAlive {SuccessfulExit: false}`, `ThrottleInterval 10`, `ExitTimeOut
110`, stdout and stderr to `launchd.stderr.log`, `EnvironmentVariables`
with `STAGE_SERVICE_SCHEMA` and any pinned variables, plus a generator
comment. Golden copies: `server/internal/service/testdata/`.

**Rewrites.** A definition without the generator marker is never modified
or removed (exit 7). Neither is a unit or plist with this name that the
manager loaded from another file (packaged or hand-written): `start`,
`stop`, `disable` and `uninstall` refuse it (exit 7, problem
`definition_shadowed`). Every command re-reads the definition after taking
the command lock. Ours is written when missing; rewritten when it differs
and runs the same executable, or when `--exe` is given; kept with a warning
when it runs another executable. On systemd a write is followed by
`daemon-reload`, and `start` refuses a unit loaded from another file
(shadowed) or masked. On macOS a rewrite also refreshes the login copy.

**Executable.** The definition records a stable absolute path: `os.Args[0]`
when it is the same file as the running binary (so `/usr/bin/stage` or a
Homebrew-style link is kept across upgrades; Linux's `os.Executable` is
already resolved), else `os.Executable()`. `--exe` overrides it and must
report the same identity through `<exe> version --json`. Relative paths,
temporary and build-cache locations (`go run`), macOS App Translocation and
the Trash are refused. It never depends on the caller's cwd or `PATH`; the
manager tools are found by absolute path (`/bin/launchctl`, `systemctl` via
`PATH` then the system dirs). A server version different from the CLI is a
note only; a later updater coordinates restarts.

## Lifecycle

- **Start twice gives one server:** the manager runs one instance and the
  data-dir lock (runtime.md) refuses a second process. `start` treats the
  service as ready only when `/api/health` answers on the socket and the
  lock holder record names the service's own pid and the health's instance.
- **Managed vs foreground:** both take the same data-dir lock. `start`
  refuses (exit 5) when a foreground server holds it, naming its pid; a
  foreground `stage serve` against the service exits 75 and says to run
  `stage service stop`. `stop` never touches a foreground server.
- **Crash restart:** systemd restarts after 2s, backing off to 60s
  (`RestartSteps`, systemd 254+); `StartLimitBurst` gives up after 10 starts
  in 5 minutes (with backoff this is reached only on systemd < 254, which
  ignores `RestartSteps`/`RestartMaxDelaySec` with a warning and retries
  every 2s, so the unit fails after about 25s; `start` runs `reset-failed`
  first). launchd respawns no sooner than every 10s.
- **Exits that a restart cannot fix** are not retried: 75 (data dir held by
  another server) and 78 (invalid configuration) via
  `RestartPreventExitStatus`. launchd cannot exclude exit codes, so on macOS
  the managed server logs the reason and exits 0 instead.
- **Fail fast:** if the server exits while `start` waits, `start` fails at
  once with the last log lines (systemd keeps retrying until stopped).
- **Stop** goes through the manager only (`systemctl --user stop`,
  `launchctl bootout`), never a pid signal; the server drains for up to
  100s and the manager kills it at 110s.
- **Client exit:** the service is independent of the CLI or desktop that
  started it.

## Sessions, logout and login startup

Login startup is off by default and separate from the running state.

- **Linux:** the user manager runs while you have a session. Without
  lingering it stops at your last logout, taking the service with it. With
  lingering (`loginctl enable-linger`) a started service survives logout,
  and an enabled one also starts at boot. Stage never changes lingering;
  `status` reports it.
- **No user manager** (no logind session, WSL without systemd, containers,
  `su`/ssh without pam_systemd): `stage service` exits 6 with the cause and
  remedies (log in through a pam_systemd session, or enable lingering and
  set `XDG_RUNTIME_DIR`, or run `stage serve --listen unix` in the
  foreground or under your own supervisor). There is no fallback
  supervision. Without `XDG_RUNTIME_DIR` in the CLI's environment it retries
  with `/run/user/<uid>`.
- **macOS:** LaunchAgents run in your GUI session (`gui/<uid>`) and stop at
  logout; without a GUI login `stage service` exits 6. `enable` makes the
  agent load at login; macOS 13+ shows a Background Items notice and lets
  you block it in System Settings > General > Login Items & Extensions.
- **Agent socket:** if a shell lacks `XDG_RUNTIME_DIR` (`su`, some ssh),
  agent commands look for the socket under the data dir and miss the
  service; `status` reports `agent_socket_mismatch` with the fix.

Surviving a desktop Quit is not a promise to run while logged out.

## Logs

- Linux: journald, rotated by journald. `stage service logs` runs
  `journalctl --user -u <id>.service`.
- macOS: the managed server writes its own log to
  `~/Library/Logs/<id>/stage.log`, rotated at 10 MiB keeping two old files,
  opened only after it holds the data-dir lock (a losing process never
  rotates the live log). launchd's stdout/stderr (panics only) go to
  `launchd.stderr.log`, which the server truncates at start when over 1 MiB.

## Verification

- `mise run check` covers the definitions (golden files), path resolution,
  executable rules, `service.env`, and the controller against stateful
  fakes of systemd and launchd (fresh start, concurrent starts, foreground
  conflict, exit 75 and crash loops, timeouts, no manager, definition rules,
  masked units, stop/restart, enable/disable/uninstall, pins, T3 offline,
  launchd bootstrap quirks and login copy), plus `serve --managed` exit codes
  and data-dir ownership in subprocesses.
- `mise run service:smoke` drives the real systemd user manager with the
  isolated identity `stage-svctest` (data and config under
  `.scratch/svctest`, only its own marked unit in `$HOME`, removed after):
  concurrent starts give one pid, SIGKILL is restarted, foreground and
  managed cannot double-own, restart, stop twice, a foreground holder makes
  `start` exit 5 and a direct `systemctl start` fail with 75 and no restarts,
  enable/disable, logs, `systemd-analyze verify`, uninstall keeps data.
  Passed on Linux (systemd 262).
- **Not verified:** launchd on a real Mac (`launchctl print` output and error
  codes are parsed defensively from documented behavior; capture fixtures
  on macOS 13+ and extend the smoke check), systemd < 254, and Linux without
  lingering across a real logout.
