# Agents

- Use `mise run <task>` for dev, tests and checks (`mise tasks` lists them; tools pinned in `mise.toml`).
- Run `mise run check` before opening a PR.
- Never touch the prod stack (compose project `stage`, `compose.yaml`, Postgres on `127.0.0.1:5439`): no `mise run up`/`down`, no `docker compose` on it, no writes to its database. Dev and tests use `compose.dev.yaml` (5440).
- Don't run `mise run install-cli` unless the user asks: it overwrites the live `~/.local/bin/stage` every agent uses.
