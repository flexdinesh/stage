# Architecture: ownership and UI hosts

Stage is one Go server plus one React application that more than one host
can render: the browser (PWA) and the MyGo desktop app
([ADR 0001](adr/0001-desktop-shell.md), [desktop.md](desktop.md)).

Execution contracts and adapter responsibilities: [execution.md](execution.md).
Machines, the worker process and the main/worker split:
[ADR 0002](adr/0002-machines-and-workers.md).
Storage engines, ports and adapters: [ADR 0003](adr/0003-storage-adapters.md),
[storage.md](storage.md).
Claude Code harness (background sessions, spike evidence):
[ADR 0005](adr/0005-claude-code-harness.md).
The Stage and harness boundary (what Stage owns, what a harness owns,
declared capabilities, project identity):
[ADR 0007](adr/0007-harness-boundary.md).
The harness foundation target and its delivery layers:
[ADR 0008](adr/0008-harness-foundation.md),
[workspace discovery and project identity](discovery.md),
[harness authoring](harnesses.md).

## Ownership

| Path | Owns | Must not |
| --- | --- | --- |
| `server/` (Go module `github.com/flexdinesh/stage/server`) | REST API, `/mcp`, CLI, stdio MCP, storage (SQLite by default, Postgres in Docker builds: [storage.md](storage.md)), harness connections, scheduling, business rules; embeds the browser host build (`internal/webui`) | Be imported by another module's `internal` path (promote a small public package instead, per ADR 0001) |
| `packages/app/` (`@stage/app`) | The whole UI: routes, pages, components, query/state logic, API client and types (`src/api/types.ts` mirrors [api.md](api.md)), design tokens (`src/index.css`), the platform contract (`src/platform/index.ts`), and the per-harness slot registry (`src/harnesses/`: each kind's setup panel, connection row, live status and typed calls, with a generic fallback) | Use browser globals at a platform boundary, import a host or MyGo, branch on the platform, or name a harness kind outside `src/harnesses/` (ADR 0007 decision 4) |
| `apps/web/` (`@stage/web`) | Browser host: `index.html`, `src/main.tsx` (mount, service worker registration), `src/sw.ts`, `public/` (manifest, icons, favicons), Vite config, browser adapters (`src/platform/`) | Hold product UI or business logic |
| `desktop/app/` | Desktop host: Go module `github.com/flexdinesh/stage/desktop/app` (MyGo shell, bridge B, launcher, URL policy) and `desktop/app/ui` (`@stage/desktop`, workspace package: mount and desktop adapters) | Import `server/internal` or the fixture; open `stage.db`; hold business logic |
| `apps/website/` (`@stage/website`) | Static marketing site and public user guides on vocs (`renderStrategy: 'full-static'`), illustrative demos (vendored stageshow in `src/vendor/`), its own `PRODUCT.md`, `DESIGN.md` and `.impeccable/`, and the Cloudflare Workers static-assets config (`wrangler.jsonc`, worker `stage-website`, deployed by Workers Builds from `main`: [website.md](website.md)). Tasks: `mise run website:dev` (:5175), `website:build`, `website:check` (also in `mise run check`), `website:preview` (wrangler dev, :8787) | Import `@stage/*` or product UI/runtime internals; be needed by a product build or release (they install with `--filter` and never build it); deploy with product releases |
| `desktop/fixture/` | MyGo proof (ADR 0001). Separate Go module and its own pnpm lockfile, outside the workspace | Be imported by anything |
| `examples/kubernetes/` | A minimal Kubernetes example for a remote main server (Deployment, Service, PVC, README), per [runtime.md](runtime.md#remote-main-server-and-workers). Documentation only: no build, check or release uses it | Become a supported chart; run more than one replica |
| `examples/postgres/` | A compose example for a main server on Postgres (Stage image + Postgres on `STAGE_PG_IMAGE`, README), per [storage.md](storage.md#docker-image-and-compose-example). Only `check:storage` reads it (the image pin) | Replace the production `compose.yaml`; pin a Postgres tag other than `STAGE_PG_IMAGE` |
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
| [features.md](features.md), [api.md](api.md) (tree, journal, handoff, base rules), [runtime.md](runtime.md) (machines) | `/docs`, `/docs/concepts/*` |
| [discovery.md](discovery.md) (workspace binding, review, project identity) | `/docs/concepts/projects` |
| [execution.md](execution.md) (session lifecycle, capabilities and placement), [api.md](api.md) (connection operations) | `/docs/concepts/sessions`, `/docs/concepts/connections`, `/docs/concepts/machines` |
| [desktop.md](desktop.md) | `/docs/usage/desktop` |
| [service.md](service.md), [runtime.md](runtime.md) (listeners, origin policy) | `/docs/usage/server`, `/docs/guides/phone` |
| [service.md](service.md) (worker service), [runtime.md](runtime.md) (worker process, agent gateway, remote main server), [storage.md](storage.md) (engines), `examples/kubernetes/`, `examples/postgres/` | `/docs/usage/workers` |
| [features.md](features.md), [api.md](api.md) (MCP, tree rules) | `/docs/guides/first-tree`, `/docs/guides/agents` |
| [features.md](features.md) (Claude Code), [execution.md](execution.md#claude-code-adapter) (Claude Code adapter), [api.md](api.md) (Claude setup, adopt) | `/docs/guides/claude-code` |
| [install.md](install.md), [release.md](release.md) (assets, platforms) | `/docs/reference/install` |
| [features.md](features.md) (harness setup), [execution.md](execution.md) (placement and adapter behavior), [api.md](api.md) (connection setup) | `/docs/reference/connections` |
| [service.md](service.md), [runtime.md](runtime.md) (listeners, data dir), [storage.md](storage.md) (engines), `.env.example` | `/docs/reference/configuration` |
| [runtime.md](runtime.md) (origin policy, worker auth) | `/docs/reference/security` |
| [features.md](features.md) (UI, Settings) | `/docs/reference/app` |
| CLI (`server/internal/cli`), [api.md](api.md) (CLI) | `/docs/reference/cli` |
| MCP tools (`server/internal/agenttools`), [api.md](api.md) (MCP) | `/docs/reference/mcp` |

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
below directly or imports MyGo, and if code outside `src/harnesses/` names a harness kind,
compares a harness display name, or imports a harness's files past `src/harnesses/index.ts`.

| Contract | Boundary | Browser adapter (`apps/web/src/platform/`) | Desktop adapter (`desktop/app/ui/src/platform/`) |
| --- | --- | --- | --- |
| `transport.request(path, init)` | API requests. Paths are API-relative (`/tasks/1`); `createApi` maps non-2xx to `ApiError` (status, server message, code) | `transport.ts`: relative `fetch("/api" + path)` | `transport.ts`: the same, over bridge B |
| `transport.subscribe(path, handlers)` | Server-sent events (notification inbox). `close()` cancels the request; no handler runs after it. The host owns reconnect and `Last-Event-ID` | `transport.ts`: native `EventSource` | same (the bridge aborts dropped streams as network errors, so they retry) |
| `transport.assetUrl(path)` | Image/attachment URLs for `<img>` and links | `transport.ts`: `/api` + path | same |
| `clipboard.writeText` | Copy link / URL / reference copying | `clipboard.ts`: Clipboard API, `execCommand` fallback over plain HTTP | `clipboard.ts`: the shell's system clipboard |
| `links.stageLink(path)` | The copyable Stage link for an app path (`/tasks/12`) | `links.ts`: `window.location.origin` + path | `links.ts`: `STAGE_PUBLIC_URL` + path, else `stage://tasks/12` |
| `links.openExternal` / `openApp` | Programmatic opens of web and native app links. Anchors stay native `<a>` and the desktop host routes them by navigation policy | `links.ts`: `window.open` / `location.assign` | the shell's URL policy |
| `notifications?` | System notification delivery: `channel` (`web-push` when absent, `native`), state, enable/disable/test, close shown, cross-device sync, `onDelivered`, `openSystemSettings?`. Absent: Settings shows only the global event filter | `webPush.ts`: service worker + Web Push | `notifications.ts`: `channel: "native"` over the generated MyGo client; the Go process shows OS notifications from the server's notification stream ([desktop.md](desktop.md#notifications)) |
| `attention.setNeedsInput` | Needs-input signal outside the page | `attention.ts`: tab favicon + Badging API | no-op: the Go process owns the Dock/launcher badge |
| `storage` | Per-device preferences (theme, projects sort). Never throws | `storage.ts`: `localStorage` | `storage.ts`: the webview's `localStorage` |
| `hostHistoryControls?` | The host shows its own back and forward, so the laptop sidebar hides its arrows. Absent or false: the arrows show | `historyControls.ts`: true in a tab or a `minimal-ui` window (`display-mode`), false in the installed standalone window | absent: the webview has no browser chrome |
| `host?` | Native shell services: the host-managed server connection (`HostState`, retry/start/restart, diagnostics), login startup, navigation requests (deep links). Drives `HostGate`, `HostBanner` and the desktop lines of Settings, General, Info (`components/Host.tsx`) | absent | `host.ts` over the generated MyGo client |

Not part of the contract: plain DOM and layout (`document` visibility,
`matchMedia`, `history`, `document.title`, reload). A webview provides them as a browser does.

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
- Storage: every package reaches the database through `db.Store`; below it,
  ports and one adapter per engine (SQLite by default, Postgres selected by
  `STAGE_DATABASE_URL` in Docker builds). One server owns one database on
  either engine ([storage.md](storage.md),
  [ADR 0003](adr/0003-storage-adapters.md)).

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

`internal/engine` composes the execution services on the main server. `coordinator` owns launch, handoff and applying observations to tasks. `observe` runs the streams and attention watcher and ingests pushed observations. `discovery` owns the workspace reader and full sync. `machine.Connections` resolves adapters and their roles for local and worker connections. The `workevent` package owns shared task and session transition events, and `notify` delivers them. Retired and legacy-only database columns were dropped in #417 (migrations 0027 and 0028). A historical row keeps its meaning through its session identity or `execution_config`, with its original journal text and accepted snapshot evidence. The list is in [storage.md](storage.md#legacy-columns-and-compatibility-triggers).

Stage owns task acceptance separately from observed execution. Both storage engines persist immutable accepted evidence and idempotent acceptance requests. A launch claim atomically checks current acceptance and pins its inputs. The release runner consumes these claims, and handoff reads only Stage snapshots. See [task acceptance](api.md#user-acceptance-api-v4).

## Users

Stage users are people. One installation owns a shared namespace of machines,
harness connections and sessions ([ADR 0002](adr/0002-machines-and-workers.md#6-identity)).
Migration 0017 seeds `users` with ID 1 (`db.DefaultUser`) and adds
`identities(user_id, provider, subject)`, unique by `(provider, subject)`.
Migration 0040 adds the durable access mode, installation ID, account and local
credential fields, sessions and grants specified in [ADR 0009](adr/0009-shared-access.md).

Personal startup uses the implicit user 1 and existing listener defaults.
Shared opt-in is sticky. A successful setup claim attaches the first admin's
credentials to user 1 in place. Internal shared startup gates requests and
pauses work-producing runners until claim. Public opt-in ships in task #460.

`GET /api/me` reports the acting user. Projects, tasks, trees, journal entries,
schedules and machines expose their durable `created_by_user_id` as `createdBy`.
Authenticated writes with a known person set `db.WithUser`. Operations with no
known person still use the legacy creator default. The request principal
controls authorization independently of `db.ActingUser`. Scheduled tasks and
their initial notes keep the schedule creator on cron and Run now. Work events
carry actor and cause in the transaction that recorded them.
[Work attribution](api.md#work-attribution) · [Journal events](storage.md#journal-events)

Migration 0021 adds `source_machine_id` on projects, tasks, trees, journal
entries and schedules. The worker gateway sets the forwarding machine from its
token through `db.WithMachine`. `db.ActingMachine(ctx)` supplies it for writes,
and the API exposes it as `sourceMachineId`. Project import and schedule
spawns keep their existing ownership fields. Machine tokens identify machines.
The shared-access actor contract records people, agents, machines and automation
independently of these provenance fields. Existing creator IDs and historical
evidence carry forward. See [agent gateway](runtime.md#agent-gateway) and
[ADR 0009 actor policy](adr/0009-shared-access.md#principals-and-endpoint-policy).

## Project and session identity

Stage project IDs and metadata are independent of harness data. Migration 0008
adds harness connection slots, versioned workspace bindings, scoped session
identities and immutable launch destinations. Existing IDs, journal bytes,
accepted snapshot JSON and T3 credentials survive. The legacy columns were
migration data only; migration 0028 dropped them per
[storage.md](storage.md#legacy-columns-and-compatibility-triggers).

A harness connection is one harness instance: a T3 environment (identity: its environment id), an OpenCode service (identity: its verified address, plus continuity proof that it serves the connection's stored workspaces and sessions) or a Codex home (identity: the home directory its daemon serves; Codex is hosted, [ADR 0006](adr/0006-codex-app-server-harness.md)). Migration 0012 stamps existing connections in place, so single-instance data keeps its IDs. Migration 0015 adds machines: every connection belongs to one (`machine_id`; the local machine, or a remote machine served by a thin worker, per [ADR 0002](adr/0002-machines-and-workers.md)), harness kinds are free text validated by the server's harness registry ([execution.md](execution.md#modules-and-the-registry)), and the active and identity indexes are per machine. At most one connection per harness kind is active on a machine, and every "active connection of a kind" lookup names its machine (`db.Store.ActiveID(machine, kind)`, `SwitchInstance(ctx, machine, ...)`; a retired connection revives only on its own machine). Harness setup is per machine too: migration 0018 keys `t3_connection` (T3 token, URL, sync bookkeeping) and `opencode_settings` by `machine_id`, moving the former single rows to machine 1 in place; setup code uses `db.Store.OnMachine(id)` and `harness.Connectors` holds one connector per machine and kind. A remote machine's setup runs on its worker and its harness credentials stay in the worker's `credentials.json`; its `t3_connection` and `opencode_settings` rows hold only the non-secret record ([execution.md](execution.md#remote-machines)). Each machine hosts one adapter per active connection (`machine.Local.Install` moves it when the instance changes; a remote machine's adapters are proxies over its worker channel that the worker answers with its own adapters, [execution.md](execution.md#remote-machines); [execution.md](execution.md#the-machine-seam)) and runs one release and one schedule runner per active connection. Replacing an instance retires its connection: tasks, sessions and bindings stay on it as history, never polled, linked or launched on, and Stage refuses new work there (409 `connection_retired`). Unlaunched backlog tasks and schedules follow the active connection. Bindings move only through project import (below) or the user. The same instance again revives its connection; Remove connection clears a retired connection's identity (or deletes it when unreferenced). `db/connections.go` owns these rules (`SwitchInstance`, `ForgetConnection`). Migration 0020 adds remote machines: a one-time pairing code (10 minutes, single use) is redeemed for a new machine and its machine token, the worker's credential on `/api/worker/*` only; both are stored as SHA-256 and compared in constant time (`db/machineauth.go`; [api.md](api.md#machines), [runtime.md](runtime.md#worker-routes)). Removing a remote machine revokes its token, retires its active connections under the replaced-instance rules (deleting unreferenced ones) and hides its row, which history keeps for its label; machine 1 cannot be removed.

### Project import

Each active connection refreshes inventory on startup, every five minutes and on Sync now. Failed inventories keep the last good snapshot and task polling continues. After a successful refresh, discovery applies the same stored-evidence resolver used by Resolve now and workspace review. The adapter declares import eligibility. For Claude Code and Codex, machine inventory checks the checkout conditions on the file host.

`machine.Ops.Inventory` reads Git metadata directly on the attested file host, in process or on its worker. Network services may supply remote and subpath information themselves. The resolver uses the most specific eligible key. A normalized origin plus known subpath works across machines and harnesses. Local checkout or directory evidence stays on its file host. A pathless workspace uses its connection and external ID, even if it reports repository metadata. See the [resolver rules and examples](discovery.md#identity-keys-and-resolution-07).

Project identity writes serialize across connections. Existing bindings stay attached, unique moves can transfer a binding, and ambiguous rows go to review. A project can have several active bindings on a connection with one primary. A merge makes the old project ID an alias. Stage owns the project archive state. See [storage](storage.md#project-identity) for the migration and transaction rules and [workspace discovery](discovery.md) for binding behavior.

Tasks pin connection/project/workspace/binding revision atomically with an attempt.
Unknown outcomes keep the same destination and request ID on retry. A binding
change applies only to a future unclaimed attempt. Connection-scoped session IDs
own task lookup, provenance, notification groups and accepted evidence.

There is no default harness ([ADR 0007](adr/0007-harness-boundary.md) decision 12): no connection is seeded (migration 0032 removes the never-configured seed), nothing outside a harness's adapter and setup branches on its kind (`harness/boundary_test.go`), and a bare session id names no connection (migration 0027 backfilled stored bare IDs onto the then-active T3 connection, and 0028 dropped the legacy-id triggers). The engine runs independent observation loops for each configured connection. Release and scheduling workers also isolate their launches by connection.
