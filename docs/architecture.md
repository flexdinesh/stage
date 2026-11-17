# Architecture: ownership and UI hosts

Stage is one Go server plus one React application that more than one host
can render: the browser (PWA) and the MyGo desktop app
([ADR 0001](adr/0001-desktop-shell.md), [desktop.md](desktop.md)).

## Ownership

| Path | Owns | Must not |
| --- | --- | --- |
| `server/` (Go module `github.com/flexdinesh/stage/server`) | REST API, `/mcp`, CLI, stdio MCP, SQLite, the T3 connection, scheduling, business rules; embeds the browser host build (`internal/webui`) | Be imported by another module's `internal` path (promote a small public package instead, per ADR 0001) |
| `packages/app/` (`@stage/app`) | The whole UI: routes, pages, components, query/state logic, API client and types (`src/api/types.ts` mirrors [api.md](api.md)), design tokens (`src/index.css`), and the platform contract (`src/platform/index.ts`) | Use browser globals at a platform boundary, import a host or MyGo, or branch on the platform |
| `apps/web/` (`@stage/web`) | Browser host: `index.html`, `src/main.tsx` (mount, service worker registration), `src/sw.ts`, `public/` (manifest, icons, favicons), Vite config, browser adapters (`src/platform/`) | Hold product UI or business logic |
| `desktop/app/` | Desktop host: Go module `github.com/flexdinesh/stage/desktop/app` (MyGo shell, bridge B, launcher, URL policy) and `desktop/app/ui` (`@stage/desktop`, workspace package: mount and desktop adapters) | Import `server/internal` or the fixture; open `stage.db`; hold business logic |
| `apps/website/` (`@stage/website`) | Static marketing site and public user guides on vocs (`renderStrategy: 'full-static'`), illustrative demos (vendored stageshow in `src/vendor/`), its own `PRODUCT.md`, `DESIGN.md` and `.impeccable/`, and the Cloudflare Workers static-assets config (`wrangler.jsonc`, worker `stage-website`, deployed by Workers Builds from `main`: [website.md](website.md)). Tasks: `mise run website:dev` (:5175), `website:build`, `website:check` (also in `mise run check`), `website:preview` (wrangler dev, :8787) | Import `@stage/*` or product UI/runtime internals; be needed by a product build or release (they install with `--filter` and never build it); deploy with product releases |
| `desktop/fixture/` | MyGo proof (ADR 0001). Separate Go module and its own pnpm lockfile, outside the workspace | Be imported by anything |
| `tools/release/`, `scripts/release.sh` | Release builds ([release.md](release.md)): one version and commit for the desktop, its bundled CLI and the headless CLI; the manifest, checksums and verification. `tools/release` is its own Go module (standard library only) | Upload, tag, install or hold product logic |

One pnpm workspace (`pnpm-workspace.yaml`: `apps/web`, `apps/website`, `packages/app`, `desktop/app/ui`) with one lockfile at
the root; it also holds the install-script policy (`allowBuilds`). `@stage/app` ships TypeScript source; the host's Vite build compiles
it. Its exports: `@stage/app` (`StageApp`, `createApi`, `ApiError`, API types),
`@stage/app/platform` (contract types), `@stage/app/index.css` (styles; Tailwind
scans `packages/app/src` only).

## Documentation ownership

Two homes, no shared or generated content between them:

- **Repository docs** (`docs/*.md`) are the engineering contracts: exact behavior, states, paths, exit codes, wire shapes. The REST reference lives only here ([api.md](api.md)).
- **Website user guides** (`apps/website/src/pages/docs`) explain how to install and use stage. Every claim traces to a contract or the code; nothing is described that the product does not do. Landing copy (`apps/website/src/components/landing/copy.ts`) and demo strings follow the same rule.
- **README** is a lean entry point: pitch, install, links. It keeps the "HTTPS and push notifications" section because Settings links to it (`packages/app/src/lib/push.ts`).

| Contract | Website page |
| --- | --- |
| [install.md](install.md), [release.md](release.md) (assets, platforms) | `/docs/install` |
| [features.md](features.md) (T3 setup), [runtime.md](runtime.md) (T3 binding), [api.md](api.md) (T3 pairing) | `/docs/t3` |
| [desktop.md](desktop.md) | `/docs/desktop` |
| [service.md](service.md), [runtime.md](runtime.md) (listeners, origin policy), `.env.example` | `/docs/deploy` (Headless server) |
| [features.md](features.md) | `/docs/features`, `/docs` |
| [api.md](api.md) (MCP, tree, journal, handoff, base rules), CLI (`server/internal/cli`), MCP tools (`server/internal/agenttools`) | `/docs/agents` |

A change that alters user-visible behavior updates its contract and the mapped
website page in the same PR (and the README only when install commands or the
HTTPS section change). A change that touches only internals updates the
contract alone. Website-only changes (copy, layout) need no contract change but
must not add claims the contracts do not support. Removing or renaming a
website page or heading breaks inbound links: update every internal link and
`apps/website/scripts/check-build.mjs`; old URLs 404 (no redirects).

## Platform contract

A host renders `<StageApp platform={...} />`. Product code reaches the host
only through `usePlatform()` and `useApi()` (`packages/app/src/platform/context.tsx`).
Optional parts are capabilities: product code checks for them, never for a
platform.
`packages/app/tests/boundaries.test.mjs` fails if product code uses the browser APIs
below directly or imports MyGo.

