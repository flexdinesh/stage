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

## Develop

Development never touches production. Dev and tests run on their own Postgres (`compose.dev.yaml`, `127.0.0.1:5440`, volume `t3-stage-dev_pgdata`). The prod stack is `compose.yaml` (`5439`), and only `make up` / `make down` touch it.

- `make dev`: dev Postgres, a database per branch (`t3stage_dev_<branch>`), the Go server on `:7390` and Vite, which proxies `/api` to it. It talks to the real T3 as its own session (`t3-stage dev`; prod is `t3-stage`). It loads `.env.dev` (copy `.env.dev.example`), never `.env`, and always sets `DATABASE_URL` itself.
- `make test`: Go tests in throwaway `t3stage_test_*` databases on the dev Postgres, web typecheck/lint, and session semantic/API contract tests.
- `node --test web/tests/sessions.test.mjs`: session eligibility, continuation editing/delivery preview, URL encoding and HTTP contract tests.
- `make dev-down` stops the dev Postgres. Other targets: `make build`, `make up`, `make down`. API contract: `docs/api.md`.

Guards: with `APP_ENV=dev` (set by `make dev`) the server refuses the prod database (port `5439` or name `t3stage`); test setup panics on it. Launches send a unique `clientRequestId` per attempt (`task-<id>-<uuid>`), so dev, test or restored databases that share one T3 can't collide with prod launches. `T3_FAKE=1` still swaps in a fake T3.
