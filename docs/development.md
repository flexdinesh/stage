# Development

Day-to-day development (no install needed) and Stage Dev, a persistent
development install of one commit.

## Day-to-day development

Tools (Go, Node, pnpm) and tasks live in `mise.toml`: `mise install`, then
`mise tasks` lists them. Development never touches a deployed stack: dev uses
a SQLite file per branch (`.scratch/dev/<branch>/stage.db`), tests use a temp
file per test, and only `mise run up` / `mise run down` touch the compose stack.

- `mise run dev`: the branch database, the Go server on `127.0.0.1:7390` and the browser host on Vite, which proxies `/api` to it. It talks to the real T3 as its own session (`stage dev`). It loads `.env.dev` (copy `.env.dev.example`), never `.env`, and always sets `STAGE_DATA_DIR` itself. Set `T3_FAKE=1` in `.env.dev` to work without T3.
- `mise run test`: Go vet and tests; shared app, browser host and desktop UI tests, typecheck and lint.
- `mise run check`: everything CI-like (Go vet and tests, workspace install with a frozen lockfile, tests, typecheck, lint, website checks, desktop app and fixture Go tests, release tool). Run before a PR.
- `mise run build`: the browser bundle (`apps/web/dist`) embedded into `bin/stage` (static, pure Go SQLite; `./bin/stage serve` runs it, config from the environment only).
- `mise run website:dev` (:5175), `website:build`, `website:check`, `website:preview` (:8787): the marketing and docs site in `apps/website` ([architecture.md](architecture.md#documentation-ownership)), deployed from `main` ([website.md](website.md)).
- `mise run readme:cover`: records the README cover. The scene is `apps/website/src/components/demos/readme-cover/` (README only, not on the landing), served alone by the capture page in `apps/website/cover/`. `apps/website/scripts/record-readme-cover.ts` steps the kit's virtual clock one 60 fps frame at a time and captures one loop at 2x (1840 x 840, transparent). It writes `docs/assets/readme-cover-{light,dark}.webp` (lossless animated WebP; a theme over 8 MB falls back to lossy quality 92) and `readme-cover-{light,dark}.png` (the `poster` frame). It needs ffmpeg with `libwebp_anim`. `-- --keep` keeps the frames, and `-- light` or `-- dark` records one theme. Edit the scene and re-record; never edit the images.
- `mise run desktop:dev`: the desktop app against a foreground dev server on a scratch socket (no OS service). `mise run dist [TARGETS=host|all]`: a developer build of the desktop app and the headless CLI/server (`stage-dev`), verified, in `.scratch/dist/<version>`. `mise run release VERSION=X.Y.Z`: the release build of a clean HEAD ([release.md](release.md)). `mise run desktop:e2e`: webview e2e (display and WebKitGTK 4.1, or macOS). See [desktop.md](desktop.md).
- `mise run install-cli`: the CLI and stdio MCP binary (no web bundle) at `~/.local/bin/stage`. It is not an installer install: `stage update` refuses it and the installer will not overwrite it ([install.md](install.md#layout)).
- `mise run install:smoke`: the installer against real binaries and systemd with the isolated `stage-svctest` identity ([install.md](install.md#verification)).
- `mise run up` / `mise run down`: build and run, or stop, the compose stack (`compose.yaml`, data in the `stagedata` volume, kept on down).
- Single test files: `node --test packages/app/tests/<name>.test.mjs` (e.g. `tasks`, `journal`, `api-client`, `boundaries`) or `node --test apps/web/tests/<name>.test.mjs` (browser adapters: `transport`, `adapters`, `push-state`).

Layout: `server/` (Go server, CLI, MCP), `packages/app/` (the shared React app, `@stage/app`), `apps/web/` (browser host, `@stage/web`), `desktop/app/` (desktop host; its frontend `desktop/app/ui`, `@stage/desktop`, is in the pnpm workspace), `apps/website/` (marketing and docs site, `@stage/website`), one pnpm workspace. Ownership and the platform contract: [architecture.md](architecture.md). API contract: [api.md](api.md) (TypeScript mirror: `packages/app/src/api/types.ts`).

Development-only environment variables (user configuration: `.env.example`):

| Variable | Purpose |
| --- | --- |
| `T3_FAKE` | `1` uses an in-memory fake T3 (UI work without a T3 server). |
| `T3_FAKE_SEED` | With `T3_FAKE=1`, `1` seeds demo threads waiting on questions. |
| `STAGE_ENV` | `dev` (set by `mise run dev`): names the T3 session `stage dev` and refuses the default data dir. |

Guards: with `STAGE_ENV=dev` the server refuses the default data dir. Launches send a unique `clientRequestId` per attempt (`task-<id>-<uuid>`), so dev, test or restored databases that share one T3 cannot collide with a deployed instance's launches ([api.md](api.md)).

## Stage Dev installs

Stage Dev is the developer identity (`stage-dev`): its own app ("Stage
Dev"), CLI (`stage-dev`), service (`stage-dev.service` /
`io.github.flexdinesh.stage-dev`), socket, data dir and install root
(`~/.local/lib/stage-dev`). It never shares anything with the stable
install ([desktop.md](desktop.md#identities)), so both can be installed at
once. It is a persistent install of one exact commit, for living on a
build; day-to-day development (`mise run dev`, `mise run desktop:dev`)
needs no install at all ([below](#stage-dev-vs-stable-vs-dev-runs)).

## Requirements

- [mise](https://mise.jdx.dev) and git. mise installs the pinned Go, Node and pnpm (`mise.toml`) for the commit being built.
- Linux desktop: glibc, GTK 3 and WebKitGTK 4.1 at run time. Without them the install stops (exit 7) and prints the package command for your distribution; `--headless` needs none of them.
- macOS 12 or later (Xcode Command Line Tools for git).
- Not root. The script never uses `sudo` or installs packages.

## Install or update from a commit

```sh
curl -fsSL https://raw.githubusercontent.com/flexdinesh/stage/main/scripts/install-dev.sh | sh                       # latest origin/main
curl -fsSL https://raw.githubusercontent.com/flexdinesh/stage/main/scripts/install-dev.sh | sh -s -- <sha>            # that commit (full 40-hex SHA)
curl -fsSL https://raw.githubusercontent.com/flexdinesh/stage/main/scripts/install-dev.sh | sh -s -- --headless       # stage-dev CLI/server only
```

Piped, the script needs no checkout: it builds from a cached bare clone
(`${XDG_CACHE_HOME:-~/.cache}/stage-install-dev/repo.git`) of
`https://github.com/flexdinesh/stage.git` (`STAGE_DEV_REPO` overrides it,
for forks and tests), using gh's credentials when `gh` is logged in.

### From a checkout

The same script in `scripts/` builds from that checkout's `origin`
instead, and only there can it build the working tree:

```sh
scripts/install-dev.sh                 # latest origin/main
scripts/install-dev.sh <sha>           # that commit
scripts/install-dev.sh --working-tree  # this checkout's working tree
```

| Flag | Meaning |
| --- | --- |
| `SHA` | full 40-character commit hash; any commit origin has (main, a branch, a pull request head) |
| `--headless` | CLI/server only (no desktop app) |
| `--cli-name NAME` | the CLI's name in `~/.local/bin` (default `stage-dev`) |
| `--check` | run `mise run check` on the commit before building (slow) |
| `--force` | rebuild and reinstall even if that commit is already installed |
| `--working-tree` | build this checkout's working tree instead of a commit (no fetch; uncommitted changes marked `-dirty`); takes no SHA; checkout runs only (piped: exit 2) |

What it does:

1. `git fetch origin`, in the checkout or, piped, in the cache clone (created on the first run, its `origin` set to `STAGE_DEV_REPO`). A failed fetch stops it (exit 10): it never falls back to local branches.
2. Resolves the commit: `origin/main` when no SHA is given, else the SHA, which origin must have (contained in an origin branch, or served by origin when fetched by hash). A commit only in your local clone is refused (exit 10). It prints `commit <full sha>`.
3. If Stage Dev is already that commit (same mode and CLI name) it stops: "already installed" (exit 0). `--force` rebuilds.
4. Checks the commit has the build interface the script drives (`mise run dist` with `STAGE_DIST_OUT`, and `stage install --from`: commit `45792be` and later); older commits are refused (exit 7) before anything runs.
5. Checks the commit out into a temporary detached `git worktree` under `${XDG_CACHE_HOME:-~/.cache}/stage-install-dev/` (checkout hooks off). Your checkout, its branches, index and uncommitted work are never touched; only `origin/*` refs are updated. Piped runs touch only the cache clone, never the repository of the current directory.
6. In that worktree: `mise install`, optionally `mise run check`, then `mise run dist` (a `stage-dev` build: version `0.0.0-dev.g<sha12>`, the full commit in every binary, verified and smoke-run: [release.md](release.md#modes)). The built commit must be exactly the SHA.
7. The built `stage-dev` runs `stage-dev install --from <dist>`: the same checks, layout, locking, service restart and rollback as a release install ([install.md](install.md)), with Stage Dev's paths.
8. Removes the temporary worktree and build, also after a failure or Ctrl-C.

A failed fetch, refusal or build changes nothing: the installed Stage Dev
keeps running. A failure while installing is handled by the installer
(rollback or recovery: [install.md](install.md#recovery)).

**Refresh** by rerunning the script (no SHA: the newest `origin/main`).
Nothing updates in the background; `stage-dev update` refuses (Stage Dev
never installs releases, and a stable release never replaces it).

**Version.** `stage-dev version` shows the version (`0.0.0-dev.g<sha12>`),
the full commit and the install; `stage-dev service status` shows what the
service runs. A `--working-tree` build is `0.0.0-dev.<branch>` with
`-dirty` on the commit when the tree had changes.

**macOS.** The build is a local `Stage Dev.app` signed ad hoc on your Mac
(`~/Applications/Stage Dev.app`), never a signed or notarized public
release; Gatekeeper is not bypassed.

## Uninstall

```sh
curl -fsSL https://raw.githubusercontent.com/flexdinesh/stage/main/scripts/uninstall-dev.sh | sh                        # app, CLI link, desktop entry, service; data kept
curl -fsSL https://raw.githubusercontent.com/flexdinesh/stage/main/scripts/uninstall-dev.sh | sh -s -- --purge --yes    # also Stage Dev's data, config, logs, preferences
```

From a checkout: `scripts/uninstall-dev.sh [--purge] [--yes]`. Piped,
stdin is the script, so `--purge` cannot ask: pass `--yes` (without it,
it refuses, exit 2).

It runs the installed Stage Dev's own `uninstall` (`stage-dev uninstall`
does the same, [install.md](install.md#uninstall)). `--purge` then deletes
only these, after typing `yes` (or with `--yes`; without a terminal it
refuses), and refuses while `STAGE_DATA_DIR` is set:

| Linux | macOS |
| --- | --- |
| `${XDG_DATA_HOME:-~/.local/share}/stage-dev` (database, backups) | `~/Library/Application Support/stage-dev` (database, backups, `service.env`, socket) |
| `${XDG_CONFIG_HOME:-~/.config}/stage-dev` (`service.env`) | `~/Library/Logs/stage-dev` |
| `${XDG_CONFIG_HOME:-~/.config}/io.github.flexdinesh.stage-dev.desktop` (desktop preferences, notification settings, logs) | `~/Library/Application Support/io.github.flexdinesh.stage-dev.desktop`, `~/Library/Logs/io.github.flexdinesh.stage-dev.desktop` |
| `${XDG_CACHE_HOME:-~/.cache}/io.github.flexdinesh.stage-dev.desktop` | `~/Library/Caches/io.github.flexdinesh.stage-dev.desktop` |

Stable Stage, `mise run desktop:dev` (`.dev` dirs) and `mise run dev`
(`.scratch`) are never touched. With nothing installed it only purges.
On Linux, journald keeps the service's past log lines.

## Stage Dev vs stable vs dev runs

| | Stable Stage | Stage Dev (this page) | `mise run desktop:dev` | `mise run dev` |
| --- | --- | --- | --- | --- |
| Lifetime | installed release | installed commit, until uninstalled | while the task runs | while the task runs |
| CLI | `~/.local/bin/stage` | `~/.local/bin/stage-dev` | `.scratch/dev/<branch>/bin/stage` | `go run` |
| App, bundle id | Stage, `io.github.flexdinesh.stage.desktop` | Stage Dev, `io.github.flexdinesh.stage-dev.desktop` | Stage Dev, `....stage-dev.desktop.dev` | browser |
| Service | `stage.service` / `io.github.flexdinesh.stage` | `stage-dev.service` / `io.github.flexdinesh.stage-dev` | none (foreground) | none (foreground) |
| Socket | `$XDG_RUNTIME_DIR/stage/` | `$XDG_RUNTIME_DIR/stage-dev/` | `$XDG_RUNTIME_DIR/stage-desktopdev/<8 hex>.sock` | TCP `127.0.0.1:7390` |
| Data | `~/.local/share/stage` | `~/.local/share/stage-dev` | `.scratch/dev/<branch>/desktop` | `.scratch/dev/<branch>` |
| Desktop prefs, notifications | `~/.config/io.github.flexdinesh.stage.desktop` | `~/.config/io.github.flexdinesh.stage-dev.desktop` | `....stage-dev.desktop.dev` | |
| Deep links | `stage://` | `stage-dev://` | `stage-dev://` | |

macOS uses the `~/Library` equivalents ([runtime.md](runtime.md#identity-and-paths),
[service.md](service.md#paths-per-identity)). Notification permission on
macOS is per bundle id, so Stage and Stage Dev ask separately. Installing,
updating or removing Stage Dev never changes the stable binaries, database,
service, pairing or login setting.

## T3

Stage Dev has its own database, so it starts unpaired: nothing is copied
from the stable install. Connecting it to the same T3 is a deliberate step:
the Dashboard's T3 setup ("Pair with T3" when T3 and its `t3` CLI are on
this machine, else a code from `t3 pair`), Settings, or `stage-dev t3 pair
[--code CODE]`. Auto-pair labels the code `stage dev mcp`; a `mise run dev`
server pairs the same way. The T3 URL can be saved there too (`stage-dev t3
url URL`); a `T3_ADMIN_TOKEN`, `T3_URL` or `T3_BIN` goes in Stage Dev's own
`service.env` (`~/.config/stage-dev/service.env`, then `stage-dev service
restart`). Both installs then run agents on that T3 as separate clients;
each Stage server is bound to the T3 environment it first paired with.

## Troubleshooting

- **`git fetch origin failed`** (exit 10): network, or git credentials for origin (the repository is private; piped runs use gh's credentials, so `gh auth login`). Nothing was built; fix and rerun.
- **`origin has no commit`** (exit 10): the SHA is mistyped, abbreviated, or not pushed. Push it, or pass the full hash.
- **`predates the dev-install build interface`** (exit 7): the commit is older than `45792be`; pick a newer one.
- **`build failed`** (exit 1): the commit's `mise install`, `check` or `dist` failed (its output is above). Stage Dev was not changed. Try `--working-tree` on a checkout of that commit to debug.
- **`installed in desktop mode`** (exit 7): switching between desktop and `--headless` needs an uninstall first (the [curl command above](#uninstall) or `scripts/uninstall-dev.sh`).
- **A file it does not own** (exit 7), **another install running** (exit 3), **rolled back** (12) or **kept after migration** (13): see [install.md](install.md#exit-codes).
- **Leftover temporary worktree** (the script was killed with SIGKILL): `git worktree prune` in your checkout (piped runs: `git -C ~/.cache/stage-install-dev/repo.git worktree prune`) and delete `~/.cache/stage-install-dev/build.*`.

Exit codes: 0 done or already installed, 1 build or other failure, 2
usage (including a SHA that is not 40 hex characters), 3/11/12/13 from the
installer, 7 unsupported (root, missing mise or git, an old commit, a mode
switch, missing WebKitGTK), 10 fetch or unknown commit.

## Testing the installer

Never install into your real home to test: the tests use temp homes and the
isolated `stage-svctest` identity (`mise run check`, `mise run
install:smoke`; [install.md](install.md#verification)). `mise run check`
runs `install-dev.sh` and `uninstall-dev.sh` against a fixture origin with
stub `mise` and `stage` (`tools/release/install_dev_script_test.go`:
default `origin/main`, explicit and pull request SHAs, repeat and
`--force`, update, fetch failure, unknown, local-only, malformed and
unsupported revisions, build failure keeping the prior install, `--check`,
`--working-tree`, cleanup, purge paths on Linux and macOS, and piped
runs via `sh -s` with `STAGE_DEV_REPO` at the fixture: cache clone reuse,
SHAs, refusals, `--help`, a current directory inside a checkout, and
uninstall) and the
stable/dev coexistence tests (`server/internal/install/coexist_test.go`).
To try a real build by hand, point `HOME`, `XDG_DATA_HOME`,
`XDG_CONFIG_HOME` and `XDG_CACHE_HOME` at a scratch directory and put a
failing `systemctl` stub first on `PATH`, so no real service is registered.

## For successors

- The build interface between the script and a commit: the commit's `mise run dist` honors `STAGE_DIST_OUT`, writes `stage-release.json` with `version` and `commit`, and `stage-dev-<v>-<os>-<arch>.tar.gz` with `<base>/stage`, whose `install --from DIR --desktop|--headless [--cli-name NAME]` installs it. The gate in `scripts/install-dev.sh` probes for it; a change to that interface needs a new probe and must keep older supported commits building.
- Piped mode is chosen when `$0` is not an `install-dev.sh` file inside a git checkout (under `curl | sh`, `$0` is the shell). `STAGE_DEV_REPO` is the seam the tests use to point the cache clone at a fixture origin.
- Not verified: macOS (paths are covered by fixtures only) and a real run of the script into a home.
