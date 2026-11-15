# ADR 0001: Desktop shell on MyGo, UI over a Unix socket

- **Status:** provisional (2026-11-15). It becomes accepted once
  `mise run fixture:e2e` and the manual checks in
  [desktop/fixture/CHECKS.md](../../desktop/fixture/CHECKS.md) pass on Linux
  and macOS. Those results update the verification table below.
- **Evidence:** [desktop/fixture](../../desktop/fixture), an isolated
  module that no production entrypoint imports.

## Context

Stage is a Go server and CLI (`server/`) plus a React SPA (`web/`) that
browsers load over TCP. The approved direction adds a desktop app for Linux
and macOS. It shows the same React UI in a webview and talks to a local
Stage server over a Unix socket. Remote browsers keep using TCP. This record
fixes the framework, the transport and the boundaries before the shipping
app changes.

## Decision

### 1. UI and transport boundary: scheme forwarding (bridge B)

The shell embeds the `web/` build and serves it on its own scheme,
`mygo://localhost`. The same handler reverse-proxies `/api/*` to the
Stage server's Unix socket (`desktop/fixture/bridge`: `Scheme`,
`NewAPIProxy`). The web app keeps relative `fetch`, native `EventSource`
and `<img src=/api/attachments/:id>` unchanged, so the remote browser and
the desktop run the same code.

