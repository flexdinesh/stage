# Agents

- Use `mise run <task>` for dev, tests and checks (`mise tasks` lists them; tools pinned in `mise.toml`).
- Run `mise run check` before opening a PR (includes `check:fixture`, the desktop fixture's Go tests).
- Touching `desktop/fixture/`: also run `pnpm test && pnpm lint && pnpm build` in `desktop/fixture/web`, and `mise run fixture:e2e` where a display + WebKitGTK 4.1 (or macOS) exists; otherwise say it was not run. Rerun `go tool mygo generate` there after changing the bound Go service. See `desktop/fixture/CHECKS.md` and `docs/adr/0001-desktop-shell.md`.
- Never touch the prod stack (compose project `stage`, `compose.yaml`, its `stagedata` volume and `stage.db`) or the default data dir (`~/.local/share/stage`): no `mise run up`/`down`, no `docker compose` on it, no writes to its database. Dev uses `.scratch/dev/<branch>/stage.db`; tests use temp files.
- Don't run `mise run install-cli` unless the user asks: it overwrites the live `~/.local/bin/stage` every agent uses.
