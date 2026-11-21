# Architecture: ownership and UI hosts

Stage is one Go server plus one React application that more than one host
can render: the browser (PWA) and the MyGo desktop app
([ADR 0001](adr/0001-desktop-shell.md), [desktop.md](desktop.md)).

Execution contracts and adapter responsibilities: [execution.md](execution.md).
Machines, the worker process and the main/worker split:
[ADR 0002](adr/0002-machines-and-workers.md).

## Ownership

| Path | Owns | Must not |
| --- | --- | --- |
| `server/` (Go module `github.com/flexdinesh/stage/server`) | REST API, `/mcp`, CLI, stdio MCP, SQLite, harness connections, scheduling, business rules; embeds the browser host build (`internal/webui`) | Be imported by another module's `internal` path (promote a small public package instead, per ADR 0001) |
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

T3 synchronization refreshes external workspace discovery and tracked task sessions, with active external discovery for notifications. The `workevent` package owns shared task/session transition events; the T3 watcher produces them and `notify` delivers them. No historical transcript sweep, question enrichment or automatic PR tracking. Raw retired database columns are preserved for historical records.

Stage owns task acceptance separately from observed execution. SQLite stores immutable accepted evidence and idempotent acceptance requests; a launch claim atomically checks current acceptance and pins its inputs. The release runner consumes these claims, and handoff reads only Stage snapshots. See [API v4 acceptance](api.md#user-acceptance-api-v4).

## Users

Hierarchy: User -> Machines -> Harness connections -> Sessions ([ADR 0002](adr/0002-machines-and-workers.md#6-identity)). Migration 0017 adds `users`, seeded with the default user (id 1, `db.DefaultUser`), and an empty `identities(user_id, provider, subject)` table, unique per `(provider, subject)`, for future sign-in. `created_by_user_id` (NOT NULL, default 1, references `users`) is on `projects`, `tasks`, `task_trees`, `journal_entries`, `schedules` and `machines`; existing rows got the default user. The API exposes it as `createdBy` and `GET /api/me` returns the acting user ([api.md](api.md#users)).

There is no sign-in and no auth. Every Go write resolves its creator through one function, `db.ActingUser(ctx)`: the user `db.WithUser` put on the context, else `db.DefaultUser`. Nothing else names the default user. Today no request sets a principal, so writes from the API, MCP, CLI and background jobs (project import, acceptance) attribute to the default user. Sign-in will set the request principal in one API middleware with `db.WithUser`. A task a schedule spawns, on a cron fire or run now, and its initial notes attribute to the schedule's creator (`spawnTask`). Journal events written by SQLite triggers take the column default (the default user). Machine tokens are machine principals, not users.

## Project and session identity

Stage project IDs and metadata are independent of harness data. Migration 0008
adds harness connection slots, versioned workspace bindings, scoped session
identities and immutable launch destinations. Existing IDs, journal bytes,
accepted snapshot JSON and T3 credentials survive. Legacy columns remain adapter
and migration data; new project rows use inert unique markers there, never routing.

A harness connection is one harness instance: a T3 environment (identity: its environment id) or an OpenCode service (identity: its verified address, plus continuity proof that it serves the connection's stored workspaces and sessions). Migration 0012 stamps existing connections in place, so single-instance data keeps its IDs. Migration 0015 adds machines: every connection belongs to one (`machine_id`; one local machine today, a remote machine served by a thin worker later, per [ADR 0002](adr/0002-machines-and-workers.md)), harness kinds are free text validated by the server's harness registry ([execution.md](execution.md#harness-registry)), and the active and identity indexes are per machine. At most one connection per harness kind is active on a machine, and every "active connection of a kind" lookup names its machine (`db.Store.ActiveID(machine, kind)`, `SwitchInstance(ctx, machine, ...)`; a retired connection revives only on its own machine). Harness setup is per machine too: migration 0018 keys `t3_connection` (T3 token, URL, sync bookkeeping) and `opencode_settings` by `machine_id`, moving the former single rows to machine 1 in place; setup code uses `db.Store.OnMachine(id)` and `harness.Connectors` holds one connector per machine and kind. The server configures one adapter per active connection (`sync.Service.SwapAdapter` moves it when the instance changes) and runs one release and one schedule runner per active connection. Replacing an instance retires its connection: tasks, sessions, bindings and the T3 mirror stay on it as history, never polled, linked or launched on, and Stage refuses new work there (409 `connection_retired`). Unlaunched backlog tasks and schedules follow the active connection. Bindings move only through project import (below) or the user. The same instance again revives its connection; Forget clears a retired connection's identity (or deletes it when unreferenced). `db/connections.go` owns these rules (`SwitchInstance`, `ForgetConnection`). Discovery refreshes workspace availability. Binding compares Git common directories and relative subprojects only below operator-declared shared paths, and only between connections on the same machine (shared paths translate within their connection's machine; in the match action a binding on another machine can only suggest by remote; import matches across machines by remote and subproject, below). An unambiguous verified family can bind automatically during the explicit match action. Equal Git remotes (with equal subprojects when both are known) suggest candidates for confirmation. Manual binding and correction remain available.

### Project import

Each active connection's sync pass (`sync.syncConnection`, every 5 minutes and on Sync now) runs `db.Store.ImportWorkspaces` after a successful discovery refresh; failed inventories never import, and GET discovery routes stay read-only for bindings. The adapter judges which workspaces are deliberate projects (`WorkspaceInfo.Importable`): every live T3 project (users add them explicitly); an OpenCode project with a VCS that OpenCode has not archived and that is not its `global` project (plain directories such as home, config or scratch stay manual-bind only). Within one machine a workspace path names one Stage project across harnesses: when exactly one project has an active binding at that exact path on a connection of this machine (bindings on replaced connections count), it gains this connection's binding (`origin: path`) unless it already has one here; several owners are ambiguous and left for manual binding. A path never matches across machines. With no owner at the path, the match key across machines is the workspace's normalized origin remote plus its subproject path inside the repository (`external_projects.repo` and `repo_path`; never the absolute path): when exactly one project has an active binding on another machine whose workspace has the same key, and that project has no active binding on this machine, it gains this connection's binding (`origin: remote`); several such projects, or one already bound on this machine at another path, are ambiguous and left for manual binding. No owner, or no known remote or subproject, creates a project (`origin: discovered`, binding `origin: imported`) titled by the harness (OpenCode: its name, else the last path segment, or `parent/segment` for `main`, `master` or `trunk`). Several external IDs at one path import the most recently active. Unbinding, or rebinding a project away, declines that workspace (`external_projects.declined_at`); a manual bind withdraws the decline. `projects.archive_owner` records who last decided the archive state: any user archive or unarchive owns it for good; otherwise a discovered project with no tasks whose every active binding points at an archived or unavailable workspace is archived, and unarchived when one is live again.

Repository metadata is a cross-machine match key, never proof within one machine. Within a machine only the path (import) and a verified shared Git checkout (the match action) bind; equal remotes there only suggest. Across machines, the normalized origin remote plus the subproject path is the import match key above; a fork has its own origin remote and so its own project, and a workspace with no remote never merges across machines. `WorkspaceInfo.Repo` is the normalized origin remote (`host/owner/name`, credentials and local remotes dropped; `gitmeta.RemoteIdentity`) and `WorkspaceInfo.RepoPath` the slash path from the checkout top to the workspace (`.` at the top, empty when unknown). T3 reports both (`repositoryIdentity.locator` when its remote is `origin`, and `rootPath`; its `origin` field names a fork's parent and is ignored). Stage reads OpenCode's from `.git/config` without a git binary (`gitmeta.Checkout`, walking up to the nearest `.git` and following worktree `gitdir` and `commondir`) only when it can see the path: a local-auth OpenCode service, or a workspace under a declared shared path, never walking above it (`sync.Service.Inventory`). Rows discovered before migration 0019 have no subproject until the next refresh. Projects expose the distinct repos of their active bindings.

Tasks pin connection/project/workspace/binding revision atomically with an attempt.
Unknown outcomes keep the same destination and request ID on retry. A binding
change applies only to a future unclaimed attempt. Connection-scoped session IDs
own task lookup, provenance, notification groups and accepted evidence. Retained
T3-specific cache reads and legacy bare thread IDs resolve to the local machine's active T3 connection (`db.Store.LegacyT3ID`; the legacy-id triggers of migration 0018 use the same rule). The coordinator polls each configured harness independently. Release and scheduling workers also isolate their launches by connection.
