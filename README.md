# t3-stage

Task manager for T3 Code. Queue tasks, launch them as T3 threads, track status, and see history across all projects. Go + Postgres + React. Talks to T3 via its MCP endpoint (`T3_URL/mcp`).

## Features

- **Projects**: 1:1 with T3 projects. Existing T3 projects import automatically. Sort by name or recent activity (latest thread update/settle or task change).
- **Tasks**: prompt, provider, model, effort, runtime mode, plan mode, worktree/branch, tags, notes. Save to backlog or start right away.
- **Screenshots**: drop images on the prompt (or Add image; PNG, JPEG, GIF, WebP, up to 8 × 10 MB). They are sent to the T3 thread with the prompt on every launch; follow-ups can carry images too.
- **Live status**: backlog → running → needs input → done → settled (or failed / interrupted). Polled from T3 every 5s.
- **Task actions**: start, follow-up, stop, duplicate, open in T3.
- **Run order**: link a task to run after another (while creating, viewing or editing it; can span projects). It starts when that thread is settled in T3; a failed predecessor keeps it waiting. Start now unlinks it and starts it immediately.
- **Sessions**: browse Active or All sessions across projects, regardless of where they started. Active has no creation-date cutoff. Open local session details, inspect child sessions, or jump to T3. History remains available from Sessions.
- **Queued continuations**: open session A, choose an existing top-level session B, and save a prompt. Both current runs continue untouched. Delivery waits for A to settle explicitly and B to finish with no pending input. Linear chains track the delivered continuation run before releasing the next step. Failed, interrupted, or cancelled sessions block delivery; uncertain sends are held for review rather than retried. Edit, pause, resume, or cancel before delivery is claimed. The saved prompt includes A's reference, without copying its transcript.
- **Dashboard**: needs-input alert, plus running, done (awaiting settle), backlog and settled (last 7 days) across projects. External T3 threads (not tasks) show under needs input and running.
- **History**: every T3 thread, including ones started outside this app. Filter by time window, project, status, provider and source; shows counts per project and per day.
- **Scheduled tasks**: in the New task drawer pick Start → At time (one-shot: a backlog task starts at that wall time in a chosen zone; a time missed while the server was down starts late) or Repeat (a schedule: presets Hourly / Daily / Weekdays / Weekly or a raw 5-field cron, in its own IANA zone). Each fire spawns and starts a new task with the schedule's config and images; a fire is skipped while the previous run is still pending, running or needs input, and fires missed while the server was down are skipped. Schedules page, project panel, and a Scheduled (next 24h) group on Stage. Run now, pause/resume, edit, delete (spawned tasks are kept).
- **Agents**: agents create, start and manage tasks via the `t3stage` CLI or MCP (stdio or HTTP). See [Agents](#agents).
- **Snippets**: reusable prompt text, global or per project. Type `/` in a task prompt, follow-up or notes to pick one and insert it.
- **Model defaults**: new tasks prefill provider, model, effort, runtime and mode from a pinned default (Settings), else from your last created task.
- **Settings**: T3 connection status, manual pairing, sync now.
- **Notifications**: installable PWA with Web Push for needs input, failed, done and settled (each toggleable in Settings), covering tasks and threads started directly in T3 (subagents excluded). The app icon badge counts what needs input.

## Deploy

Runs on the same machine as T3. Requires Docker with compose.

1. On the T3 host, issue an admin token. t3-stage uses it to mint and auto-renew its own T3 access every 30 days:

   ```sh
   t3 auth session issue --ttl 3650d --label t3-stage --scope access:write --scope orchestration:read --scope orchestration:operate --scope settings:write --scope source-control:write --token-only
   ```

2. Configure:

   ```sh
   cp .env.example .env   # set T3_ADMIN_TOKEN; T3_PUBLIC_URL = T3 address used for "Open in T3" links
   ```

3. Run:

   ```sh
   docker compose up -d --build
   ```

   UI at `http://<host>:7380`, bound to `0.0.0.0`. Postgres listens on `127.0.0.1:5439` and stores data in the `pgdata` volume.

Update: `git pull && docker compose up -d --build`.

## Install as an app (PWA) and notifications

Push and install need HTTPS. Put a reverse proxy (e.g. Caddy) with a trusted cert in front of `:7380` and set `APP_PUBLIC_URL` to that URL (it is also the VAPID subject). The domain can stay private (LAN/tailnet): the server only makes outbound requests to the browser push services (Apple, Google, Mozilla). The device needs internet to receive pushes.

- iOS/iPadOS 16.4+: open the URL in Safari, Share → Add to Home Screen, open t3-stage from the Home Screen, then Settings → Notifications → Enable.
- macOS: Safari → File → Add to Dock (or use the site in Safari/Chrome), then Settings → Notifications → Enable.
- Settings → Notifications → Send test checks the device. The service worker registers only in production builds (`make build` / compose).

## Agents

Agents (Claude Code, Codex, ...) running in T3 can queue and manage t3-stage tasks. Three front ends share one set of operations over the REST API:

- **CLI**: `t3stage task add|ls|show|edit|rm|start|stop|followup|dup|link|unlink|wait`, `schedule ...`, `sessions ...`, `projects ls`, `models ls`, `context`. Human output by default, `--json` for agents/scripts. `t3stage help` lists everything.
- **stdio MCP**: `t3stage mcp`.
- **HTTP MCP**: `http://<host>:7380/mcp` (streamable HTTP, served by the running app; no install). Cannot read local files, so no `imagePaths`.

Install the binary on the T3 host (the compose image does not install it; rerun after updates) and register it:

```sh
make install-cli                     # ~/.local/bin/t3stage (no args = server; subcommands = CLI)
t3stage setup mcp                    # claude mcp add --scope user + codex mcp add (stdio); --http registers /mcp instead
t3stage setup instructions           # idempotent usage block in ~/.claude/CLAUDE.md and ~/.codex/AGENTS.md
```

`T3STAGE_URL` (or `--url`) points at the app, default `http://127.0.0.1:7380`.

Caller context: T3 sets `T3CODE_THREAD_ID` in every provider process. The CLI and stdio MCP read it and the cwd; HTTP MCP tools take `callerThreadId` / `cwd` arguments instead.

- Project: explicit `--project` (id or title) > the calling session's project > the project whose root contains cwd.
- Model: explicit > pinned default > last used (like the New task form).
- Provenance: tasks and schedules record the creating session (`originThreadId`; spawned schedule tasks inherit it), shown as "Created by" on task detail and "by agent" on task rows.
- Run after me: `--after-me` / `afterMe` starts the task when the calling session is settled in T3. A session owned by a task becomes a normal task link; any other session is a session link (`afterThreadId`, many tasks may wait on one session).

MCP tools: `get_context`, `list_projects`, `list_models`, `list_tasks`, `get_task`, `create_task`, `update_task`, `delete_task`, `start_task`, `stop_task`, `followup_task`, `duplicate_task`, `link_task`, `unlink_task`, `wait_task`, `list_schedules`, `get_schedule`, `create_schedule`, `update_schedule`, `delete_schedule`, `run_schedule`, `preview_schedule`, `list_sessions`, `get_session`. No gating: agents get the same access as the web UI.

```sh
t3stage task add --title "Fix flaky test" --prompt "..." --effort high          # backlog
t3stage task add --title "Deploy" --prompt-file deploy.md --after-me             # runs when this session settles
echo "..." | t3stage task add --title "Refactor" --prompt - --base-ref origin/main --start
t3stage task ls --state backlog --json
t3stage task wait 42 --until done,settled --timeout 30m                          # exit 3 on timeout
t3stage schedule add --title "Nightly deps" --prompt "..." --cron "0 2 * * *" --tz Asia/Kolkata
```

## Develop

Development never touches production. Dev and tests run on their own Postgres (`compose.dev.yaml`, `127.0.0.1:5440`, volume `t3-stage-dev_pgdata`). The prod stack is `compose.yaml` (`5439`), and only `make up` / `make down` touch it.

- `make dev`: dev Postgres, a database per branch (`t3stage_dev_<branch>`), the Go server on `:7390` and Vite, which proxies `/api` to it. It talks to the real T3 as its own session (`t3-stage dev`; prod is `t3-stage`). It loads `.env.dev` (copy `.env.dev.example`), never `.env`, and always sets `DATABASE_URL` itself.
- `make test`: Go tests in throwaway `t3stage_test_*` databases on the dev Postgres, web typecheck/lint, and session semantic/API contract tests.
- `node --test web/tests/sessions.test.mjs`: session eligibility, continuation editing/delivery preview, URL encoding and HTTP contract tests.
- `make dev-down` stops the dev Postgres. Other targets: `make build`, `make install-cli`, `make up`, `make down`. API contract: `docs/api.md`.

Guards: with `APP_ENV=dev` (set by `make dev`) the server refuses the prod database (port `5439` or name `t3stage`); test setup panics on it. Launches send a unique `clientRequestId` per attempt (`task-<id>-<uuid>`), so dev, test or restored databases that share one T3 can't collide with prod launches. `T3_FAKE=1` still swaps in a fake T3.