Both bridges apply one policy (`bridge.Permit`):
- Methods: GET, POST, PUT, PATCH, DELETE only.
- Paths: clean paths under `/api/` only (no `..`, `//`, trailing slash, or encoded `/` or `\`). `/mcp` and everything else stay in the frontend.
- Redirects: refused. The proxy answers 502 instead of letting the webview follow one off the socket.
- Cookies: dropped both ways.
- Proxies: none. `Proxy: nil`, so `HTTP_PROXY` and similar variables are ignored.
- Dialing: the Unix socket only, whatever host a request names.

The page gets a CSP (`connect-src 'self'`, `img-src 'self' blob: data:`),
so it has no ambient network or `file:` access. The packaged local path
opens **no TCP listener**. Tests assert this for the app, the stub and a
real `stage serve` (`procnet`).

**Why B:**
- Smallest surface: one `http.Handler`, zero web changes.
- The webview's own loaders handle streaming, cancellation, images and downloads.
- MyGo documents that custom-scheme handlers get a real `*http.Request` and stream their responses; its SSE limitation is documented for Windows only, which is out of scope.
- Source reading (v0.9.0) shows two more facts:
  - Linux pipes responses to WebKit as `Flush` is called, and cancels the request context when WebKit drops the request.
  - Request bodies are buffered, which is fine within Stage's 10 MB upload cap.

### 2. Development vs production

| | Frontend | API |
| --- | --- | --- |
| Dev (`mygo dev`) | Vite on loopback TCP (`devUrl`) | Vite's `/api` proxy, which `web/vite.config.ts` already points at the server's socket |
| Production | Embedded assets on `mygo://localhost` | Bridge B over the Unix socket; no TCP |

### 3. Ownership

- **Stage server:** sole owner of SQLite, the T3 connection, scheduling and business rules.
- **Supervision:** OS per-user services supervise the server (systemd `--user` on Linux, a launchd LaunchAgent on macOS). It keeps running after the desktop quits. Starting at login is optional. Stage does not manage T3.
- **Desktop:** owns the window, native notifications, single instance and the tray. It never opens `stage.db` and holds no business logic.
- **Close and Quit:**
  - Closing the window hides it; the app stays for notifications.
  - An explicit Quit stops the app and its notifications, never the server.
  - A notification click, a second launch, the tray or the Dock brings the window back.

### 4. Module sharing

- The desktop app is a **separate Go module under `desktop/`**, as the fixture is.
- Go forbids importing `server/internal/...` from another module. When the shell needs server code (listen-address parsing, the socket client), the server promotes a small public package for it (for example `server/stageclient`), which is then a supported API.
- The desktop builds and embeds its own copy of `web/dist` at build time, because `server/internal/webui` is internal. It never imports `server/internal`.
- `go.work` does not lift the internal restriction and is not used.

### 5. Release version compatibility

The desktop app and the server ship from one repository tag, and
`/api/health` will report the server version. If the versions differ, the
desktop shows a non-blocking banner. There is no cross-version compatibility
guarantee.

### 6. Supported platforms (initial)

| OS | CPU | Minimum | Webview | Service manager |
| --- | --- | --- | --- | --- |
| Linux (glibc) | amd64, arm64 | GTK 3 + WebKitGTK 4.1 (deb depends `libgtk-3-0, libwebkit2gtk-4.1-0`) | WebKitGTK | systemd `--user` only |
| macOS | universal (arm64 + amd64) | 12.0 (MyGo default) | WKWebView | launchd LaunchAgent |

- Windows is out of scope (WebView2 buffers custom-scheme responses, so SSE would not stream).
- Packaging is checked from Linux:
  - `go tool mygo build -platform linux/amd64,darwin/universal` produces a `.deb`, a tarball with `install.sh`, a `.desktop` file, and `Stage Fixture.app` (with `CFBundleIdentifier` and `LSMinimumSystemVersion 12.0`).
  - The `.dmg` needs macOS.

### 7. Installation identities

Stable and developer installations are kept separate on five axes: app
identifier, CLI name, service unit, socket and data dir. They never share
state. The concrete names belong to the packaging task. MyGo's own `mygo dev`
appends `.dev` to the bundle identifier; that only labels dev runs and is not
the developer installation.

### 8. MyGo version

- **Pin:** `github.com/egoist/mygo` v0.9.0 (tag commit 4811a28), with npm `@mygo-plugins/fetch` and `mygo-runtime` 0.9.0.
- **Why not 0.9.1:** it was published the same day, and the machine's pnpm `minimumReleaseAge` (3 days) refuses it. Every file the fixture depends on is byte-identical between 0.9.0 and 0.9.1.
- **CLI:** pinned as a Go tool (`go tool mygo`).
- **Config:** JSON (`mygo.json`), so there is no Bun dependency.
- **Bindings:** `mygo generate` writes the typed client (`web/src/mygo.ts`). The app runs headless for this (`MYGO_GENERATE`), so `main` must reach `App.Run` without a socket.

## Alternatives considered

- **A. Fetch plugin.** `fetch.New(fetch.Options{Client: bridge.UnixClient(sock), Allow: bridge.Allow})` with pages requesting `http://stage.invalid/api/*`. It works with the same policy, and the fixture proves it with the same probe suite. Its costs:
  - The web app needs a transport seam for every request.
  - Native `EventSource` cannot use the plugin, so it needs an explicit SSE adapter (`desktop/fixture/web/src/sse.ts`) that reimplements `retry` and `Last-Event-ID`.
  - `<img>` and downloads need blob URLs.
  - Every body crosses IPC as base64 JSON, about 1.33× the size.

  A is the **fallback** if WebKit's `EventSource` or image loading on a custom scheme fails in the e2e run.
- **C. Typed bindings.** Per-endpoint Go methods would duplicate about 60 routes and fork the desktop from the browser path. A generic `Do(method, path, body)` plus a `Channel` stream would reimplement A. Rejected without a prototype.

## Verification

| Evidence | Where | Status |
| --- | --- | --- |
| Policy, Unix client (JSON, multipart, proxy env ignored, redirect refused, host ignored), scheme proxy (routing, upload/attachment headers, SSE flushed per event, `Last-Event-ID` resume, reset, cancel reaches server), stub contract, no TCP listener | `mise run check` (`check:fixture`) | passed on Linux amd64 |
| SSE adapter: chunked/CRLF parsing, reconnect with `Last-Event-ID`, close aborts | `desktop/fixture/web` `pnpm test` | passed (Node 26) |
| App builds: linux amd64/arm64, darwin arm64/amd64, `-tags e2e`; packaged deb/tarball/.app | `go build`, `go tool mygo build` | passed (cross-built on Linux) |
| Webview probe suite, both bridges: JSON, upload, `<img>`, download, routing, SSE live/reconnect/reset/cancel, abort, policy, CSP sandbox; no TCP listener; reload/close/Quit/second instance | `mise run fixture:e2e` | **unverified**: this machine has no display or WebKitGTK; no Mac available |
| Real Stage UI through bridge B against real `stage serve` on a temp socket | `STAGE_FIXTURE_REAL_UI=1 mise run fixture:e2e` | **unverified** (as above); server side checked: `stage serve` on a temp socket answers `/api/health` |
| Notifications and click-to-wake, tray, X11/Wayland, renderer, macOS packaged identity, relaunch from Notification Center | CHECKS.md manual table | **unverified** |

## Known limitations and open risks

- **WebKit `EventSource` on a custom scheme:** undocumented for Linux and macOS. The e2e run decides B vs A.
- **Linux notification clicks after the app exits:** undocumented by MyGo. While the app runs, clicks work through `OnClick`.
- **Tray:** needs AppIndicator. GNOME needs the AppIndicator extension. The app runs without a tray, and the deb does not depend on it.
- **Copied links:** `window.location.origin` is `mygo://localhost`, so copied Stage links (`StageLinks.tsx`) need a configured public URL on desktop.
- **Service worker and Web Push:** not used on desktop; native notifications replace them.
- **External links:** `t3code://` links and `window.location.assign` to external URLs need the shell to route them to the OS (`mygo.Shell`), not navigate the webview.
- **Media:** WebKitGTK cannot play `<video>`/`<audio>` from custom schemes. Stage serves only images.
- **WebKitGTK renderer:** may need `WEBKIT_DISABLE_DMABUF_RENDERER=1` on some GPUs (manual check 2).
- **MyGo maturity:** pre-1.0 with frequent releases. Bump the pin deliberately and rerun the fixture.

## For successors

- The shipping shell reuses the bridge-B handler and policy, the CSP, the close/Quit/notification wiring and the single-instance handover from `desktop/fixture/`. Rewrite them in the real desktop module; do not import the fixture.
- Keep the fixture until the shell has its own e2e coverage. Its checks are the regression suite for MyGo bumps.
- The UI is now `@stage/app` (`app/`) behind a platform contract, with `web/` as the browser host ([architecture.md](../architecture.md)). The desktop host renders `StageApp` with its own adapters (`links.appOrigin` from a configured public URL, native `notifications` and `attention`); bridge B lets it reuse the browser `transport` as is.