| Contract | Boundary | Browser adapter (`apps/web/src/platform/`) | Desktop adapter (`desktop/app/ui/src/platform/`) |
| --- | --- | --- | --- |
| `transport.request(path, init)` | API requests. Paths are API-relative (`/tasks/1`); `createApi` maps non-2xx to `ApiError` (status, server message, code) | `transport.ts`: relative `fetch("/api" + path)` | `transport.ts`: the same, over bridge B |
| `transport.subscribe(path, handlers)` | Server-sent events (notification inbox). `close()` cancels the request; no handler runs after it. The host owns reconnect and `Last-Event-ID` | `transport.ts`: native `EventSource` | same (the bridge aborts dropped streams as network errors, so they retry) |
| `transport.assetUrl(path)` | Image/attachment URLs for `<img>` and links | `transport.ts`: `/api` + path | same |
| `clipboard.writeText` | Copy link / URL / reference copying | `clipboard.ts`: Clipboard API, `execCommand` fallback over plain HTTP | `clipboard.ts`: the shell's system clipboard |
| `links.stageLink(path)` | The copyable Stage link for an app path (`/tasks/12`) | `links.ts`: `window.location.origin` + path | `links.ts`: `STAGE_PUBLIC_URL` + path, else `stage://tasks/12` |
| `links.openExternal` / `openApp` | Programmatic opens (palette Ctrl+Enter opens `t3code://`). Anchors stay native `<a>`; a desktop host routes them by navigation policy | `links.ts`: `window.open` / `location.assign` | the shell's URL policy |
| `notifications?` | System notification delivery: `channel` (`web-push` when absent, `native`), state, enable/disable/test, close shown, cross-device sync, `onDelivered`, `openSystemSettings?`. Absent: Settings shows only the global event filter | `webPush.ts`: service worker + Web Push | `notifications.ts`: `channel: "native"` over the generated MyGo client; the Go process shows OS notifications from the server's notification stream ([desktop.md](desktop.md#notifications)) |
| `attention.setNeedsInput` | Needs-input signal outside the page | `attention.ts`: tab favicon + Badging API | no-op: the Go process owns the Dock/launcher badge |
| `storage` | Per-device preferences (theme, projects sort). Never throws | `storage.ts`: `localStorage` | `storage.ts`: the webview's `localStorage` |
| `host?` | Native shell services: the host-managed server connection (`HostState`, retry/start/restart, diagnostics), login startup, navigation requests (deep links). Drives `HostGate`, `HostBanner` and Settings "Desktop app" (`components/Host.tsx`) | absent | `host.ts` over the generated MyGo client |

Not part of the contract: plain DOM and layout (`document` visibility,
`matchMedia`, `history`, reload). A webview provides them as a browser does.

## Data flow

- Browser: page and `/api` on the same origin over TCP; `apps/web/vite.config.ts`
  proxies `/api` in dev (to TCP or a `unix:` socket).
- Desktop: the shell serves the desktop host build on `mygo://localhost` and
  forwards `/api` to the server's Unix socket (ADR 0001, bridge B), so the
  transport is the browser's. It ensures the per-user service first
  ([desktop.md](desktop.md)).
- Server side: one `stage serve` process serves every listener (TCP and/or
  Unix sockets) with one store and runtime, owns its data dir through a lock,
  and applies the request origin policy to `/api` and `/mcp`
  ([runtime.md](runtime.md)). On a desktop machine an OS per-user service
  (systemd `--user`, launchd) supervises it (`stage service`,
  [service.md](service.md)).

## Builds and legacy paths

- `mise run build`: frozen install filtered to `@stage/web...`, `pnpm --filter @stage/web build`,
  copy `apps/web/dist` into `server/internal/webui/dist`, `go build`.
- `apps/web/dist` is a fixed path: `mise run build`, `scripts/release.sh`, the
  `Dockerfile` (`COPY --from=web /src/apps/web/dist/`), and the fixture e2e
  (`desktop/fixture/e2e`, `../../../apps/web/dist`) read it. Moving it breaks all four.
- The `Dockerfile` (used by the compose stack) copies the root workspace
  manifests and lockfile, `packages/app/` and `apps/web/`, and `desktop/app/ui/package.json`
  (manifest only), then runs a frozen install filtered to `@stage/web...`. A
  workspace package the browser host does not depend on (`apps/website`) needs
  no `COPY` and stays in `.dockerignore`.
- Product builds (`mise run build`, `scripts/release.sh`, the `Dockerfile`,
  `desktop:e2e`) install only product packages with `--filter`; website output
  (`apps/website/dist`) never reaches `server/internal/webui/dist` or a release asset.
- `desktop/app/dist` is the desktop UI build (`pnpm --filter @stage/desktop build`), embedded by the desktop app; it keeps a tracked `.gitkeep`.
- The theme key `stage-theme` is read before first paint in
  `web/index.html` and `desktop/app/ui/public/theme.js`; keep both in step with `app/src/lib/theme.ts`.

T3 synchronization refreshes projects and tracked task sessions, with active external discovery for notifications. The `workevent` package owns shared task/session transition events; the T3 watcher produces them and `notify` delivers them. No historical transcript sweep, question enrichment or automatic PR tracking. Raw retired database columns are preserved for historical records.
