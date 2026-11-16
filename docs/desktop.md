# Desktop app

The desktop app (`desktop/app`) is a MyGo shell for Linux and macOS that
shows the shared React app (`@stage/app`) in a webview and talks to the
local Stage server over its Unix socket only. The server stays the sole
owner of SQLite, the T3 connection, scheduling and business rules; the
per-user service supervises it ([service.md](service.md)). The desktop owns
windows, OS integration and transport. It never opens `stage.db` and holds
no business logic. Remote machines keep using the browser over TCP.
Decisions: [ADR 0001](adr/0001-desktop-shell.md).

## Layout

| Path | What |
| --- | --- |
| `desktop/app/` | Go module `github.com/flexdinesh/stage/desktop/app` (MyGo v0.9.0). Never imports `server/internal` or the fixture. |
| `bridge/` | Bridge B: the `mygo://localhost` scheme serves the embedded UI (`dist/`, with a CSP header) and reverse-proxies `/api/*` to the socket. |
| `launch/` | Launcher and monitor: ensures the service through the bundled CLI, checks readiness and API version over the socket, watches health. No MyGo import. |
| `nav/` | URL policy: what the webview may load, what goes to the OS, deep links. |
| `main.go`, `shell.go`, `service.go` | Windows, menus, tray, single instance, close/Quit, login startup, the bound `Desktop` service and its events. |
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

While the socket is down, the bridge answers JSON requests 503 (`{code:"starting"|"unavailable"}`) and holds `text/event-stream` requests up to 25 s, then answers an empty `200 text/event-stream` with `retry: 1000`, so the webview's EventSource keeps reconnecting (a 5xx would close it for good, and MyGo turns an aborted handler into a 500).

The app opens no TCP listener for desktop-to-Stage traffic; the integration test asserts zero TCP listeners for the server and the desktop process.

## Connection states

The shell publishes one `HostState` (`app/src/platform/index.ts`, mirrored by `launch.State`) through the `host:state` event.

| Phase | Cause | Shown | Primary action |
| --- | --- | --- | --- |
| `starting` | ensuring the service, waiting for health | connection screen after 400 ms (no flash on a fast start), steps service / server / api | none |
| `ready` | healthy, same API | the app | none |
| `reconnecting` | was ready, stopped answering | banner over the page | Retry now |
| `stopped` | not running after 30 s, or stopped | screen before the first ready, banner after | Start Stage server |
| `failed` | `start` exit 1, crash loop, unhealthy | same, with the log tail | Start Stage server |
| `unsupported` | exit 6: no usable service manager | screen with the cause and a foreground command | Retry |
| `misconfigured` | exit 7 (invalid `service.env`, foreign or shadowed definition, masked unit), or the app runs from a temporary location (`/tmp`, `/var/folders`, App Translocation, Trash) | screen with fix and problems | Retry |
| `conflict` | exit 5 and the terminal server cannot be used | screen with fix and command | Retry |
| `incompatible` | another API version | blocking screen, also after the app ran | Restart Stage server |

- **Terminal server (exit 5).** When a foreground `stage serve` holds the data dir and its socket answers health with the same identity and API version, the desktop uses it and shows a quiet banner: "Connected to a Stage server started from a terminal (pid N). It stops when that terminal does." A default foreground server listens on TCP only, so this usually lands on `conflict`, whose command adds a socket (`stage serve --listen unix,127.0.0.1:7380`).
- **Version skew.** A server build different from the bundled CLI shows a dismissible banner with "Restart Stage server" (`stage service restart --exe <cli> --wait 30s --json`).
- **T3.** Never blocks startup. T3 offline or unpaired uses the app's existing T3 status control and Settings pairing.
- **Diagnostics.** "Show details" lists the server log tail; "Copy diagnostics" (screen, Settings, Help menu) copies app, CLI and server versions, the identity, `service status --json` without secrets and the last 50 log lines.

## Window, close and Quit

- Closing the last window hides it; the app stays resident (reachable from the Dock, the tray, the launcher or a second launch). The next task adds native notifications to this resident app.
- Explicit Quit (`Cmd/Ctrl+Q`, menu, tray) stops the desktop app only. The Stage service keeps running.
- Linux: the first close shows a one-time dialog, "Stage keeps running. Reopen it from the tray or your app launcher.", with Keep running and Quit Stage. It shows whether or not a tray exists, because a missing tray host (GNOME without AppIndicator) is undetectable.
- Tray (where available): Show Stage, Quit Stage. No notification items until notifications ship.
- Window size, position and maximized state persist (`StateKey "main"`). Minimum 720x520.
- Menus: app menu (macOS) or File with Quit (Linux), Edit (so copy and paste work on macOS), View (Reload `Cmd/Ctrl+R`, zoom), Window (Minimize, Close `Cmd/Ctrl+W`), Help (Copy Diagnostics, Show Logs). The page keeps its own shortcuts (`Ctrl/Cmd+K`, `N`).
- MyGo's own folders (window state, cache, logs) use the app identifier, never the Stage data dir (`~/Library/Application Support/Stage` would collide with `.../stage` on case-insensitive APFS).

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

Kept identical: routes, task operations, needs-input priority, settings, theme, keyboard navigation and the browser/phone design. Desktop-only: native menus and window chrome, `Cmd/Ctrl` accelerators, window persistence, activation, the system clipboard, the connection screen and banner, and the Settings "Desktop app" panel. No service worker, no Web Push, no notification controls (the platform's `notifications` is absent; Settings shows only the global event filter). No Dock badge yet (notifications task).

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
- Preview of every connection state in a browser: `pnpm --filter stage-desktop dev`, then `http://localhost:5174/preview.html?state=failed` (states: `starting-service`, `starting-api`, `failed`, `stopped`, `unsupported`, `misconfigured`, `location`, `conflict`, `incompatible`, `ready`, `ready-skew`, `ready-foreground`, `reconnecting`, `stopped-after`).
- `mise run desktop:package [IDENTITY=stage|stage-dev]` (default `stage-dev`): cross-builds the CLI with the identity and version into `desktop/app/resources/<os>-<arch>/bin/stage` (MyGo merges the darwin pair into a universal binary), then `go tool mygo build -platform linux/amd64,linux/arm64,darwin/universal` into `desktop/app/build/`: a `.deb`, a tarball with `install.sh` and a `.desktop` file (scheme handler) per Linux arch, and `<Name>.app` (`CFBundleURLTypes`, `LSMinimumSystemVersion 12.0`). The `.dmg` needs macOS. No app icon ships yet (the `.desktop` file names `Icon=<name>`; the tray uses a generated ink square). Installing is a later task.
- `mise run check` includes `check:desktop` (Go vet, unit tests, the headless integration test); the UI package's tests, typecheck and lint run in `check:web`.
- `mise run desktop:e2e`: the webview e2e (a display and WebKitGTK 4.1, or macOS).

## Verification

See the PR and the plan's execution notes for the latest run. Covered without a webview: bridge policy and streaming, launcher states against fake CLIs, URL policy, and a full local workflow through the bridge against a real `stage serve` with fake T3 and isolated data (create, start, read, answer, upload, attachment, live transcript, reconnect after a server restart, cancellation, deep link parsing by the CLI, zero TCP listeners). Not verified here: the webview e2e, real systemd or launchd from the desktop, tray, Dock, Wayland vs X11, and a real Mac.
