# Desktop app

The desktop app (`desktop/app`) is a MyGo shell for Linux and macOS that
shows the shared React app (`@stage/app`) in a webview and talks to one
Stage server at a time: this machine's over its Unix socket (the default),
or a saved remote server over HTTP(S) ([Servers](#servers)).
Each server stays the sole owner of its SQLite, T3 connection, scheduling
and business rules; this machine's per-user service supervises the local
one ([service.md](service.md)). Use **Servers** in General settings to add
and switch installations. A desktop can also run as a worker through the
CLI so this machine's harnesses run a remote server's tasks.
[Worker](#worker)

The desktop owns windows, OS integration
and transport. It never opens `stage.db`, holds no business logic and
never manages a remote server. Browsers keep using TCP.
Decisions: [ADR 0001](adr/0001-desktop-shell.md).

## Layout

| Path | What |
| --- | --- |
| `desktop/app/` | Go module `github.com/flexdinesh/stage/desktop/app` (MyGo v0.9.0). Never imports `server/internal` or the fixture. |
| `bridge/` | Bridge B: the `mygo://localhost` scheme serves the embedded UI (`dist/`, with a CSP header) and reverse-proxies `/api/*` to the active server (socket or TCP). Request bodies without a `Content-Length` (macOS WKWebView sends none) are forwarded with unknown length. |
| `target/` | The servers: the local target (implicit) and saved remotes (`servers.json`), each one's transport (no HTTP proxy), Host and base URL. No MyGo import. |
| `session/` | Native session storage and HTTP authorization for a shared Stage server. No MyGo import. |
| `launch/` | Launcher and monitor: ensures the local service through the bundled CLI, checks readiness and API version (socket or TCP), watches health, switches servers, tests a server URL, and runs this machine's worker (`worker.go`: join, leave, restart, state). No MyGo import. |
| `nav/` | URL policy: what the webview may load, what goes to the OS, deep links, notification click paths. |
| `alerts/` | Native notifications: every saved server's notification stream, catch-up and foreground policy, device preferences (`notifications.json`), cleanup, badge, the OS settings opener. No MyGo import. |
| `main.go`, `shell.go`, `service.go`, `servers.go` | Windows, menus, tray, single instance, close/Quit, login startup, notification clicks, the bound `Desktop` service (server list and switching in `servers.go`) and its events. |
| `notifications.go` | The MyGo side of `alerts`: OS notifications and the Dock/launcher badge. |
| `selfcheck.go` | `STAGE_DESKTOP_SELFCHECK=1`: print how the bundled CLI resolves, then exit (release verification). |
| `mygo.config.ts` | Build config per identity (`IDENTITY=stage` or `stage-dev`). |
| `ui/` | pnpm workspace package `@stage/desktop`: the desktop host frontend (mount, adapters, generated `src/mygo.ts`). Builds into `desktop/app/dist`. |
| `itest/`, `e2e/` | Headless integration test against a real `stage serve`; webview e2e (build tag `e2e`). |

## Launch sequence

1. **Single instance.** A second launch hands its arguments (and any deep link) to the running app, which shows and focuses its window, then exits.
2. **Window.** The window opens at once with the connection screen (hidden when the OS started the app at login).
3. **Service.** In `WhenReady`, the launcher runs the bundled CLI by absolute path (`App.Path(PathResources)/bin/stage`):
   `stage version --json`, then `stage service start --exe <cli> --wait 30s --json` ([service.md](service.md#desktop-contract)). Nothing runs under `MYGO_GENERATE`, so binding generation starts no service.
4. **Readiness.** `GET /api/health` over the socket (Host `stage.invalid`), within 10 s: `service` must be `stage` and `apiVersion` must equal the bundled CLI's. A different build `version` is a non-blocking banner.
5. **App.** The bridge points at the socket and the shared app mounts.
6. **Monitor.** Health every 5 s. A failure is `reconnecting` (retries back off 1 s to 5 s); after 30 s without an answer the launcher reads `stage service status --json` and reports `stopped` or `failed`. Each return to ready increments `epoch`: the app refetches its data and reopens live transcripts.

With a remote server active, steps 3 and 4 differ: no service command runs (only `stage version --json`, for the API version this app needs), readiness is `GET /api/health` over TCP with the remote's Host, and the monitor reports `unreachable` instead of asking a service ([Servers](#servers)).

While the target is down, the bridge answers JSON requests 503 (`{code:"starting"|"unavailable"}`) and holds `text/event-stream` requests up to 25 s, then answers an empty `200 text/event-stream` with `retry: 1000`, so the webview's EventSource keeps reconnecting (a 5xx would close it for good, and MyGo turns an aborted handler into a 500).

The app opens no TCP listener for desktop-to-Stage traffic; the integration test asserts zero TCP listeners for the server and the desktop process. A remote server is reached with outbound TCP only.

## Connection states

The shell publishes one `HostState` (`packages/app/src/platform/index.ts`, mirrored by `launch.State`) through the `host:state` event.

| Phase | Cause | Shown | Primary action |
| --- | --- | --- | --- |
| `starting` | ensuring the service, waiting for health | connection screen after 400 ms (no flash on a fast start, again after each switch), steps service / server / api (remote: server / api) | none |
| `ready` | healthy, same API | the app | none |
| `reconnecting` | was ready, stopped answering | banner over the page | Retry now |
| `stopped` | not running after 30 s, or stopped | screen before the first ready, banner after | Start Stage server |
| `failed` | `start` exit 1, crash loop, unhealthy | same, with the log tail | Start Stage server |
| `unsupported` | exit 6: no usable service manager | screen with the cause and a foreground command | Retry |
| `misconfigured` | exit 7 (invalid `service.env`, foreign or shadowed definition, masked unit), or the app runs from a temporary location (`/tmp`, `/var/folders`, App Translocation, Trash) | screen with fix and problems | Retry |
| `conflict` | exit 5 and the terminal server cannot be used | screen with fix and command | Retry |
| `incompatible` | another API version | blocking screen, also after the app ran | Restart Stage server (remote: Retry once that machine is updated) |
| `unreachable` | remote only: no answer at its URL, or its origin policy refused this app (403); polling continues | screen before the server's first ready (since the switch), banner after | Retry |
| `worker` | this machine only: it is a worker of a main server, so its own server is off ([Worker](#worker)) | screen "This machine is a worker of Studio" with the worker's state | Leave and use this machine |

"Before the first ready" counts per server: the page gates on `serverEpoch`, so after a switch the app stays unmounted until the new server answers. The screen and the banner offer no server switching.

- **Terminal server (exit 5).** When a foreground `stage serve` holds the data dir and its socket answers health with the same identity and API version, the desktop uses it and shows a quiet banner: "Connected to a Stage server started from a terminal (pid N). It stops when that terminal does." A default foreground server listens on TCP only, so this usually lands on `conflict`, whose command adds a socket (`stage serve --listen unix,127.0.0.1:7380`).
- **Version skew.** A server build different from the bundled CLI shows a dismissible banner with "Restart Stage server" (`stage service restart --exe <cli> --wait 30s --json`).
- **T3.** Never blocks startup. T3 offline or unpaired uses the app's existing status button and Settings pairing.
- **Diagnostics.** "Show details" lists the server log tail; "Copy diagnostics" (screen, Settings, Help menu) copies app, CLI and server versions, the identity, `service status --json` without secrets and the last 50 log lines.

## Servers

Open **Settings → General → Servers** to add or switch to a saved server.
**Add server** opens the name and URL fields and a connection check. After
**Save server**, use **Switch** on the server's row.

The desktop connects to one Stage server at a time. Each server is bound to its own T3 backend; the desktop never manages a server's T3 beyond showing that server's onboarding.

- **This machine** (`local`): implicit, always first, labeled "This machine", never edited or removed. Reached over its Unix socket; its service is ensured as in the [launch sequence](#launch-sequence).
- **Remote servers**: saved by name and URL (`http(s)://host[:port]`, no path, credentials, query or fragment). Personal servers use the existing trusted-network connection. Shared servers use their configured canonical URL and require [sign-in](#shared-access). Names and URLs are unique. "This machine" is reserved. The desktop never starts, stops or configures a remote.
- **Remote server setup** (on that machine): reach a personal server through its trusted listener or proxy. A hostname must be in `STAGE_ALLOWED_ORIGINS` or match `STAGE_PUBLIC_URL`. For a shared server, use its canonical HTTPS URL through the proxy. [Runtime origin policy](runtime.md#request-origin-policy)

### Saved list

`<userData>/servers.json` (next to `notifications.json`), mode 0600, written atomically on every edit:

```json
{"version":1,"active":"local","servers":[{"id":"s-1a2b3c4d","name":"Studio","url":"http://192.168.1.20:7380"}]}
```

| Field | Meaning |
| --- | --- |
| `active` | the server the app connects to at launch (`local` by default; an unknown id falls back to `local`) |
| `servers[].id` | `s-` + 8 hex, never `local` and never containing `/` (notification ids are `<id>/<tag>`) |
| `servers[].url` | normalized: lowercase scheme and host, default port dropped, no trailing slash |

Missing: local only. Corrupt or another version: logged, local only, replaced on the next edit. Invalid entries are dropped.

### Switching

`SwitchServer(id)` runs when the user switches in Settings or opens a
notification from another saved server. When a different server is saved,
the sign-in, connection and worker screens also offer a switch action:

1. The choice is saved as `active`.
2. The bridge is unset at once: every open request and stream to the old server ends (in-flight JSON gets 503 `unavailable`, streams end and EventSource reconnects into the wait), so nothing of the old server reaches the page afterwards.
3. The state starts over for the new server: `starting` (local: steps service / server / api; remote: server / api), `target` set, `server` null, `serverEpoch` 0. A start still running for the old server is cancelled, and its late outcome is dropped.
4. The page starts a fresh query client when the target ID, URL or account epoch changes. If the app had run on the old target it opens `/`. A navigation that triggered the switch (a notification click) arrives after it and wins. Until the new server answers the page shows the blank ground, then the connection screen.
5. On ready, the bridge points at the new server, `epoch` and `serverEpoch` increment, and the app mounts.

Switching to the active server only refreshes its name. Editing the active server's URL reconnects it; removing it switches to this machine first.

### Remote differences

- No service commands: Start/Restart server, "Open Stage at login" and the server logs are this machine's only (`ErrRemote` from the shell; the page hides them for a remote). While this machine is a worker, "Open Stage at login" stays available and acts on the worker service ([Worker](#worker)). Diagnostics name the remote and skip `service status` and logs.
- Phases: `starting` (server, api), `ready`, `reconnecting`, `unreachable`, `incompatible`. Never `stopped`, `failed`, `unsupported`, `misconfigured` or `conflict`; no foreground banner and no version-skew banner.
- For T3, the local target opens its native link and a remote target opens its web link. OpenCode uses its configured web client for both targets.
- The bridge checks the page origin before forwarding; a cross-site request or an unsafe method from another origin gets 403. Personal requests use the target's Host and carry no `Cookie`, `Origin` or `Sec-Fetch-*`.
- `desktop:dev` loads the page from Vite, whose `/api` proxy goes to the dev socket: switching there changes the launcher and notifications, not the page's `/api`. Use a packaged build to try remotes in the UI.
- Settings, General, Info shows a remote's URL instead of the socket and no "per-user service" line.

### Test connection

`TestServer(url)` probes `GET /api/health` as the bridge would, with an 8 s bound. A 403 from the origin policy reports a reachable server and offers the trusted-origin setting. A connection failure asks the user to check the server or HTTPS proxy. An incompatible API version reports an error.

### Shared access

Personal is the default. Its service starts on its private product socket
and opens directly to the workspace. To share the local installation, the
owner opts in through [service.env](service.md#serviceenv) and completes
[first-admin setup](runtime.md#shared-setup-and-owner-recovery) in a browser.
For a remote shared server, save and switch to its canonical HTTPS URL.
Shared servers still run the desktop's health and version checks. A reachable shared server
with a 401 from `/api/me` shows **Sign in to Stage**. During first setup
the desktop asks the user to finish in a browser. Open private setup,
invite and reset links at the canonical browser URL. A remote shared server
uses HTTPS. HTTP is available on loopback for local testing. Then sign in to
the same account with email and password in the desktop app. Open
Settings → Account to edit your profile, change your password, manage your
agent credential or sign out.

The `mygo://localhost` origin serves the app. Go drops webview cookie and
authorization headers on shared requests and strips every upstream
`Set-Cookie`. It captures the upstream `stage_session` only from successful
explicit sign-in. Before using it, Go checks `/api/auth/status` and matches
the saved server ID, exact canonical target and installation ID. A local
shared server uses the canonical HTTP Host while Go dials its Unix socket.
Go sets the verified canonical `Origin` on writes. The bridge and native HTTP
clients reject upstream redirects. The same session authorizes webview API
calls, attachments, event streams and that server's native notifications.

Desktop attachment URLs include the active target and account generation.
The bridge serves them with `no-store`. Switching targets or accounts closes
attachment windows and invalidates old attachment URLs on the shared scheme.

Go stores only the opaque session, its expiry and binding metadata. Each
file is written atomically with mode `0600` in the owner-only `sessions/`
directory under the desktop app's user data. A restart reuses a still-valid
session. A 401, expiry, sign-out, password change, replacement installation,
server removal or URL edit clears the affected credential and ends requests
using it. Native alert badges, shown notifications and cursors reset for
that server. On the active server the page drops its query, mutation and
shared view state and remounts the account gate. Other saved servers retain their own sessions
and alert state. Switching targets ends webview requests and resets the page,
and each saved server can continue its own native notification stream.

## Worker

Joining is not offered in the UI today (see the introduction); an existing
join still shows in Settings, General, Info with Restart and Leave. With a
remote server active, the shell can make this machine a **worker** of it ([ADR 0002](adr/0002-machines-and-workers.md),
[runtime.md](runtime.md#worker-process)): this machine's T3 and OpenCode run
that server's tasks, its agents reach that server through the worker's
agent gateway, and harness credentials stay here. "This machine" stays the
default; nothing changes until you join.

**One role per machine.** One identity runs the Stage server or a worker,
not both ([service.md](service.md#worker-service)): the worker's agent
gateway owns the identity's default socket, so agents' bare `stage` and
`stage mcp` reach the main server. Joining therefore turns this machine's
own server off (its data stays, dormant), and leaving brings it back.

### Join, leave, restart

`HostServices.worker` (`packages/app/src/platform/index.ts`), the bound
`Desktop` methods `JoinWorker(link)`, `LeaveWorker(local)` and
`RestartWorker()` (`launch/worker.go`). Every command runs the bundled CLI
by absolute path.

| Action | Steps |
| --- | --- |
| **Join as worker** (remote active, not joined; not offered in the UI) | The page creates a pairing on the active server (`POST /api/machines/pairings`, as Settings, Machines, Add), so the link uses that server's `STAGE_PUBLIC_URL`; without one its 409 message shows. The host then runs `stage worker join --json <link>`, `stage service uninstall --wait 30s --json` and `stage worker service start --exe <cli> --wait 30s --json`. When the server service cannot be uninstalled (a foreign definition) or no service manager can run the worker (exit 6), the join is undone (`stage worker leave`, else `--local`). A failed worker start keeps the machine joined and shows `failed` with its fix. When the join fails the page revokes the code. Login startup moves to the worker (`stage worker service enable`). |
| **Leave** (inline confirm, as removing a machine) | `stage worker service uninstall --wait 30s --json`, then `stage worker leave --json`, which removes the machine on the main server (its connections are retired). When that server cannot be told (unreachable), the error shows with "Forget it on this machine only" (`stage worker leave --local --json`; the server keeps the machine as offline until it is removed there). Login startup moves back (`stage service enable`). With this machine's target active, its server starts; with a remote active, nothing local starts. |
| **Restart worker** (offline, failed, needs update) | `stage worker service restart --exe <cli> --wait 30s --json`, e.g. after an app update changed the bundled version. |

### State

`HostState.worker` (null when not joined) comes from `stage worker service
status --json` (local only, no call to the main server): at launch, on each
switch and retry, after each action, and every 10 s while joined. Its
`serverId` and `serverName` are the saved server at the worker's URL (or the
active remote whose `publicUrl` it is); otherwise the URL's host.

| Status | When | Marker |
| --- | --- | --- |
| `starting` | the worker service is starting | run |
| `online` | running, channel to the main server ready | ok |
| `offline` | running but not connected (main server unreachable; it reconnects by itself), or the service is stopped | idle |
| `needs_update` | the worker and main server have disjoint channel protocol ranges; the message names the side to update | bad |
| `removed` | the main server removed this machine (token revoked; the worker stopped) | bad |
| `failed` | the service failed, or the service manager is unreachable | bad |

- **Settings, General, Info.** Joined: "Worker of Studio" with the marker,
  `online · machine 4 · <url>` and the message; Restart worker and Leave.
  "Open Stage at login" shows while joined, also with a remote active
  ("starts hidden, with the worker"). Not joined: no worker row.
- **Banner.** Problems only, while the active remote is the worker's server
  and ready: `offline` plain, `needs_update`, `removed` and `failed` bad,
  with Restart worker (when it helps) and Settings. Online and starting stay
  quiet.
- **This machine's target** while joined: the `worker` phase. The launcher
  never starts, probes or bridges this machine's socket then (it is the
  gateway, which forwards to the main server), and this machine's
  notification stream is dropped (its cursor forgotten); leaving restores
  both. The server list names it `this machine · worker of Studio`.
- **Diagnostics** add the worker line and `stage worker service status
  --json` (secrets redacted).
- A development run (`STAGE_DESKTOP_SOCKET`) has no worker.

## Window, close and Quit

- Closing the last window hides it; the app stays resident (reachable from the Dock, the tray, the launcher or a second launch), and native notifications continue ([Notifications](#notifications)).
- Explicit Quit (`Cmd/Ctrl+Q`, menu, tray) stops the desktop app and its notifications only. The Stage service keeps running.
- Linux: the first close shows a one-time dialog, "Stage keeps running. Reopen it from the tray or your app launcher.", with Keep running and Quit Stage. It shows whether or not a tray exists, because a missing tray host (GNOME without AppIndicator) is undetectable.
- Tray (where available): Show Stage, Quit Stage. Notification controls live in Settings only.
- Window size, position and maximized state persist (`StateKey "main"`). Minimum 720x520.
- Menus: app menu (macOS) or File with Quit (Linux), Edit (so copy and paste work on macOS), View (Reload `Cmd/Ctrl+R`, zoom), Go (Back `Cmd+[` on macOS or `Alt+Left` elsewhere, Forward `Cmd+]` or `Alt+Right`: the webview's history, which the page's own back and forward arrows also walk), Window (Minimize, Close `Cmd/Ctrl+W`), Help (Copy Diagnostics, Show Logs). The page keeps its own shortcuts (`Ctrl/Cmd+K`, `N`). The desktop host maps the mouse side buttons to back and forward (the webview has no browser chrome) and cancels their default, so an engine that handles them itself does not move twice.
- MyGo's own folders (window state, cache, logs) use the app identifier, never the Stage data dir (`~/Library/Application Support/Stage` would collide with `.../stage` on case-insensitive APFS).

## Notifications

Native OS notifications come from the resident Go process (`alerts/`, shown through `notifications.go`), not the webview, so they keep working while the window is closed. The browser keeps Web Push; the desktop webview never uses the web Notification API or Web Push (its permission handler denies every permission request).

- **Source.** `GET /api/notifications/stream` of **every saved server at once** (this machine's over its Unix socket, remotes over TCP, [api.md](api.md#get-apinotificationsstream)), each with `Last-Event-ID` set to its own stored cursor. Every launcher ready (first start, recovery, a new server: each `epoch`) reconnects that server's stream; with a remote active, this machine's socket comes from `stage service status --json` (read only, nothing started). A dropped stream retries with jittered backoff, 1 s to 30 s. A server without the stream (404, older than the app) is logged once and waited out until its next ready. Adding a server opens its stream; removing one closes its notifications and forgets its cursor.
- **Shared access.** The notification stream requires a human session for the saved server. An `access-ended` event or HTTP 401/403 clears that server's cursor, badge, queued delivery and shown notifications. Its stream waits for sign-in. Replacing the session starts with a silent catch-up baseline. Network errors and server outages keep the existing cursor and use the usual reconnect backoff.
- **What shows.** The event filter applies across devices, for the workspace in personal mode and for the signed-in account in shared mode. The desktop's own on/off is per installation (`notifications.json`), on by default. Only unread rows show.
- **Foreground.** Window visible and focused on the row's server: no OS notification; the page's toasts show the row (same as the PWA). Otherwise (window hidden or unfocused, or another server's row) an OS notification. Each delivery of the active server (a live unread row, or a catch-up batch), shown or not, fires `notifications:delivered`, so the page refetches its inbox at once.
- **Content.** Title, body and target are the row's Web Push payload (`notify.PayloadFor`, one server function): title Needs input, Failed, Ready or Done, body `<title> · <project>`. With more than one server saved, the title starts with the server's name (`Studio · Needs input`). The notification ID is `<server id>/<group tag>` (`local/task-12`, `s-1a2b3c4d/session-<connectionId>:<externalSessionId>`; `<server id>/notification-<id>` for a row without one), so a newer event of a task or external session replaces its shown notification, and the same task number on two servers never collides.
- **Clicks.** Switch to the notification's server, then show its task at `/tasks/<n>` or the inbox at `/?inbox` for external sessions. Historical session/answer URLs are rejected and replaced from notification identity. The inbox opens the session in its harness. Settings/test notifications use `/settings`. macOS clicks after Quit relaunch the app; Linux clicks require a running app.
- **Catch-up** (each connect of each server, after the replay). Rows up to the server's `latestId` at connect show only when unread, newer than the cursor and at most 15 min old: the newest per group (rows without one stay silent), at most 3, oldest first. The rest collapse into one "N more in Stage" ("Open notifications") that opens the inbox drawer (`/?inbox`). Older rows stay silent: inbox and badge only. First run (no cursor) and a reset or smaller database (`reset`, or `latestId` below the cursor) move the cursor to `latestId` silently. With the window in front or notifications off nothing shows; the cursor still advances.
- **Cleanup.** Each server's `unread` snapshot closes that server's shown notifications whose group has no unread rows (read on any device) and its summary once none of its groups is unread; the first snapshot also closes earlier runs' notifications listed in its `shown` (macOS; a no-op on Linux). The page closes a task's or session's notification of the active server when its detail opens (`closeShown`), reconciles on focus and on each delivery (`syncShown`), and opening the drawer closes all of the active server's.
- **Badge.** Go sums each server's snapshot `badge`, using the needs-input count for personal servers and the signed-in account's unread count for shared servers. It updates the Dock icon (macOS) or launcher entry (Linux docks that read the Unity launcher API) even while notifications are off. The server list shows needs-input counts for personal servers (`needsInput`, `servers:changed`).
- **Delivery.** One worker shows queued notifications in order, so a slow show never stalls the stream. A newer note for a queued ID replaces it. One show may wait up to 2 min (the first macOS prompt). A note whose group was read while it showed is closed at once. Losing or replacing access cancels delivery for that session and closes any notification that finishes showing afterward.

The native sign-in owner supplies the notification stream's credentials through the Go-only `alerts.Service.SetSession(target, session)` hook. Supply an opaque ID unique to the sign-in and a request preparation function that verifies the installation and attaches its credential for that exact target. Restore the saved session before the stream starts to reuse its cursor. Pass an empty session on sign-out. `alerts.Config.AccessEnded` identifies the target and rejected session so the credential owner can forget a matching sign-in without affecting a newer one. [Shared access ADR](adr/0009-shared-access.md)

### Device states

Settings, Notifications, `THIS DEVICE`: the platform's `notifications` with `channel: "native"` (copy in [DESIGN.md](../DESIGN.md#settings-notifications)).

| State | When | Actions |
| --- | --- | --- |
| `unsupported` | no OS notification service (checked once per run; on macOS any unpackaged binary) | none |
| `off` | turned off on this device | Enable notifications |
| `blocked` | macOS refused the last show (`ErrNotificationsDenied`) | Open System Settings, Try again |
| `on` | otherwise | Send test, Disable, System settings |

- **Enable** (also Try again) turns the toggle on and shows "Notifications are on" at the click; on macOS the first show asks for permission. A denial sets `blocked`; any successful show clears it. Linux has no denial signal: a failed show is only logged, so Linux never reports `blocked`.
- **Send test** shows "Test notification" locally (no server push), even with the window in front; its click opens Settings.
- **Disable** closes shown notifications. The stream keeps running for the badge and the cursor.
- **Open System Settings** (and the ghost System settings): macOS 13+ opens Stage's own pane (`x-apple.systempreferences:com.apple.Notifications-Settings.extension?id=<bundle id>`), macOS 12 the Notifications pane. Linux, best effort by `XDG_CURRENT_DESKTOP`: GNOME, Unity or Ubuntu `gnome-control-center notifications`, KDE `systemsettings kcm_notifications` (`systemsettings5` on Plasma 5), only when on `PATH`; otherwise the page shows "Allow notifications for Stage in your desktop's settings."

### Preferences file

`<userData>/notifications.json` (`~/.config/<appIdentifier>` on Linux, `~/Library/Application Support/<appIdentifier>` on macOS; `mygo dev` appends `.dev`): device state only, the inbox and read state stay on each server. Mode 0600, written atomically (temp file, fsync, rename); live cursor moves are debounced 1 s.

```json
{"version":2,"enabled":true,"denied":false,"servers":{"local":{"cursor":123,"shown":["local/task-7"]},"s-1a2b3c4d":{"cursor":40,"shown":[]}}}
```

| Field | Meaning |
| --- | --- |
| `enabled` | the device toggle (default `true`) |
| `denied` | the OS refused the last show (`blocked`) |
| `servers.<id>.sessionScope` | fingerprint of the shared sign-in and target, used to resume only that sign-in's cursor |
| `servers.<id>.cursor` | that server's newest notification id handled; absent before its first connect |
| `servers.<id>.shown` | its IDs shown and not closed, oldest first, at most 50, so a later run can close stale ones |

Missing: defaults (on, no cursors). Version 1 (`{"version":1,"enabled":true,"cursor":123,"denied":false,"shown":["task-7"]}`, one server) migrates to `servers.local` with its IDs unchanged (an earlier run's `task-7` still closes; a click on it opens this machine's server), and is written as version 2 at the next save. Corrupt or another version: logged, defaults, replaced on the next save.

### Lifecycle

- Window close: hidden; notifications continue.
- Quit: flushes the cursor, then the stream and notifications stop. The service and the inbox keep running; the next launch catches up.
- Crash or kill: up to 1 s of cursor moves may be lost, so the next launch may re-show at most the capped catch-up. No exactly-once delivery.

### Platform limits (MyGo)

- macOS: notifications need a packaged bundle (`mygo dev` or `mygo build`); permission and the settings pane follow the bundle id (the app identifier, `.dev` under `mygo dev`).
- Linux: `org.freedesktop.Notifications` over D-Bus, without a desktop-entry hint. Close and replace reach only this run's notifications (earlier runs' stay until dismissed); no clicks after the app exits; no denial signal.

## Login startup

Settings, General, Info, "Open Stage at login" turns on both MyGo `SetOpenAtLogin` (XDG autostart on Linux, a login item on macOS 13+, a launch agent on macOS 12) and `stage service enable` (while this machine is a worker, `stage worker service enable`); off turns both off. A login launch starts the app resident without a window and ensures the service.

## Links

| Link | Desktop behavior |
| --- | --- |
| In-app routes | stay in the webview (`mygo://localhost`) |
| `http(s)` | the default browser |
| `t3code://threads/<env>/<thread>` | the OS (T3 app); if no handler opens it, the T3 web URL (`/api/harnesses/t3/status` `details.publicUrl` + `/<env>/<thread>`) in the browser |
| `<identity>://tasks/<n>`, `trees/<n>`, `schedules/<n>`, `projects/<n>`, `connections/<c>/sessions/<id>` | shows the app at that page; a task or tree link keeps a valid `?entry=<n>` (that journal entry), other queries and fragments are dropped; a session page opens its owning task or says it is not tracked |
| attachment images (`/api/attachments/<id>`, new tab) | a child window without bindings |
| anything else | refused and logged |

- **Copied references** (Copy link, URL, `Alt+C`): `STAGE_PUBLIC_URL` + path when the server has one (`/api/health` `publicUrl`), so the link opens anywhere; otherwise the installation's deep link, `stage://tasks/12`, `stage://trees/4`, `stage://schedules/7`, `stage://projects/3` or `stage://tasks/12?entry=45` for a journal note (`stage-dev://` for the developer identity). Never a `mygo://` origin. `stage show`, MCP `read_ref`, `GET /api/refs/resolve` and the app's ref chips parse all forms (`server/internal/refs`, `packages/app/src/lib/refs.ts`); task, tree, schedule, project, journal-note and scoped session links render as chips.
- Markdown stays sanitized (DOMPurify); its URI allowlist adds `t3code:` and `stage(-x):`, and bare `stage://` links autolink.
- Only app-owned content calls Go: MyGo trusts only the top frame on `mygo://` (or the dev server), and the navigation lock keeps foreign pages out of the main frame.

## Host differences (restrained)

Kept identical: routes, task operations, needs-input priority, settings, theme, keyboard navigation and the browser/phone design. Desktop-only: native menus and window chrome, `Cmd/Ctrl` accelerators, window persistence, activation, the system clipboard, the connection screen and banner, the desktop lines of Settings, General, Info (with this machine as a worker, [Worker](#worker)), server switching ([Servers](#servers)), native OS notifications and the Dock/launcher badge (both from Go, [Notifications](#notifications)). Settings "This device" controls native notifications through `notifications.channel: "native"`. Go owns badge updates.

## Identities

| | Stable | Developer |
| --- | --- | --- |
| App name | Stage | Stage Dev |
| App identifier | `io.github.flexdinesh.stage.desktop` | `io.github.flexdinesh.stage-dev.desktop` |
| Package slug | `stage-desktop` | `stage-dev-desktop` |
| Bundled CLI identity | `stage` | `stage-dev` |
| Service unit / launchd label | `stage.service` / `io.github.flexdinesh.stage` | `stage-dev.service` / `io.github.flexdinesh.stage-dev` |
| URL scheme | `stage://` | `stage-dev://` |
| Socket, data dir | per identity ([runtime.md](runtime.md#identity-and-paths)) | same |

App identifiers end in `.desktop` because `io.github.flexdinesh.<id>` is the service's launchd label: on macOS 12 MyGo's login agent (`~/Library/LaunchAgents/<appID>.plist`) would overwrite the service's login copy. `mygo dev` appends ` Dev` / `.dev`, which only labels dev runs.

## Develop and package

- `mise run desktop:dev`: builds a `stage-dev` CLI into `.scratch/dev/<branch>/bin`, runs a foreground `stage serve` on a short socket under `$XDG_RUNTIME_DIR/stage-desktopdev/` with data in `.scratch/dev/<branch>/desktop` (`.env.dev`, `T3_FAKE=1` to work without T3), and `go tool mygo dev` with the UI's Vite server on `localhost:5174`, which proxies `/api` to that socket. The dev-only `STAGE_DESKTOP_SOCKET` override skips the bundled CLI and the service (identity and API version come from the dev server's health) and is ignored by `mygo build` builds. No OS service is touched.
- Preview of every connection state in a browser: `pnpm --filter @stage/desktop dev`, then `http://localhost:5174/preview.html?state=failed` (states: `starting-service`, `starting-api`, `failed`, `stopped`, `unsupported`, `misconfigured`, `location`, `conflict`, `incompatible`, `ready`, `ready-skew`, `ready-foreground`, `reconnecting`, `stopped-after`, and for a remote `remote-starting`, `unreachable`, `unreachable-refused`, `remote-ready`, `remote-reconnecting`, and for the worker `worker-join`, `worker-online`, `worker-offline`, `worker-failed`, `worker-removed`, `local-worker`; the fake shell saves this machine, "Studio" and "Lab": switching plays a short connect (Lab stays unreachable) and Test connection of a `.lan` URL answers like a refused hostname).
- Preview the shared account gate with `state=shared-sign-in` and `state=shared-setup`. The Vite proxy is for the personal dev socket. Use the packaged `mygo://localhost` bridge to exercise real desktop sign-in.
- `mise run dist [TARGETS=host|all]`: a `stage-dev` developer build ([release.md](release.md)). It builds the CLI/server (web UI embedded) per target, copies it to `desktop/app/resources/<os>-<arch>/bin/stage` with `LICENSE` (MyGo merges the darwin pair into a universal binary), runs `go tool mygo build` (the `desktop/app/build/` outputs: per Linux arch a tarball with `install.sh`, a `.deb`, an `.rpm`, a `PKGBUILD` and a `.desktop` file (scheme handler); `<Name>.app` with `CFBundleURLTypes` and `LSMinimumSystemVersion 12.0`), then assembles and verifies the assets in `.scratch/dist/<version>`. `mise run release` is the `stage` build of a clean HEAD. The `.dmg` needs macOS. The app icon ships (see [App icon](#app-icon)); the tray uses a generated ink square. Installing and updating: [install.md](install.md) (Stage Dev from a checkout: [development.md](development.md)).
- `STAGE_DESKTOP_SELFCHECK=1 <app executable>` prints how the packaged app resolves its bundled CLI (`<resources>/bin/stage`, its `version --json`) and exits before the single-instance lock, windows or the service; release verification runs it.
- `mise run check` includes `check:desktop` (Go vet, unit tests, the headless integration test); the UI package's tests, typecheck and lint run in `check:web`.
- `mise run desktop:e2e`: the webview e2e (a display and WebKitGTK 4.1, or macOS).

### App icon

The brand mark `apps/web/public/logo.svg`, unchanged (DESIGN.md, Brand mark): `desktop/app/icon/icon.png` (1024x1024, MyGo `icon`) and `desktop/app/icon/icon.icns` (`macos.icon`, PNG entries ic11 32, ic12 64, ic07 128, ic13/ic08 256, ic14/ic09 512, ic10 1024; no icp4/icp5, as MyGo's own renderer). Both are checked in outside `resources/`, which each build recreates. The Linux tarball and packages carry it as `<slug>.png` beside `<slug>.desktop` (`Icon=<slug>`); the `.app` gets it as `AppIcon.icns`. macOS does not mask an `.icns`, so the Dock shows the square tile. Regenerate from the repository root after changing `logo.svg`:

```sh
rsvg-convert -w 1024 -h 1024 apps/web/public/logo.svg -o desktop/app/icon/icon.png
python3 -c 'import struct,subprocess as s,sys; e=b"".join(t.encode()+struct.pack(">I",len(p)+8)+p for t,n in [("ic11",32),("ic12",64),("ic07",128),("ic13",256),("ic08",256),("ic14",512),("ic09",512),("ic10",1024)] for p in [s.run(["rsvg-convert","-w",str(n),"-h",str(n),sys.argv[1]],capture_output=True,check=True).stdout]); open(sys.argv[2],"wb").write(b"icns"+struct.pack(">I",len(e)+8)+e)' apps/web/public/logo.svg desktop/app/icon/icon.icns
```

## Verification

See the PR and the plan's execution notes for the latest run. Covered without a webview: bridge policy and streaming (incl. a remote over TCP: Host, stripped `Cookie`/`Origin`/`Sec-Fetch-*`, the bridge's own page-origin check, switch cancels open streams, 503 mapping), the saved server list, launcher remote phases, switching and per-server epochs, launcher states against fake CLIs, URL policy (incl. notification click paths), the `alerts` unit tests against a scripted stream on a temp socket (preferences, first run, cursor and reset, reconnect and backoff, 404, catch-up, replace, foreground, off with badge, denial and recovery, cleanup, click targets, Linux settings command), a full local workflow through the bridge against a real `stage serve` with fake T3 and isolated data (create, start, read, stop, upload, attachment, notification stream, reconnect after a server restart, cancellation, deep link parsing by the CLI, zero TCP listeners), the notification stream integration test (`itest/notifications_test.go`: a real `stage serve`, the real `alerts` service and a recording sink; first run silent, live show and target, foreground, read elsewhere closes, global filter, reopen catch-up, 15 min window, server restart, database reset), multi-server notifications (two servers, badge sum, id collisions, version 1 migration), and `itest/remote_test.go` (`TestDesktopRemote`: a real `stage serve --listen 127.0.0.1:<port>` on its own temp data dir next to a local one; switching both ways, unsafe methods over TCP through the bridge, a stream cut by a switch, notifications from both, Test connection incl. a refused hostname, remote down and back), the worker launcher against fake CLIs (`launch/worker_test.go`: the worker phase without any local socket or service call, no local probe with a remote active, public URL match, join and its undo paths, leave incl. `--local` and the local server starting again, restart, login startup, status mapping), and `itest/worker_test.go` (`TestDesktopWorker`: a real `stage serve --listen 127.0.0.1:<port>` with `STAGE_PUBLIC_URL` as the remote, a real pairing, `stage worker join`, a real foreground `stage worker run` standing in for the worker service (no systemd in the isolated environment), online, offline, restart, the main server's view of the machine, and `stage worker leave` removing it). Not verified here: the webview e2e, real systemd or launchd from the desktop, tray, Dock, Wayland vs X11, real OS notifications, and a real Mac.

### Manual notification checks

The headless `itest/shared_test.go` drives the bridge against a real isolated
shared Stage server, including its authenticated notification stream.
`itest/shared_stream_test.go` uses a scripted upstream to verify expiry,
per-server cursor cleanup and badge isolation at deterministic session boundaries.

Build with `mise run dist` (`TARGETS=all` for every platform) and install or open the result from `desktop/app/build/` (or the assets in `.scratch/dist/<version>`). A fresh profile means Quit Stage Dev, then delete `notifications.json` from `~/.config/io.github.flexdinesh.stage-dev.desktop/` (Linux) or `~/Library/Application Support/io.github.flexdinesh.stage-dev.desktop/` (macOS); macOS keeps its permission answer per bundle id. Empty cells: not run.

| # | Check | Linux X11 | Linux Wayland | macOS packaged |
| --- | --- | --- | --- | --- |
| 1 | Fresh profile: state on; the first real notification shows (macOS asks for permission first) | | | |
| 2 | Deny (macOS): blocked; Open System Settings opens Stage's pane; allow, Try again: on | | | |
| 3 | Send test shows "Test notification", also with the window in front; click opens Settings | | | |
| 4 | Live needs input, window unfocused: notification; click shows its task, with Open in T3 | | | |
| 5 | Window visible and focused: toast only, no OS notification | | | |
| 6 | Newer event of the same task replaces its notification | | | |
| 7 | Window closed: notifications continue; click restores the window at the target | | | |
| 8 | Quit, 4+ events on different tasks, reopen within 15 min: 3 + "N more in Stage", which opens the drawer; events older than 15 min stay silent | | | |
| 9 | Cold click: after Quit, a click relaunches at the target (macOS) | | | |
| 10 | Reading in the browser closes the desktop notification (macOS also an earlier run's) | | | |
| 11 | Opening the drawer closes all | | | |
| 12 | Disable closes shown notifications and shows none; the badge keeps updating | | | |
| 13 | Badge counts needs input (Dock, Unity launcher API dock) and clears at 0 | | | |
| 14 | System settings: GNOME/KDE opens notification settings, else the hint; macOS opens Stage's pane | | | |
| 15 | Restart the `stage-dev` service: stream reconnects, no duplicates, a new event arrives | | | |
| 16 | Window hidden during events: no stale toast pile-up on return | | | |

Task completion uses the shared app review drawer (full width on phones), with cached evidence, dependent impact and optional acceptance note. Accepted history stays available without T3. Moving a completed task back (the State picker's "Change state") preserves history and never reopens a closed tree. A completed task shows one "completed" chip; its execution chip joins only once execution moves on (running again, needs input, failed), and a finished run not completed yet reads "to review". Notifications stay live beside acceptance; API v4 separates these contracts.

Project planning remains available without T3. The shared project form creates
Stage metadata offline. Before launch, select an exact workspace on the
chosen connection. The bridge integration tests read the demo workspace
selector through `GET /api/connections/{connectionId}/workspaces` after
inventory refresh and launch against isolated fake T3.

Harness navigation uses `Links.executionDestination`. The desktop host chooses native links for a local Stage connection and web links for a remote connection. A resume command (`ExecutionLinks.command`) is never opened; the shared task page offers to copy it. Shared product code does not inspect browser location or choose the platform.

## OpenCode navigation

Open in OpenCode opens the configured web client in the system browser for local and remote Stage servers. Each browser needs to know the service address. On a fresh device, add the browser service URL in OpenCode once. The OpenCode session panel's **Copy** copies the scoped task's external ID. Settings shows the registration hint when the two addresses differ. Browser authentication happens on the destination.
