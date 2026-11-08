# t3-stage

Task manager for T3 Code. Queue tasks, launch them as T3 threads, track status, and see history across all projects. Go + Postgres + React. Talks to T3 via its MCP endpoint (`T3_URL/mcp`).

## Features

- **Projects**: 1:1 with T3 projects. Existing T3 projects import automatically.
- **Tasks**: prompt, provider, model, effort, runtime mode, plan mode, worktree/branch, tags, notes. Save to backlog or start right away.
- **Live status**: backlog → running → needs input → done → settled (or failed / interrupted). Polled from T3 every 5s.
- **Task actions**: start, follow-up, stop, duplicate, open in T3.
- **Chains**: ordered tasks (can span projects). The next step starts when the previous thread is settled in T3. A failure pauses the chain. Controls: start, pause, resume, skip, reorder.
- **Dashboard**: needs-input alert, plus running, done (awaiting settle), backlog and settled (last 7 days) across projects.
- **History**: every T3 thread, including ones started outside this app. Filter by time window, project, status, provider and source; shows counts per project and per day.
- **Templates**: reusable prompt + model presets.
- **Model defaults**: new tasks prefill provider, model, effort, runtime and mode from a pinned default (Settings), else from your last created task.
- **Settings**: T3 connection status, manual pairing, sync now.

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

## Develop

`make dev` starts Postgres, the Go server on `:7380` and Vite, which proxies `/api` to the Go server. `T3_FAKE=1` swaps in a fake T3. Other targets: `make test`, `make build`, `make up`, `make down`. API contract: `docs/api.md`.
