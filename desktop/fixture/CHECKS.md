# Desktop fixture checks

The fixture proves the desktop shell's framework and transport choices
([ADR 0001](../../docs/adr/0001-desktop-shell.md)). It is a separate Go
module with its own probe page; nothing in `server/` or `web/` imports it.
It uses MyGo v0.9.0, which is pinned.

| Path | What |
| --- | --- |
| `bridge/` | Both candidate bridges. A: a Unix-socket `http.Client` plus an `Allow` policy for `fetch.New`. B: a scheme handler that reverse-proxies `/api/*` to the socket. Plain `net/http`, unit-tested. |
| `stub/`, `cmd/stagestub` | A socket-only copy of Stage's wire contract: JSON, uploads, attachments, SSE. `/api/fixture/*` drives and observes it. |
| `main.go`, `service.go` | The MyGo app: bridge selection, single instance, close-to-hide, Quit, notification, tray. |
| `web/` | Probe page (TS + Vite). It runs the check suite and publishes `window.stageFixture`. |
| `e2e/` | Runner over MyGo's automation socket (`-tags e2e`). |

## Automated

### `mise run check` (any machine)

`check:fixture` runs the fixture's Go tests. No webview needed. It covers:
- the request policy;
- the Unix client: JSON, multipart, proxy env ignored, redirects refused, host ignored;
- the scheme proxy: routing, upload and attachment headers, SSE flushed per event, reconnect with `Last-Event-ID`, reset, cancellation reaching the server;
- the stub contract;
- no TCP listener in the process (checked against `/proc` on Linux, `lsof` on macOS).

### `mise run fixture:e2e` (Linux desktop session with WebKitGTK 4.1, or macOS)

The task builds the probe page, then runs `go test -tags e2e ./e2e`. For each bridge (`scheme`, `fetch`) it starts the stub on a temp socket and launches the app with every proxy variable pointing at a dead port. It then asserts:

1. Every probe check passes:
   - **`json-crud`**: create, list, update (UTF-8 body) and delete a snippet, then 404.
   - **`upload-image`**: a canvas PNG through `FormData` gets 201; a non-image gets 400; 10 MB + 1 byte gets 400.
   - **`image-render`**: the upload shows in an `<img>` and decodes at 4x3. B uses a direct `/api/attachments/:id`; A uses a blob URL.
   - **`download`**: the body and `Content-Disposition` are readable.
   - **`routing`**: `pushState` to a deep link works, and fetching `/tasks/42` from the scheme returns `index.html`.
   - **`sse-live`**: 5 events arrive in order, each within 1 s of being emitted. B uses native `EventSource`; A uses `web/src/sse.ts` over the plugin.
   - **`sse-reconnect`**: after the server drops the stream it reconnects with `Last-Event-ID`; ids 0..3 arrive exactly once.
   - **`sse-reset`**: a `reset` event closes the stream for good.
   - **`sse-cancel`**: closing the stream ends it on the server.
   - **`fetch-abort`**: `AbortController` ends the request on the server.
   - **`policy-scheme`** (B only): a redirect gets 502; `PROPFIND` gets 405; `/mcp` never reaches the server.
   - **`policy-fetch`** (A only): the plugin refuses a redirect, `PROPFIND`, a foreign host, a non-`/api` path and https.
   - **`page-sandbox`**: the page's CSP refuses native `fetch` to the network and to `file:`.
2. Neither the app nor the stub listens on TCP.
3. A reload ends the page's open stream on the server.
4. A second launch exits; the first instance shows its window.
5. Closing the window hides it and the app keeps running. The test logs `visibilityState` and the open stream count.
6. Quit exits the app and ends its streams; the stub keeps running and stays healthy.

With `STAGE_FIXTURE_REAL_UI=1`, `TestRealUI` also builds `web/dist` and a real `stage serve`. The server runs on a temp socket with a temp `STAGE_DATA_DIR` and `T3_URL=http://127.0.0.1:9`, so it never reaches the machine's real T3. The real UI loads through bridge B with no web changes and renders. The test then checks `/api/health` through the page, a `/schedules` deep link, and that neither the app nor the server listens on TCP.

Linux setup: install `webkit2gtk-4.1` (Arch) / `libwebkit2gtk-4.1-0` (Debian, Ubuntu) / `webkit2gtk4.1` (Fedora), then run `go tool mygo doctor` in `desktop/fixture`.

## Manual

The automation plugin cannot see native notifications, tray menus or the
Dock, so these are checked by hand.

Before running, start the stub, then the app:
- **Stub:** `go run ./cmd/stagestub -socket /tmp/sf/stage.sock`.
- **App:** in `desktop/fixture`, run `STAGE_FIXTURE_SOCKET=/tmp/sf/stage.sock STAGE_FIXTURE_BRIDGE=scheme go run .` (after `pnpm --dir web build`).
- **macOS packaged build:** run `go tool mygo build -platform darwin/universal` in `desktop/fixture`. This gives `build/darwin-universal/Stage Fixture.app` (checked by cross-building on Linux). Start its executable from a terminal so it keeps the bundle identity and the environment: `STAGE_FIXTURE_SOCKET=… STAGE_FIXTURE_BRIDGE=scheme "build/darwin-universal/Stage Fixture.app/Contents/MacOS/Stage Fixture"`.

| # | Check | Linux X11 | Linux Wayland | macOS packaged |
| --- | --- | --- | --- | --- |
| 1 | Window opens; probe "Run checks" all pass (both bridges) | | | |
| 2 | Renderer: no blank or flickering page. If there is one, note whether `WEBKIT_DISABLE_DMABUF_RENDERER=1` fixes it | | | |
| 3 | Notify: the notification shows, with the app's name and icon (macOS: the first time asks for permission) | | | |
| 4 | Click the notification with the window open: the window is focused | | | |
| 5 | Close the window, choose "Send Test Notification" from the tray or the menu, then click the notification: the window comes back | | | |
| 6 | Close the window: the process stays (`pgrep -f fixture`) | | | |
| 7 | Second launch while running: it exits, and the first window shows and gets focus | | | |
| 8 | Tray icon present (GNOME needs the AppIndicator extension), with working Show and Quit | | | |
| 9 | Menu "Quit Fixture" / Cmd+Q: the app exits; the stub keeps answering `curl --unix-socket /tmp/sf/stage.sock http://stage.invalid/api/health` | | | |
| 10 | Download link: the file is saved, or a save prompt appears. Note what happens | | | |
| 11 | macOS: a click on a notification left in Notification Center after Quit relaunches the app (`OnNotificationClick`) | n/a | n/a | |
| 12 | `GDK_BACKEND=x11` vs Wayland: same results | | | |

Paste results (or failures with logs) into the task. The ADR's
verification table is updated from them.
