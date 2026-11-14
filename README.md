# stage

Task manager for T3 Code. Queue tasks, launch them as T3 threads, track status, and see history across all projects. Go + SQLite + React, shipped as a single binary. Talks to T3 via its MCP endpoint (`T3_URL/mcp`).

## Features

- **Projects**: 1:1 with T3 projects. Existing T3 projects import automatically. Sort by name or recent activity (latest thread update/settle or task change).
- **Tasks**: prompt, provider, model, effort, runtime mode, plan mode, worktree/branch, tags, notes. Save to backlog or start right away.
- **Screenshots**: drop images on the prompt (or Add image; PNG, JPEG, GIF, WebP, up to 8 × 10 MB). They are sent to the T3 thread with the prompt on every launch; follow-ups can carry images too.
- **Live status**: backlog → running → needs input → done → settled (or failed / interrupted). Polled from T3 every 5s.
- **Task actions**: start, follow-up, stop, duplicate, open in T3.
- **Answer questions**: when a session waits on a question (T3 user input), task and session detail show it in a Needs input panel at the top: single or multi select, or your own answer, then Send answer. Needs-input rows, toasts and the notifications drawer get an Answer button, and needs-input pushes land on the panel. Permission approvals still resolve in T3; the panel counts them and links there.
- **Run order**: link a task to run after another (while creating, viewing or editing it; can span projects). It starts when that thread is settled in T3; a failed predecessor keeps it waiting. Start now unlinks it and starts it immediately. Handoff (on by default, per task): when a linked task starts, stage appends its predecessor's final reply (up to 4000 chars), PR links, branch/worktree and T3 thread link to the prompt, so sequential work carries context forward. Task detail shows what was (or will be) appended; a failed read launches without it and logs why. Stacked work: a linked task's Workspace can be "#12's branch" (opt-in): its worktree starts from the predecessor's branch, so it builds on unmerged changes. It falls back to its own base ref (logged, shown on task detail) when the predecessor ran at root, in another project, has no branch, its PRs are merged, or the branch is gone.
- **Sessions**: browse Active or All sessions across projects, regardless of where they started. Active has no creation-date cutoff. Open local session details, inspect child sessions, or jump to T3. History remains available from Sessions.
- **Transcript**: session and task detail have an Overview | Transcript switch (`?view=transcript`). Read-only user and assistant messages and plans as safe markdown (no raw HTML, no remote images), newest 50 with Load earlier, long messages collapsed. Live while a run is active: the server polls T3 every 2s (one poller per session, only while someone watches) and pushes changes over SSE. Tool calls, commands and diffs stay in T3.
- **Queued continuations**: open session A, choose an existing top-level session B, and save a prompt. Both current runs continue untouched. Delivery waits for A to settle explicitly and B to finish with no pending input. Linear chains track the delivered continuation run before releasing the next step. Failed, interrupted, or cancelled sessions block delivery; uncertain sends are held for review rather than retried. Edit, pause, resume, or cancel before delivery is claimed. The saved prompt includes A's reference, without copying its transcript.
- **Last word**: each session's latest assistant reply, read during sync once a thread is at rest. Task and session detail show it as markdown (up to 4000 chars, with a link to T3 when longer); Needs input and Done rows show it as one line. Read-only: reply in T3.
- **Dashboard**: needs-input alert, plus running, done (awaiting settle), backlog and settled (last 7 days) across projects. External T3 threads (not tasks) show under needs input and running.
- **Pull requests and runs**: PRs T3 links to a thread show as chips with their state (open, merged, closed) on task and session rows, and as a list on task and session detail linking to the PR. Detail pages also list the thread's runs (each follow-up is a new run): status, model, start, run time and queue time, newest first.
- **History**: every T3 thread, including ones started outside this app. Filter by time window, project, status, provider and source; shows counts per project and per day.
- **Scheduled tasks**: in the New task drawer pick Start → At time (one-shot: a backlog task starts at that wall time in a chosen zone; a time missed while the server was down starts late) or Repeat (a schedule: presets Hourly / Daily / Weekdays / Weekly or a raw 5-field cron, in its own IANA zone). Each fire spawns and starts a new task with the schedule's config and images; a fire is skipped while the previous run is still pending, running or needs input, and fires missed while the server was down are skipped. Schedules page, project panel, and a Scheduled (next 24h) group on Stage. Run now, pause/resume, edit, delete (spawned tasks are kept).
- **Ctrl K palette**: jump to tasks, projects and sessions by title, or find a session by what was said in it. Message text, branch, PR and project matches come from T3's thread search and are labeled (`branch`, `PR`, `you said`, `agent said`) under Sessions and In messages, with a snippet. Title matches show instantly; T3 matches follow. When T3 is offline or slow the palette keeps title matches and says so in its footer.
- **Agents**: agents create, start and manage tasks via the `stage` CLI or MCP (stdio or HTTP). See [Agents](#agents).
- **Snippets**: reusable prompt text, global or per project. Type `/` in a task prompt, follow-up or notes to pick one and insert it.
- **Model defaults**: new tasks prefill provider, model, effort, runtime and mode from a pinned default (Settings), else from your last created task.
- **Settings**: T3 connection status, manual pairing, sync now.
- **Notifications**: installable PWA with Web Push for needs input, failed, done and settled (each toggleable in Settings), covering tasks and threads started directly in T3 (subagents excluded). The app icon badge counts what needs input.

## Deploy

Runs on the same machine as T3. Requires Docker with compose.

1. On the T3 host, issue an admin token. stage uses it to mint and auto-renew its own T3 access every 30 days:

   ```sh
   t3 auth session issue --ttl 3650d --label stage --scope access:write --scope orchestration:read --scope orchestration:operate --scope settings:write --scope source-control:write --token-only
   ```

2. Configure:

   ```sh
   cp .env.example .env   # set T3_ADMIN_TOKEN; T3_PUBLIC_URL = T3 address used for "Open in T3" links
   ```

3. Run:

   ```sh
   docker compose up -d --build
   ```

   UI at `http://<host>:7380`, bound to `0.0.0.0`. The SQLite database (`stage.db`) lives in the `stagedata` volume (`STAGE_DATA_DIR=/data`). Outside Docker it defaults to `$XDG_DATA_HOME/stage` (else `~/.local/share/stage`; macOS `~/Library/Application Support/stage`).

Update: `git pull && docker compose up -d --build`.

## Install as an app (PWA) and notifications

Push and install need HTTPS. Put a reverse proxy (e.g. Caddy) with a trusted cert in front of `:7380` and set `APP_PUBLIC_URL` to that URL (it is also the VAPID subject). The domain can stay private (LAN/tailnet): the server only makes outbound requests to the browser push services (Apple, Google, Mozilla). The device needs internet to receive pushes.

The live transcript is a Server-Sent Events stream (`/api/threads/:id/transcript/stream`). Caddy's `reverse_proxy` flushes it immediately; if your config uses `encode`, exclude that path so events aren't buffered. Other proxies: disable response buffering (the server sends `X-Accel-Buffering: no`) and allow long-lived responses (a ping every 15s).

- iOS/iPadOS 16.4+: open the URL in Safari, Share → Add to Home Screen, open stage from the Home Screen, then Settings → Notifications → Enable.
- macOS: Safari → File → Add to Dock (or use the site in Safari/Chrome), then Settings → Notifications → Enable.
- Settings → Notifications → Send test checks the device. The service worker registers only in production builds (`mise run build` / compose).

## Agents

Agents (Claude Code, Codex, ...) running in T3 can queue and manage stage tasks. Three front ends share one set of operations over the REST API:

- **CLI**: `stage task add|ls|show|edit|rm|start|stop|followup|dup|link|order|unlink|wait`, `schedule ...`, `sessions ls|search|show|questions` (questions is read-only), `projects ls`, `models ls`, `context`. Human output by default, `--json` for agents/scripts. `stage help` lists everything.
- **stdio MCP**: `stage mcp`.
- **HTTP MCP**: `http://<host>:7380/mcp` (streamable HTTP, served by the running app; no install). Cannot read local files, so no `imagePaths`.

Install the binary on the T3 host (the compose image does not install it; rerun after updates) and register it:

```sh
mise run install-cli                 # ~/.local/bin/stage (no args = server; subcommands = CLI)
stage setup mcp                    # claude mcp add --scope user + codex mcp add (stdio); --http registers /mcp instead
stage setup instructions           # idempotent usage block in ~/.claude/CLAUDE.md and ~/.codex/AGENTS.md
```

`STAGE_URL` (or `--url`) points at the app, default `http://127.0.0.1:7380`.

Caller context: T3 sets `T3CODE_THREAD_ID` in every provider process. The CLI and stdio MCP read it and the cwd; HTTP MCP tools take `callerThreadId` / `cwd` arguments instead.

- Project: explicit `--project` (id or title) > the calling session's project > the project whose root contains cwd.
- Model: explicit > pinned default > last used (like the New task form).
- Provenance: tasks and schedules record the creating session (`originThreadId`; spawned schedule tasks inherit it), shown as "Created by" on task detail and "by agent" on task rows.
- Run after me: `--after-me` / `afterMe` starts the task when the calling session is settled in T3. A session owned by a task becomes a normal task link; any other session is a session link (`afterThreadId`, many tasks may wait on one session).
- Handoff: a linked task's launch prompt gets its predecessor's final reply, PRs, branch/worktree and thread id (default on; `--handoff=false` / `handoff: false` turns it off on add, edit or link).
- Predecessor base: `--base predecessor --base-ref origin/main` / `base: "predecessor"` starts a linked task's worktree from its predecessor's branch (`--base-ref` is the fallback); `task show` / `get_task` show the base the launch used.
- Session batch: the tasks a session created, grouped into run-order chains (head first) and independent tasks. `get_session` / `stage sessions show` return it (`createdTasks`) so a planning agent can check what it queued; `order_tasks` / `stage task order ID ID...` links several backlog tasks in order in one step. In the web UI, session detail lists the batch ("Created tasks": start a chain, start a task, link selected tasks in order, reorder an unstarted chain, edit) and task detail shows it as a compact Batch panel.

MCP tools: `get_context`, `list_projects`, `list_models`, `list_tasks`, `get_task`, `create_task`, `update_task`, `delete_task`, `start_task`, `stop_task`, `followup_task`, `duplicate_task`, `link_task`, `order_tasks`, `unlink_task`, `wait_task`, `list_schedules`, `get_schedule`, `create_schedule`, `update_schedule`, `delete_schedule`, `run_schedule`, `preview_schedule`, `list_sessions`, `search_sessions`, `get_session`. No gating: agents get the same access as the web UI.

```sh
stage task add --title "Fix flaky test" --prompt "..." --effort high          # backlog
stage task add --title "Deploy" --prompt-file deploy.md --after-me             # runs when this session settles
echo "..." | stage task add --title "Refactor" --prompt - --base-ref origin/main --start
stage task ls --state backlog --json
stage task wait 42 --until done,settled --timeout 30m                          # exit 3 on timeout
stage task order 41 43 42                                                      # 43 runs after 41, 42 after 43
stage sessions show "$T3CODE_THREAD_ID"                                         # this session's batch
stage schedule add --title "Nightly deps" --prompt "..." --cron "0 2 * * *" --tz Asia/Kolkata
stage sessions search "token refresh" --project stage                          # titles, then message text via T3
```

## Develop

Development never touches production. Dev uses a SQLite file per branch (`.scratch/dev/<branch>/stage.db`); tests use a temp file per test. The prod stack is `compose.yaml`, and only `mise run up` / `mise run down` touch it.

Tools (Go, Node, pnpm) and tasks live in `mise.toml`: `mise install`, then `mise tasks` lists them.

- `mise run dev`: a database per branch (`.scratch/dev/<branch>/stage.db`), the Go server on `:7390` and Vite, which proxies `/api` to it. It talks to the real T3 as its own session (`stage dev`; prod is `stage`). It loads `.env.dev` (copy `.env.dev.example`), never `.env`, and always sets `STAGE_DATA_DIR` itself.
- `mise run test`: Go tests (a throwaway SQLite file per test), web typecheck/lint, and session semantic/API contract tests.
- `node --test web/tests/sessions.test.mjs`: session eligibility, continuation editing/delivery preview, URL encoding and HTTP contract tests.
- `node --test web/tests/palette-search.test.mjs`: palette session search merge, dedupe, labels and T3 offline note.
- `node --test web/tests/tasks.test.mjs`: session batch helpers (selection, Start chain, reorder, progress) and the order HTTP contract.
- `mise run check`: everything CI-like (go vet, go test, web install/test/typecheck/lint). Run before a PR.
- Other tasks: `mise run build`, `mise run install-cli`, `mise run up`, `mise run down`. API contract: `docs/api.md`.

Guards: with `APP_ENV=dev` (set by `mise run dev`) the server refuses the default (production) data dir. Launches send a unique `clientRequestId` per attempt (`task-<id>-<uuid>`), so dev, test or restored databases that share one T3 can't collide with prod launches. `T3_FAKE=1` still swaps in a fake T3.
