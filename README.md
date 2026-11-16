# stage

Self-hosted task manager for [T3 Code](https://github.com/pingdotgg/t3code). Queue tasks, launch them as T3 threads, run them as dependency trees, answer what blocks them, and see history across all projects. Go + SQLite + React in a single binary, with an installable PWA and Web Push.

stage requires a running T3 Code server. It talks to T3 only through T3's MCP endpoint (`T3_URL/mcp`); conversations stay in T3, and every task links straight to its thread.

## Features

Full behavior: [docs/features.md](docs/features.md).

- **Tasks**: prompt, provider, model, effort, runtime mode, plan mode, worktree/branch, tags, screenshots, optional initial journal note. Save to backlog or start now; follow up, stop, duplicate, open in T3.
- **Journals**: append-only task/tree notes and events, optional categories (finding, decision, blocker, outcome), search, bounded previews and full-entry reads. Task notes follow current tree membership; tree notes and historical events stay put. Closed work accepts notes without reopening. Recorded work is protected from deletion; emptying or dissolving archives the tree.
- **Projects**: mirror T3 projects 1:1; existing ones import automatically.
- **Live status**: backlog, running, needs input, done, settled (or failed, interrupted). Polled from T3 every 5s.
- **Answer questions**: a session waiting on a question shows a Needs input panel; answer it from stage, including from a phone notification.
- **Trees**: tasks start when the tasks they need settle (a DAG, across projects; auto or manual start). Existing sessions can be tracked as tasks. Handoff appends each need's final reply, PRs and branch to the prompt; stacked tasks can start from a need's branch.
- **Scheduled tasks**: one-shot start times or recurring schedules (presets or 5-field cron, per-schedule time zone).
- **Sessions and history**: every T3 thread, including ones started outside stage, with filters, transcripts (live over SSE), last reply, linked PRs and runs.
- **Dashboard**: needs input first, then running, done, backlog, scheduled and recently settled work.
- **Ctrl K palette**: jump to tasks, projects and sessions by title, or find a session by what was said in it.
- **Snippets and model defaults**: reusable prompt text via `/`; new tasks prefill a pinned or last-used model.
- **Notifications**: installable PWA with Web Push for needs input, failed, done and settled; app icon badge. See [HTTPS and push notifications](#https-and-push-notifications).
- **References**: copy a link to any task or session and paste it into a prompt in stage or T3; agents read it with `read_ref`, and stage shows pasted links as live chips.
- **Agents**: agents create and manage tasks via the `stage` CLI or MCP (stdio or HTTP). See [Agents](#agents).

## Install (desktop or headless)

Linux and macOS, per user, no root. The desktop app includes the CLI/server; `--headless` installs the CLI/server only, for remote machines.

```sh
curl -fsSL https://raw.githubusercontent.com/flexdinesh/stage/main/scripts/install.sh | sh                  # desktop
curl -fsSL https://raw.githubusercontent.com/flexdinesh/stage/main/scripts/install.sh | sh -s -- --headless  # CLI/server only
```

While the repository is private, use an authenticated `gh`: `gh api repos/flexdinesh/stage/contents/scripts/install.sh -H 'Accept: application/vnd.github.raw' | sh`. Then `stage update` installs the latest release (same mode), `stage update --check` compares versions, and `stage uninstall` removes it and keeps your data. Headless: `stage service start` runs the server as a per-user service. Layout, verification, what restarts, recovery and exit codes: [docs/install.md](docs/install.md).

## Quick start: Docker

Runs on the same machine as T3. Requires Docker with compose.

1. Optional: issue a T3 admin token on the T3 host. stage uses it to mint and auto-renew its own T3 access. Without it, pair manually in Settings (`t3 pair`).

   ```sh
   t3 auth session issue --ttl 3650d --label stage --scope access:write --scope orchestration:read --scope orchestration:operate --scope settings:write --scope source-control:write --token-only
   ```

2. Configure and start:

   ```sh
   git clone https://github.com/flexdinesh/stage && cd stage
   cp .env.example .env   # set T3_ADMIN_TOKEN and T3_PUBLIC_URL (e.g. http://localhost:3773)
   docker compose up -d --build
   ```

3. Open `http://localhost:7380`.

How compose wires it (`compose.yaml`):

- The port is published on host loopback only (`127.0.0.1:7380`). See [Security](#security) before changing that.
- The SQLite database (`stage.db`) lives in the `stagedata` volume at `/data`. Back up that volume.
- T3 runs on the host. The container reaches it at `http://host.docker.internal:3773` (`T3_URL`). Browsers cannot resolve that name, so set `T3_PUBLIC_URL` to the address your browser uses for T3, or "Open in T3" links break.
- Linux: `host.docker.internal` maps to the Docker bridge gateway, not the host's loopback. T3 must listen beyond `127.0.0.1` (on `0.0.0.0` or the bridge address) for the container to reach it. Docker Desktop (macOS, Windows) forwards to host loopback, so no change is needed there.

Update: `git pull && docker compose up -d --build`.

## Quick start: single binary

Requires [mise](https://mise.jdx.dev) (or Go, Node and pnpm at the versions in `mise.toml`).

```sh
git clone https://github.com/flexdinesh/stage && cd stage
mise install
mise run build                     # bin/stage, web UI embedded
T3_ADMIN_TOKEN=... ./bin/stage serve
```

Open `http://localhost:7380`. The binary is static (pure Go SQLite, no cgo). It reads config from the environment only (no `.env` file). Data goes to `STAGE_DATA_DIR`, by default `$XDG_DATA_HOME/stage`, else `~/.local/share/stage` (macOS: `~/Library/Application Support/stage`).

The same binary is the agent CLI: `stage` with no arguments or `serve` runs the server; any other subcommand is the CLI.

### As a per-user service (Linux systemd, macOS launchd)

```sh
stage service start      # register + start; creates ~/.config/stage/service.env (macOS: ~/Library/Application Support/stage/)
stage service status     # state, Stage health and T3 reported separately
stage service logs -f
stage service enable     # optional: start at login (separate from running now)
stage service stop | restart | disable | uninstall
```

The OS supervises `stage serve --managed`: restarts with backoff, logs (journald / `~/Library/Logs/stage`), optional login startup. It listens on the private Unix socket only (add TCP in `service.env`: `STAGE_LISTEN=unix,127.0.0.1:7380`), uses the default data dir, and reads its config only from `service.env`, never the shell. It cannot share a data dir with a foreground `stage serve`. Details: [docs/service.md](docs/service.md).

### Desktop app (Linux, macOS)

The desktop app shows the same UI in a native window and talks to the local Stage server over its Unix socket. It starts the per-user service itself (`stage service start`) and leaves it running when you quit. Install it with the installer ([Install](#install-desktop-or-headless)); developer builds: [docs/development.md](docs/development.md) and `mise run desktop:dev`. Behavior, identities and links: [docs/desktop.md](docs/desktop.md).

## Configuration

Environment variables. `.env.example` documents each one.

Server (`stage serve`):

| Variable | Default | Purpose |
|---|---|---|
| `STAGE_LISTEN` | `127.0.0.1:7380` | Listen addresses, comma-separated: `host:port`, `unix:/abs/path.sock`, or `unix` (the private default socket). One process serves all of them. `stage serve --listen ADDR` (repeatable) overrides it. Compose sets `0.0.0.0:7380` inside the container. See [docs/runtime.md](docs/runtime.md). |
| `STAGE_DATA_DIR` | `$XDG_DATA_HOME/stage`, else `~/.local/share/stage` (macOS: `~/Library/Application Support/stage`) | Directory holding `stage.db`. Compose sets `/data`. |
| `STAGE_PUBLIC_URL` | unset | Optional HTTPS URL the PWA is served at. Its origin is trusted by the request-origin policy (see [Security](#security)). It is the Web Push VAPID subject when `https`; otherwise a fallback subject is used. Push works without it. |
| `STAGE_ALLOWED_ORIGINS` | unset | Comma list of extra browser origins (`https://host[:port]`) to trust, e.g. a Tailscale serve or Caddy hostname other than `STAGE_PUBLIC_URL`. Their hosts pass the Host check. |
| `T3_URL` | `http://127.0.0.1:3773` | T3 server stage calls (MCP, OAuth, uploads). Compose defaults to `http://host.docker.internal:3773`. |
| `T3_PUBLIC_URL` | `T3_URL` | T3 address browsers use for "Open in T3" links. Set it under compose. |
| `T3_ADMIN_TOKEN` | unset | Admin token for minting and auto-renewing stage's T3 access. Unset: pair manually in Settings. |
| `T3_FAKE` | unset | `1` uses an in-memory fake T3 (UI work without a T3 server). |
| `T3_FAKE_SEED` | unset | With `T3_FAKE=1`, `1` seeds demo threads waiting on questions. |
| `STAGE_ENV` | unset | `dev` (set by `mise run dev`): names the T3 session `stage dev` and refuses the default data dir. |

Client (CLI, `stage mcp`):

| Variable | Default | Purpose |
|---|---|---|
| `STAGE_URL` | the private default socket when a server owns it, else `http://127.0.0.1:7380` | Server address: an `http(s)` URL, or `unix:///abs/path.sock`. `--url` overrides it. `stage status` shows which server the CLI reaches. |
| `T3CODE_THREAD_ID` | set by T3 | The calling T3 session; picks the project, records provenance, enables `--after-me` and `sessions adopt` without an id. |

### Unix socket

```sh
stage serve --listen unix                       # private socket, e.g. /run/user/1000/stage/stage.sock (0600)
stage serve --listen unix,127.0.0.1:7380        # the socket and TCP, one process
stage status                                    # the CLI finds the private socket by itself
STAGE_LISTEN=unix:/run/stage/stage.sock stage serve   # an explicit socket (0660)
STAGE_URL=unix:///run/stage/stage.sock stage task ls
```

Explicit paths must be absolute and fit the OS socket path limit. Only one server can own a socket path or a data dir: a second one exits with the holder's pid and listeners, and a socket left by a crashed server is replaced. Browsers need a reverse proxy in front of a socket (e.g. Caddy `reverse_proxy unix//run/stage/stage.sock`). `stage setup mcp` passes an explicit `STAGE_URL` to the stdio server; `--http` needs an `http(s)` URL, so use stdio with a socket. Paths, locks, readiness and the health/version contract: [docs/runtime.md](docs/runtime.md).

## Security

stage has no authentication. Anyone who can reach its port can read every task and session, answer questions, and launch agents in full-access mode on the host (`full-access` is the task default).

- It binds `127.0.0.1` by default, and compose publishes the port on host loopback only. This keeps other machines out, not browsers.
- `/api` and `/mcp` reject requests that browsers send on behalf of other sites: a Host other than an IP, `localhost` or a trusted origin's host (DNS rebinding), cross-site and same-site requests (`Sec-Fetch-Site`, Origin), and form-encodable bodies (`text/plain`, urlencoded). Trusted origins are `STAGE_PUBLIC_URL`'s and those in `STAGE_ALLOWED_ORIGINS`. Details: [docs/runtime.md](docs/runtime.md#request-origin-policy).
- That policy is not authentication. Any process or person that can reach a listener directly has full access. A private socket (`--listen unix`, mode `0600`) limits that to your user.
- Before exposing it to a network, put an authenticating HTTPS reverse proxy in front and keep stage itself unreachable except through it. A tailnet limits who can connect, not what they can do.
- Upgrading: a proxy hostname other than `STAGE_PUBLIC_URL`'s now gets `403 host … not allowed`; add its origin to `STAGE_ALLOWED_ORIGINS`.
- `.env` holds `T3_ADMIN_TOKEN`; keep it private.

## HTTPS and push notifications

Push notifications and installing the PWA need a secure context. `http://localhost` is one, so both work in a browser on the same machine with no setup. Other devices (phone, laptop) need HTTPS with a certificate they trust. Optionally set `STAGE_PUBLIC_URL` to the HTTPS URL: it is only the Web Push VAPID subject (a fallback is used otherwise). Settings, Notifications says "needs https" when a page is opened over plain HTTP from another address.

The server only makes outbound requests to the browser push services (Apple, Google, Mozilla), so the URL can stay private (LAN or tailnet). Devices need internet to receive pushes.

Options, easiest first:

- **Tailscale serve**: a real certificate for your tailnet name, nothing to install on devices. Easiest for iOS. Enable HTTPS certificates for the tailnet, then on the stage host:

  ```sh
  tailscale serve --bg http://127.0.0.1:7380
  ```

  stage is then at `https://<machine>.<tailnet>.ts.net`, reachable only from your tailnet.

- **Caddy**: with a real domain, Caddy gets a certificate automatically:

  ```caddyfile
  stage.example.com {
      reverse_proxy 127.0.0.1:7380
  }
  ```

  For a LAN-only name, issue a certificate with [mkcert](https://github.com/FiloSottile/mkcert) (`mkcert stage.lan`) and use `tls stage.lan.pem stage.lan-key.pem`. Every device must trust the mkcert root CA; on iOS install it as a profile, then enable it under Settings, General, About, Certificate Trust Settings.

- **Any reverse proxy** that terminates TLS and forwards to `127.0.0.1:7380`, with the streaming settings below.

Behind a proxy that forwards to `127.0.0.1`, HTTP MCP (`/mcp`) returns 403; agents use stdio MCP or the CLI with `STAGE_URL`. See [Agents](#agents).

The live transcript is a Server-Sent Events stream (`/api/threads/:id/transcript/stream`). Caddy's `reverse_proxy` flushes it immediately; if your config uses `encode`, exclude that path so events are not buffered. Other proxies: disable response buffering (the server sends `X-Accel-Buffering: no`) and allow long-lived responses (a ping every 15s).

Install and enable:

- iOS/iPadOS 16.4+: open the URL in Safari, Share, Add to Home Screen, open stage from the Home Screen, then Settings, Notifications, Enable.
- macOS: Safari, File, Add to Dock (or use the site in Safari or Chrome), then Settings, Notifications, Enable.
- Settings, Notifications, Send test checks the device. The service worker registers only in production builds (`mise run build` or compose).

## Agents

Agents (Claude Code, Codex, ...) running in T3 can queue and manage stage tasks. Three front ends share one set of operations over the REST API:

- **CLI**: `stage task add|ls|show|edit|needs|notes|rm|start|stop|followup|dup|wait`, `tree ls|show|new|add|rm|edit|notes|start`, `schedule ...`, `sessions ls|search|show|questions|adopt` (questions is read-only), `show REF` (any stage link, T3 thread link, `#ID` or thread id), `projects ls`, `models ls`, `context`. Human output by default, `--json` for agents and scripts. `stage help` lists everything.
- **stdio MCP**: `stage mcp`.
- **HTTP MCP**: `/mcp` on the running app (streamable HTTP, served in-process; no install). Cannot read local files, so no `imagePaths`. Reachable only where the MCP SDK's DNS rebinding check passes: directly at a loopback address with a loopback Host (`http://127.0.0.1:7380/mcp`, `http://localhost:7380/mcp`), or on a non-loopback listen address (compose, or `STAGE_LISTEN=0.0.0.0:7380` reached by its network address). A request that arrives on loopback with another Host gets `403 Forbidden: invalid Host header`, so a reverse proxy (Tailscale serve, Caddy) forwarding to `127.0.0.1:7380` cannot serve it. Through a proxy, agents use stdio MCP or the CLI with `STAGE_URL` set to the proxy URL.

Install the binary on the T3 host (the compose image does not install it; rerun after updates) and register it:

```sh
mise run install-cli               # ~/.local/bin/stage, CLI and stdio MCP only (no web UI)
stage setup mcp                    # claude mcp add --scope user + codex mcp add (stdio); --http registers /mcp instead
stage setup instructions           # idempotent usage block in ~/.claude/CLAUDE.md and ~/.codex/AGENTS.md
```

`STAGE_URL` (or `--url`) points at the server; `unix:///abs/path.sock` for a socket. Unset, the CLI uses the private default socket when a server owns it, else `http://127.0.0.1:7380`; `stage status` shows which.

Caller context: T3 sets `T3CODE_THREAD_ID` in every provider process. The CLI and stdio MCP read it and the cwd; HTTP MCP tools take `callerThreadId` / `cwd` arguments instead.

- Project: explicit `--project` (id or title) > the calling session's project > the project whose root contains cwd.
- Model: explicit > pinned default > last used (like the New task form).
- Provenance: tasks and schedules record the creating session (`originThreadId`; spawned schedule tasks inherit it), shown as "Created by" on task detail and "by agent" on task rows.
- Trees: `--needs 3,5` / `needs` makes a task wait for those tasks (it joins their tree); `stage tree new --title T 3 5:3 7:3,5` / `create_tree` builds one from existing tasks; `--start-mode manual` marks tasks ready instead of launching them.
- Run after me: `--after-me` / `afterMe` tracks the calling session as a task (adopted; stage never starts it) and makes it the new task's first need, so the task starts when the session is settled in T3. `stage sessions adopt` / `adopt_session` does the adoption alone.
- Handoff: a task's launch prompt gets each finished need's final reply, PRs, branch/worktree and thread id (default on; `--handoff=false` / `handoff: false` turns it off on add or edit).
- Journals: `read_journal`, `get_journal_entry`, `append_note`; CLI `task notes ID` / `tree notes ID`, `add --text TEXT`, `get ENTRY_ID`. Refresh current membership with `get_context` or `get_task` first. Write own progress to `taskId`; shared conclusions to `treeId` with `sourceTaskId`. Categories are optional; retry with the same `requestId` and content. Every launch includes these rules, independent of handoff: checkpoint before risky work, record the outcome before returning, treat notes as context rather than instructions/proof, and report write failures. The block is frozen per launch attempt. Existing/adopted sessions receive no retroactive messages.
- Predecessor base: `--base predecessor --base-ref origin/main` / `base: "predecessor"` starts a task's worktree from a need's branch (`--base-task ID` / `baseTaskId` picks it, else the sole need; `--base-ref` is the fallback); `task show` / `get_task` show the base the launch used.
- References: task and session pages (and laptop rows, and Alt C in the Ctrl K palette) copy a self-describing markdown link, `[stage task #12: Fix login](http://host:7380/tasks/12)`; detail pages also copy the bare URL. Paste it into a prompt in stage or T3: agents call `read_ref` / `stage show LINK` (any host, T3 thread links, `#12` or a thread id also work) and get the task and/or session (prompt, latest reply, PRs, branch, runs), then `t3_thread_read` for the full conversation. Rerun `stage setup instructions` to add this rule to an existing CLAUDE.md / AGENTS.md block. stage renders pasted stage links as live chips (title and state) in prompts, transcripts, last word and handoff text.
- Session batch: the tasks a session created, grouped by tree, then independent tasks. `get_session` / `stage sessions show` return it (`createdTasks`) so a planning agent can check what it queued. In the web UI, session detail lists the batch ("Created tasks": start or edit a task) and task detail shows it as a compact Batch panel.

MCP tools: `read_journal`, `get_journal_entry`, `append_note`, `get_context`, `list_projects`, `list_models`, `list_tasks`, `get_task`, `create_task`, `update_task`, `delete_task`, `start_task`, `stop_task`, `followup_task`, `duplicate_task`, `wait_task`, `list_trees`, `get_tree`, `create_tree`, `update_tree`, `add_to_tree`, `remove_from_tree`, `set_needs`, `start_tree`, `adopt_session`, `list_schedules`, `get_schedule`, `create_schedule`, `update_schedule`, `delete_schedule`, `run_schedule`, `preview_schedule`, `list_sessions`, `search_sessions`, `get_session`, `get_session_questions`, `read_ref`. No gating: agents get the same access as the web UI.

```sh
stage task add --title "Fix flaky test" --prompt "..." --effort high          # backlog
stage task add --title "Deploy" --prompt-file deploy.md --after-me             # runs when this session settles
echo "..." | stage task add --title "Refactor" --prompt - --base-ref origin/main --start
stage task ls --state backlog --json
stage task wait 42 --until done,settled --timeout 30m                          # exit 3 on timeout
stage tree new --title "Auth" 41 43:41 42:43                                   # 43 needs 41, 42 needs 43
stage sessions show "$T3CODE_THREAD_ID"                                         # this session's batch
stage schedule add --title "Nightly deps" --prompt "..." --cron "0 2 * * *" --tz Europe/Berlin
stage sessions search "token refresh" --project stage                          # titles, then message text via T3
```

## Develop

Tools (Go, Node, pnpm) and tasks live in `mise.toml`: `mise install`, then `mise tasks` lists them. Development never touches a deployed stack: dev uses a SQLite file per branch (`.scratch/dev/<branch>/stage.db`), tests use a temp file per test, and only `mise run up` / `mise run down` touch the compose stack.

- `mise run dev`: the branch database, the Go server on `127.0.0.1:7390` and the browser host on Vite, which proxies `/api` to it. It talks to the real T3 as its own session (`stage dev`). It loads `.env.dev` (copy `.env.dev.example`), never `.env`, and always sets `STAGE_DATA_DIR` itself. Set `T3_FAKE=1` in `.env.dev` to work without T3.
- `mise run test`: Go vet and tests; app and web host tests, typecheck and lint.
- `mise run check`: everything CI-like (Go vet and tests, workspace install with a frozen lockfile, tests, typecheck, lint, desktop app and fixture Go tests). Run before a PR.
- `mise run build`: the browser bundle (`web/dist`) embedded into `bin/stage`.
- `mise run desktop:dev`: the desktop app against a foreground dev server on a scratch socket (no OS service). `mise run dist [TARGETS=host|all]`: a developer build of the desktop app and the headless CLI/server (`stage-dev`), verified, in `.scratch/dist/<version>`. `mise run release VERSION=X.Y.Z`: the release build of a clean HEAD ([docs/release.md](docs/release.md)). `mise run desktop:e2e`: webview e2e (display and WebKitGTK 4.1, or macOS). See [docs/desktop.md](docs/desktop.md).
- `mise run install-cli`: the CLI and stdio MCP binary (no web bundle) at `~/.local/bin/stage`. It is not an installer install: `stage update` refuses it and the installer will not overwrite it.
- Installing a development build as Stage Dev: [docs/development.md](docs/development.md). `mise run install:smoke`: the installer against real binaries and systemd with the isolated `stage-svctest` identity ([docs/install.md](docs/install.md#verification)).
- `mise run up` / `mise run down`: build and run, or stop, the compose stack (data volume kept).
- Single test files: `node --test app/tests/<name>.test.mjs` (e.g. `sessions`, `tasks`, `api-client`, `boundaries`) or `node --test web/tests/<name>.test.mjs` (browser adapters: `transport`, `adapters`, `push-state`).

Layout: `server/` (Go server, CLI, MCP), `app/` (the shared React app, `@stage/app`), `web/` (browser host), `desktop/app/` (desktop host; its frontend `desktop/app/ui` is in the pnpm workspace), one pnpm workspace. Ownership and the platform contract: [docs/architecture.md](docs/architecture.md).

API contract: [docs/api.md](docs/api.md) (TypeScript mirror: `app/src/api/types.ts`).

API v2 replaces task `notes` with `initialNote` on creation/adoption and append-only journals. Old task notes are discarded; old task events remain. Accurate tree history begins at rollout. Schedule `notes` remains the initial-note template for spawned tasks; duplicates copy no notes. Upgrade server and clients together.

Guards: with `STAGE_ENV=dev` the server refuses the default data dir. Launches send a unique `clientRequestId` per attempt (`task-<id>-<uuid>`), so dev, test or restored databases that share one T3 cannot collide with a deployed instance's launches.

## License

[MIT](LICENSE)
