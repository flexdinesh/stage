# Architecture: ownership and UI hosts

Stage is one Go server plus one React application that more than one host
can render. Today the only host is the browser (PWA). The MyGo desktop shell
([ADR 0001](adr/0001-desktop-shell.md)) will be a second host and reuse the
same application.

## Ownership

| Path | Owns | Must not |
| --- | --- | --- |
| `server/` (Go module `github.com/flexdinesh/stage/server`) | REST API, `/mcp`, CLI, stdio MCP, SQLite, the T3 connection, scheduling, business rules; embeds the browser host build (`internal/webui`) | Be imported by another module's `internal` path (promote a small public package instead, per ADR 0001) |
| `app/` (`@stage/app`) | The whole UI: routes, pages, components, query/state logic, API client and types (`src/api/types.ts` mirrors [api.md](api.md)), design tokens (`src/index.css`), and the platform contract (`src/platform/index.ts`) | Use browser globals at a platform boundary, import a host or MyGo, or branch on the platform |
| `web/` (`stage-web`) | Browser host: `index.html`, `src/main.tsx` (mount, service worker registration), `src/sw.ts`, `public/` (manifest, icons, favicons), Vite config, browser adapters (`src/platform/`) | Hold product UI or business logic |
| `desktop/fixture/` | MyGo proof (ADR 0001). Separate Go module and its own pnpm lockfile, outside the workspace | Be imported by anything |
| `desktop/app/` | Reserved for the MyGo desktop host (a later task). Not created yet | |

One pnpm workspace (`pnpm-workspace.yaml`: `app`, `web`) with one lockfile at
the root. `@stage/app` ships TypeScript source; the host's Vite build compiles
it. Its exports: `@stage/app` (`StageApp`, `createApi`, `ApiError`, API types),
`@stage/app/platform` (contract types), `@stage/app/index.css` (styles; Tailwind
scans `app/src` only).

## Platform contract

A host renders `<StageApp platform={...} />`. Product code reaches the host
only through `usePlatform()` and `useApi()` (`app/src/platform/context.tsx`).
`app/tests/boundaries.test.mjs` fails if product code uses the browser APIs
below directly or imports MyGo.

| Contract | Boundary | Browser adapter (`web/src/platform/`) |
| --- | --- | --- |
| `transport.request(path, init)` | API requests. Paths are API-relative (`/tasks/1`); `createApi` maps non-2xx to `ApiError` (status, server message, code) | `transport.ts`: relative `fetch("/api" + path)` |
| `transport.subscribe(path, handlers)` | Server-sent events (live transcript). `close()` cancels the request; no handler runs after it. The host owns reconnect and `Last-Event-ID` | `transport.ts`: native `EventSource` |
| `transport.assetUrl(path)` | Image/attachment URLs for `<img>` and links | `transport.ts`: `/api` + path |
| `clipboard.writeText` | Copy link / URL / reference copying | `clipboard.ts`: Clipboard API, `execCommand` fallback over plain HTTP |
| `links.appOrigin()` | Origin inside copied Stage links | `links.ts`: `window.location.origin` |
| `links.openExternal` / `openApp` | Programmatic opens (palette Ctrl+Enter opens `t3code://`). Anchors stay native `<a>`; a desktop host routes them by navigation policy | `links.ts`: `window.open` / `location.assign` |
| `notifications` | System notification delivery: state, enable/disable/test, close shown, cross-device sync, `onDelivered` | `webPush.ts`: service worker + Web Push |
| `attention.setNeedsInput` | Needs-input signal outside the page | `attention.ts`: tab favicon + Badging API |
| `storage` | Per-device preferences (theme, projects sort, history filters). Never throws | `storage.ts`: `localStorage` |

Not part of the contract: plain DOM and layout (`document` visibility,
`matchMedia`, `history`, reload). A webview provides them as a browser does.

## Data flow

- Browser: page and `/api` on the same origin over TCP; `web/vite.config.ts`
  proxies `/api` in dev (to TCP or a `unix:` socket).
- Desktop (later): the shell serves the same build on its own scheme and
  forwards `/api` to the server's Unix socket (ADR 0001, bridge B), so the
  browser transport works unchanged there. A different bridge would only
  need another `Transport`.

## Builds and legacy paths

- `mise run build`: workspace frozen install, `pnpm --filter stage-web build`,
  copy `web/dist` into `server/internal/webui/dist`, `go build`.
- `web/dist` is a fixed path: `mise run build`, the `Dockerfile`
  (`COPY --from=web /src/web/dist/`), and the fixture e2e
  (`desktop/fixture/e2e`, `../../../web/dist`) read it. Moving it breaks all three.
- The `Dockerfile` (used by the compose stack) copies the root workspace
  manifests and lockfile, `app/` and `web/`. A new workspace package needs a
  matching `COPY`.
- The browser theme key `stage-theme` is read before first paint in
  `web/index.html`; keep it in step with `app/src/lib/theme.ts`.
