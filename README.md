# t3-stage

Task/stage manager for T3 Code: projects, tasks, and sequential chains on top of a local T3 install, plus a cross-project history of every T3 thread. Go + Postgres + React. Runs on the T3 host and binds `0.0.0.0:7380`. Talks to T3 only via its MCP endpoint (`T3_URL/mcp`).

## Layout

- `server/`: Go backend (`cmd/t3stage`, `internal/{config,db,t3,sync,chains,api,webui}`). Serves the API and the embedded web UI.
- `web/`: Vite + React + TypeScript + Tailwind SPA.
- `docs/api.md`: REST contract (mirrored in `web/src/api/types.ts`).

## Setup

Requirements: Docker with compose. For local dev also Go 1.27, Node, pnpm.

1. One-time, on the T3 host, issue an admin session token (lets t3-stage mint and auto-renew its own OAuth token):

   ```sh
   t3 auth session issue --ttl 3650d --label t3-stage --scope access:write --scope orchestration:read --scope orchestration:operate --scope settings:write --scope source-control:write --token-only
   ```

2. Configure:

   ```sh
   cp .env.example .env
   # paste the token into T3_ADMIN_TOKEN; adjust T3_URL / T3_PUBLIC_URL if needed
   ```

   Without `T3_ADMIN_TOKEN`, pair manually from Settings with a code from `t3 pair`.

   `T3_FAKE=1` swaps in an in-memory fake T3 (no T3 server needed; for UI/dev only).

## Run

```sh
docker compose up -d --build
```

App on `http://<host>:7380` (host networking). Postgres on `127.0.0.1:5439`, data in the `pgdata` volume.

## Develop

```sh
make dev
```

Starts Postgres (compose), the Go server on `:7380` and the Vite dev server (proxies `/api` to `:7380`). Open the Vite URL it prints.

Other targets: `make db`, `make build` (single binary at `bin/t3stage`), `make test`, `make up`, `make down`.
