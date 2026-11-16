# Desktop app

The desktop app (`desktop/app`) is a MyGo shell for Linux and macOS that
shows the shared React app (`@stage/app`) in a webview and talks to one
Stage server at a time: this machine's over its Unix socket (the default),
or a saved remote server on a trusted network over TCP ([Servers](#servers)).
Each server stays the sole owner of its SQLite, T3 connection, scheduling
and business rules; this machine's per-user service supervises the local
one ([service.md](service.md)). The desktop owns windows, OS integration
and transport. It never opens `stage.db`, holds no business logic and
never manages a remote server. Browsers keep using TCP.
Decisions: [ADR 0001](adr/0001-desktop-shell.md).

## Layout

| Path | What |
| --- | --- |
| `desktop/app/` | Go module `github.com/flexdinesh/stage/desktop/app` (MyGo v0.9.0). Never imports `server/internal` or the fixture. |
| `bridge/` | Bridge B: the `mygo://localhost` scheme serves the embedded UI (`dist/`, with a CSP header) and reverse-proxies `/api/*` to the active server (socket or TCP). |
| `target/` | The servers: the local target (implicit) and saved remotes (`servers.json`), each one's transport (no HTTP proxy), Host and base URL. No MyGo import. |
| `launch/` | Launcher and monitor: ensures the local service through the bundled CLI, checks readiness and API version (socket or TCP), watches health, switches servers, tests a server URL. No MyGo import. |
| `nav/` | URL policy: what the webview may load, what goes to the OS, deep links, notification click paths. |
| `alerts/` | Native notifications: every saved server's notification stream, catch-up and foreground policy, device preferences (`notifications.json`), cleanup, badge, the OS settings opener. No MyGo import. |
| `main.go`, `shell.go`, `service.go`, `servers.go` | Windows, menus, tray, single instance, close/Quit, login startup, notification clicks, the bound `Desktop` service (server list and switching in `servers.go`) and its events. |
| `notifications.go` | The MyGo side of `alerts`: OS notifications and the Dock/launcher badge. |
| `selfcheck.go` | `STAGE_DESKTOP_SELFCHECK=1`: print how the bundled CLI resolves, then exit (release verification). |
| `mygo.config.ts` | Build config per identity (`IDENTITY=stage` or `stage-dev`). |
| `ui/` | pnpm workspace package `stage-desktop`: the desktop host frontend (mount, adapters, generated `src/mygo.ts`). Builds into `desktop/app/dist`. |
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

The shell publishes one `HostState` (`app/src/platform/index.ts`, mirrored by `launch.State`) through the `host:state` event.

| Phase | Cause | Shown | Primary action |
| --- | --- | --- | --- |
| `starting` | ensuring the service, waiting for health | connection screen after 400 ms (no flash on a fast start, again after each switch), steps service / server / api (remote: server / api) | none |
| `ready` | healthy, same API | the app | none |
| `reconnecting` | was ready, stopped answering | banner over the page | Retry now (remote: and Switch server) |
| `stopped` | not running after 30 s, or stopped | screen before the first ready, banner after | Start Stage server |
| `failed` | `start` exit 1, crash loop, unhealthy | same, with the log tail | Start Stage server |
| `unsupported` | exit 6: no usable service manager | screen with the cause and a foreground command | Retry |
| `misconfigured` | exit 7 (invalid `service.env`, foreign or shadowed definition, masked unit), or the app runs from a temporary location (`/tmp`, `/var/folders`, App Translocation, Trash) | screen with fix and problems | Retry |
| `conflict` | exit 5 and the terminal server cannot be used | screen with fix and command | Retry |
| `incompatible` | another API version | blocking screen, also after the app ran | Restart Stage server (remote: Retry once that machine is updated; Switch server) |
| `unreachable` | remote only: no answer at its URL, or its origin policy refused this app (403); polling continues | screen before the server's first ready (since the switch), banner after | Retry (and Switch server) |

"Before the first ready" counts per server: the page gates on `serverEpoch`, so after a switch the app stays unmounted until the new server answers. The screen and the banner offer "Switch server" (a drawer listing the saved servers) whenever more than one is saved; for a remote it sits next to Retry.

- **Terminal server (exit 5).** When a foreground `stage serve` holds the data dir and its socket answers health with the same identity and API version, the desktop uses it and shows a quiet banner: "Connected to a Stage server started from a terminal (pid N). It stops when that terminal does." A default foreground server listens on TCP only, so this usually lands on `conflict`, whose command adds a socket (`stage serve --listen unix,127.0.0.1:7380`).
- **Version skew.** A server build different from the bundled CLI shows a dismissible banner with "Restart Stage server" (`stage service restart --exe <cli> --wait 30s --json`).
- **T3.** Never blocks startup. T3 offline or unpaired uses the app's existing T3 status control and Settings pairing.
- **Diagnostics.** "Show details" lists the server log tail; "Copy diagnostics" (screen, Settings, Help menu) copies app, CLI and server versions, the identity, `service status --json` without secrets and the last 50 log lines.

## Servers

The desktop connects to one Stage server at a time. Each server is bound to its own T3 backend; the desktop never manages a server's T3 beyond showing that server's onboarding.

- **This machine** (`local`): implicit, always first, labeled "This machine", never edited or removed. Reached over its Unix socket; its service is ensured as in the [launch sequence](#launch-sequence).
- **Remote servers**: saved by name and URL (`http(s)://host[:port]`, no path, credentials, query or fragment), on a **trusted network with no authentication** (as a browser on that network would be). Names and URLs are unique; "This machine" is reserved. The desktop never starts, stops or configures a remote.
- **Remote server setup** (on that machine): it must listen on TCP (`stage serve --listen 0.0.0.0:7380`, or `STAGE_LISTEN` in its `service.env`). A URL with an IP literal or `localhost` passes its Host check; a hostname must be in its `STAGE_ALLOWED_ORIGINS` (or be its `STAGE_PUBLIC_URL`), else it answers 403 ([runtime.md](runtime.md#request-origin-policy)). Test connection and the `unreachable` screen name the setting: `STAGE_ALLOWED_ORIGINS=<url>`.

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

`SwitchServer(id)` (the Settings `SERVERS` panel, the top-bar menu with more than one server, the connection screen, a notification click):

1. The choice is saved as `active`.
2. The bridge is unset at once: every open request and stream to the old server ends (in-flight JSON gets 503 `unavailable`, streams end and EventSource reconnects into the wait), so nothing of the old server reaches the page afterwards.
3. The state starts over for the new server: `starting` (local: steps service / server / api; remote: server / api), `target` set, `server` null, `serverEpoch` 0. A start still running for the old server is cancelled, and its late outcome is dropped.
4. The page sees `target.id` change with that first state: when the app had run on the old server it drops its cached data (`queryClient.clear()`) and opens `/`; a navigation that triggered the switch (a notification click) arrives after it and wins. Until the new server answers the page shows the blank ground, then the connection screen.
5. On ready, the bridge points at the new server, `epoch` and `serverEpoch` increment, and the app mounts.

Switching to the active server only refreshes its name. Editing the active server's URL reconnects it; removing it switches to this machine first.

### Remote differences

- No service commands: Start/Restart server, "Open Stage at login" and the server logs are this machine's only (`ErrRemote` from the shell; the page hides them for a remote). Diagnostics name the remote and skip `service status` and logs.
- Phases: `starting` (server, api), `ready`, `reconnecting`, `unreachable`, `incompatible`. Never `stopped`, `failed`, `unsupported`, `misconfigured` or `conflict`; no foreground banner and no version-skew banner.
- The page shows "Open in app" / "Open in T3" only for this machine's server (a remote's T3 links name that machine's T3).
- Bridge requests carry the remote's Host and no `Cookie`, `Origin` or `Sec-Fetch-*`: the server sees a non-browser client, so its cross-origin checks pass over TCP; the bridge enforces the page origin itself first (a cross-site request, or an unsafe method from another origin, gets 403 without reaching any server).
- `desktop:dev` loads the page from Vite, whose `/api` proxy goes to the dev socket: switching there changes the launcher and notifications, not the page's `/api`. Use a packaged build to try remotes in the UI.
- Settings "Desktop app" names the active server (`Studio · 1.4.0`) and shows its URL instead of the socket.

### Test connection

`TestServer(url)` before saving: `GET /api/health` as the bridge would (8 s bound), returning `{reachable, apiVersion, identity, version, error, hint, command}`. A 403 from the server's origin policy is `reachable` with `command: "STAGE_ALLOWED_ORIGINS=<url>"`; no answer explains `--listen`; another API version is an `error`.

## Window, close and Quit

- Closing the last window hides it; the app stays resident (reachable from the Dock, the tray, the launcher or a second launch), and native notifications continue ([Notifications](#notifications)).
- Explicit Quit (`Cmd/Ctrl+Q`, menu, tray) stops the desktop app and its notifications only. The Stage service keeps running.
- Linux: the first close shows a one-time dialog, "Stage keeps running. Reopen it from the tray or your app launcher.", with Keep running and Quit Stage. It shows whether or not a tray exists, because a missing tray host (GNOME without AppIndicator) is undetectable.
- Tray (where available): Show Stage, Quit Stage. Notification controls live in Settings only.
- Window size, position and maximized state persist (`StateKey "main"`). Minimum 720x520.
- Menus: app menu (macOS) or File with Quit (Linux), Edit (so copy and paste work on macOS), View (Reload `Cmd/Ctrl+R`, zoom), Window (Minimize, Close `Cmd/Ctrl+W`), Help (Copy Diagnostics, Show Logs). The page keeps its own shortcuts (`Ctrl/Cmd+K`, `N`).
- MyGo's own folders (window state, cache, logs) use the app identifier, never the Stage data dir (`~/Library/Application Support/Stage` would collide with `.../stage` on case-insensitive APFS).

## Notifications

Native OS notifications come from the resident Go process (`alerts/`, shown through `notifications.go`), not the webview, so they keep working while the window is closed. The browser keeps Web Push; the desktop webview never uses the web Notification API or Web Push (its permission handler denies every permission request).

- **Source.** `GET /api/notifications/stream` of **every saved server at once** (this machine's over its Unix socket, remotes over TCP, [api.md](api.md#get-apinotificationsstream)), each with `Last-Event-ID` set to its own stored cursor. Every launcher ready (first start, recovery, a new server: each `epoch`) reconnects that server's stream; with a remote active, this machine's socket comes from `stage service status --json` (read only, nothing started). A dropped stream retries with jittered backoff, 1 s to 30 s. A server without the stream (404, older than the app) is logged once and waited out until its next ready. Adding a server opens its stream; removing one closes its notifications and forgets its cursor.
- **What shows.** The global event filter (Settings, `EVENTS · all devices`) decides on the server which events become inbox rows, for every device. The desktop's own on/off is per installation (`notifications.json`), on by default. Only unread rows show.
- **Foreground.** Window visible and focused on the row's server: no OS notification; the page's toasts show the row (same as the PWA). Otherwise (window hidden or unfocused, or another server's row) an OS notification. Each delivery of the active server (a live unread row, or a catch-up batch), shown or not, fires `notifications:delivered`, so the page refetches its inbox at once.
- **Content.** Title, body and target are the row's Web Push payload (`notify.PayloadFor`, one server function): title Needs input, Failed, Ready, Done or Settled, body `<title> · <project>`. With more than one server saved, the title starts with the server's name (`Studio · Needs input`). The notification ID is `<server id>/<group tag>` (`local/task-12`, `s-1a2b3c4d/thread-<id>`; `<server id>/notification-<id>` for a row without one), so a newer event of a task or thread replaces its shown notification, and the same task number on two servers never collides.
- **Clicks.** Switch to the notification's server when another one is active ([Switching](#switching); a removed server opens `/`), then show the window at the target: the row's URL when `nav.AlertPath` allows it (`/tasks/<n>` or `/sessions/<id>`, each optionally with `#answer`; `/settings`; `/?inbox`), else the path derived from the ID, else `/`. Needs input opens the Needs input panel (`#answer`); the click path bypasses `nav.DeepLink`, which drops fragments. macOS: clicking a notification after Quit relaunches the app at its target (`OnNotificationClick` is registered before `Run`). Linux: clicks work only while the app runs.
- **Catch-up** (each connect of each server, after the replay). Rows up to the server's `latestId` at connect show only when unread, newer than the cursor and at most 15 min old: the newest per group (rows without one stay silent), at most 3, oldest first. The rest collapse into one "N more in Stage" ("Open notifications") that opens the inbox drawer (`/?inbox`). Older rows stay silent: inbox and badge only. First run (no cursor) and a reset or smaller database (`reset`, or `latestId` below the cursor) move the cursor to `latestId` silently. With the window in front or notifications off nothing shows; the cursor still advances.
- **Cleanup.** Each server's `unread` snapshot closes that server's shown notifications whose group has no unread rows (read on any device) and its summary once none of its groups is unread; the first snapshot also closes earlier runs' notifications listed in its `shown` (macOS; a no-op on Linux). The page closes a task's or session's notification of the active server when its detail opens (`closeShown`), reconciles on focus and on each delivery (`syncShown`), and opening the drawer closes all of the active server's.
- **Badge.** The sum of every server's snapshot `badge` (the needs-input count, `GET /api/badge` `needsInput`) on the Dock icon (macOS) or the launcher entry (Linux docks that read the Unity launcher API), set by Go when it changes, also while notifications are off. Each server's own count is in the server list (`needsInput`, `servers:changed`). The page's `attention` is a no-op on desktop: Go is the only badge writer.
- **Delivery.** One worker shows queued notifications in order, so a slow show never stalls the stream; a newer note for a queued ID replaces it. One show may wait up to 2 min (the first macOS prompt). A note whose group was read while it showed is closed at once.

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

Settings, Desktop app, "Open Stage at login" turns on both MyGo `SetOpenAtLogin` (XDG autostart on Linux, a login item on macOS 13+, a launch agent on macOS 12) and `stage service enable`; off turns both off. A login launch starts the app resident without a window and ensures the service.

## Links

| Link | Desktop behavior |
| --- | --- |
| In-app routes | stay in the webview (`mygo://localhost`) |
| `http(s)` | the default browser |
| `t3code://threads/<env>/<thread>` | the OS (T3 app); if no handler opens it, the T3 web URL (`/api/t3/status` `publicUrl` + `/<env>/<thread>`) in the browser |
| `<identity>://tasks/<n>`, `<identity>://sessions/<id>` | shows the app at that page |
| attachment images (`/api/attachments/<id>`, new tab) | a child window without bindings |
| anything else | refused and logged |

- **Copied references** (Copy link, URL, `Alt+C`): `STAGE_PUBLIC_URL` + path when the server has one (`/api/health` `publicUrl`), so the link opens anywhere; otherwise the installation's deep link, `stage://tasks/12` (`stage-dev://` for the developer identity). Never a `mygo://` origin. `stage show`, MCP `read_ref`, `GET /api/refs/resolve` and the app's ref chips parse all forms (`server/internal/refs`, `app/src/lib/refs.ts`).
- Markdown stays sanitized (DOMPurify); its URI allowlist adds `t3code:` and `stage(-x):`, and bare `stage://` links autolink.
- Only app-owned content calls Go: MyGo trusts only the top frame on `mygo://` (or the dev server), and the navigation lock keeps foreign pages out of the main frame.

## Host differences (restrained)

Kept identical: routes, task operations, needs-input priority, settings, theme, keyboard navigation and the browser/phone design. Desktop-only: native menus and window chrome, `Cmd/Ctrl` accelerators, window persistence, activation, the system clipboard, the connection screen and banner, the Settings "Desktop app" panel, server switching ([Servers](#servers): Settings `SERVERS`, the top-bar server menu with more than one server), native OS notifications and the Dock/launcher badge (both from Go, [Notifications](#notifications)). No service worker and no Web Push: Settings "This device" controls the native notifications (the platform's `notifications` has `channel: "native"`), and the page's `attention` is a no-op.

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
- Preview of every connection state in a browser: `pnpm --filter stage-desktop dev`, then `http://localhost:5174/preview.html?state=failed` (states: `starting-service`, `starting-api`, `failed`, `stopped`, `unsupported`, `misconfigured`, `location`, `conflict`, `incompatible`, `ready`, `ready-skew`, `ready-foreground`, `reconnecting`, `stopped-after`, and for a remote `remote-starting`, `unreachable`, `unreachable-refused`, `remote-ready`, `remote-reconnecting`; the fake shell saves this machine, "Studio" and "Lab": switching plays a short connect (Lab stays unreachable) and Test connection of a `.lan` URL answers like a refused hostname).
- `mise run dist [TARGETS=host|all]`: a `stage-dev` developer build ([release.md](release.md)). It builds the CLI/server (web UI embedded) per target, copies it to `desktop/app/resources/<os>-<arch>/bin/stage` with `LICENSE` (MyGo merges the darwin pair into a universal binary), runs `go tool mygo build` (the `desktop/app/build/` outputs: per Linux arch a tarball with `install.sh`, a `.deb`, an `.rpm`, a `PKGBUILD` and a `.desktop` file (scheme handler); `<Name>.app` with `CFBundleURLTypes` and `LSMinimumSystemVersion 12.0`), then assembles and verifies the assets in `.scratch/dist/<version>`. `mise run release VERSION=X.Y.Z` is the `stage` build of a clean HEAD. The `.dmg` needs macOS. The app icon ships (see [App icon](#app-icon)); the tray uses a generated ink square. Installing and updating: [install.md](install.md) (Stage Dev from a checkout: [development.md](development.md)).
- `STAGE_DESKTOP_SELFCHECK=1 <app executable>` prints how the packaged app resolves its bundled CLI (`<resources>/bin/stage`, its `version --json`) and exits before the single-instance lock, windows or the service; release verification runs it.
- `mise run check` includes `check:desktop` (Go vet, unit tests, the headless integration test); the UI package's tests, typecheck and lint run in `check:web`.
- `mise run desktop:e2e`: the webview e2e (a display and WebKitGTK 4.1, or macOS).

### App icon

The brand mark `web/public/logo.svg`, unchanged (DESIGN.md, Brand mark): `desktop/app/icon/icon.png` (1024x1024, MyGo `icon`) and `desktop/app/icon/icon.icns` (`macos.icon`, PNG entries ic11 32, ic12 64, ic07 128, ic13/ic08 256, ic14/ic09 512, ic10 1024; no icp4/icp5, as MyGo's own renderer). Both are checked in outside `resources/`, which each build recreates. The Linux tarball and packages carry it as `<slug>.png` beside `<slug>.desktop` (`Icon=<slug>`); the `.app` gets it as `AppIcon.icns`. macOS does not mask an `.icns`, so the Dock shows the square tile. Regenerate from the repository root after changing `logo.svg`:

```sh
rsvg-convert -w 1024 -h 1024 web/public/logo.svg -o desktop/app/icon/icon.png
python3 -c 'import struct,subprocess as s,sys; e=b"".join(t.encode()+struct.pack(">I",len(p)+8)+p for t,n in [("ic11",32),("ic12",64),("ic07",128),("ic13",256),("ic08",256),("ic14",512),("ic09",512),("ic10",1024)] for p in [s.run(["rsvg-convert","-w",str(n),"-h",str(n),sys.argv[1]],capture_output=True,check=True).stdout]); open(sys.argv[2],"wb").write(b"icns"+struct.pack(">I",len(e)+8)+e)' web/public/logo.svg desktop/app/icon/icon.icns
```

## Verification

See the PR and the plan's execution notes for the latest run. Covered without a webview: bridge policy and streaming (incl. a remote over TCP: Host, stripped `Cookie`/`Origin`/`Sec-Fetch-*`, the bridge's own page-origin check, switch cancels open streams, 503 mapping), the saved server list, launcher remote phases, switching and per-server epochs, launcher states against fake CLIs, URL policy (incl. notification click paths), the `alerts` unit tests against a scripted stream on a temp socket (preferences, first run, cursor and reset, reconnect and backoff, 404, catch-up, replace, foreground, off with badge, denial and recovery, cleanup, click targets, Linux settings command), a full local workflow through the bridge against a real `stage serve` with fake T3 and isolated data (create, start, read, answer, upload, attachment, live transcript, reconnect after a server restart, cancellation, deep link parsing by the CLI, zero TCP listeners), the notification stream integration test (`itest/notifications_test.go`: a real `stage serve`, the real `alerts` service and a recording sink; first run silent, live show and target, foreground, read elsewhere closes, global filter, reopen catch-up, 15 min window, server restart, database reset), multi-server notifications (two servers, badge sum, id collisions, version 1 migration), and `itest/remote_test.go` (`TestDesktopRemote`: a real `stage serve --listen 127.0.0.1:<port>` on its own temp data dir next to a local one; switching both ways, unsafe methods over TCP through the bridge, a stream cut by a switch, notifications from both, Test connection incl. a refused hostname, remote down and back). Not verified here: the webview e2e, real systemd or launchd from the desktop, tray, Dock, Wayland vs X11, real OS notifications, and a real Mac.

### Manual notification checks

Build with `mise run dist` (`TARGETS=all` for every platform) and install or open the result from `desktop/app/build/` (or the assets in `.scratch/dist/<version>`). A fresh profile means Quit Stage Dev, then delete `notifications.json` from `~/.config/io.github.flexdinesh.stage-dev.desktop/` (Linux) or `~/Library/Application Support/io.github.flexdinesh.stage-dev.desktop/` (macOS); macOS keeps its permission answer per bundle id. Empty cells: not run.

| # | Check | Linux X11 | Linux Wayland | macOS packaged |
| --- | --- | --- | --- | --- |
| 1 | Fresh profile: state on; the first real notification shows (macOS asks for permission first) | | | |
| 2 | Deny (macOS): blocked; Open System Settings opens Stage's pane; allow, Try again: on | | | |
| 3 | Send test shows "Test notification", also with the window in front; click opens Settings | | | |
| 4 | Live needs input, window unfocused: notification; click shows the session at `#answer` | | | |
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
