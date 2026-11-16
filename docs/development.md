# Development installs (Stage Dev)

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

Run from any Stage checkout (the script is in `scripts/`):

```sh
scripts/install-dev.sh            # latest origin/main
scripts/install-dev.sh <sha>      # that commit (full 40-hex SHA)
scripts/install-dev.sh --headless # stage-dev CLI/server only
```

| Flag | Meaning |
| --- | --- |
| `SHA` | full 40-character commit hash; any commit origin has (main, a branch, a pull request head) |
| `--headless` | CLI/server only (no desktop app) |
| `--cli-name NAME` | the CLI's name in `~/.local/bin` (default `stage-dev`) |
| `--check` | run `mise run check` on the commit before building (slow) |
| `--force` | rebuild and reinstall even if that commit is already installed |
| `--working-tree` | build this checkout's working tree instead of a commit (no fetch; uncommitted changes marked `-dirty`); takes no SHA |

What it does:

1. `git fetch origin`. A failed fetch stops it (exit 10): it never falls back to local branches.
2. Resolves the commit: `origin/main` when no SHA is given, else the SHA, which origin must have (contained in an origin branch, or served by origin when fetched by hash). A commit only in your local clone is refused (exit 10). It prints `commit <full sha>`.
3. If Stage Dev is already that commit (same mode and CLI name) it stops: "already installed" (exit 0). `--force` rebuilds.
4. Checks the commit has the build interface the script drives (`mise run dist` with `STAGE_DIST_OUT`, and `stage install --from`: commit `45792be` and later); older commits are refused (exit 7) before anything runs.
5. Checks the commit out into a temporary detached `git worktree` under `${XDG_CACHE_HOME:-~/.cache}/stage-install-dev/` (checkout hooks off). Your checkout, its branches, index and uncommitted work are never touched; only `origin/*` refs are updated.
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
scripts/uninstall-dev.sh           # app, CLI link, desktop entry, service; data kept
scripts/uninstall-dev.sh --purge   # also Stage Dev's data, config, logs, preferences (asks first)
scripts/uninstall-dev.sh --purge --yes
```

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
pair it in Stage Dev's Settings, or with a code from `t3 pair`. A
`T3_ADMIN_TOKEN` or `T3_URL` goes in Stage Dev's own `service.env`
(`~/.config/stage-dev/service.env`, then `stage-dev service restart`). Both
installs then run agents on that T3 as separate clients.

## Troubleshooting

- **`git fetch origin failed`** (exit 10): network, or git credentials for origin (the repository is private). Nothing was built; fix and rerun.
- **`origin has no commit`** (exit 10): the SHA is mistyped, abbreviated, or not pushed. Push it, or pass the full hash.
- **`predates the dev-install build interface`** (exit 7): the commit is older than `45792be`; pick a newer one.
- **`build failed`** (exit 1): the commit's `mise install`, `check` or `dist` failed (its output is above). Stage Dev was not changed. Try `--working-tree` on a checkout of that commit to debug.
- **`installed in desktop mode`** (exit 7): switching between desktop and `--headless` needs `scripts/uninstall-dev.sh` first.
- **A file it does not own** (exit 7), **another install running** (exit 3), **rolled back** (12) or **kept after migration** (13): see [install.md](install.md#exit-codes).
- **Leftover temporary worktree** (the script was killed with SIGKILL): `git worktree prune` in your checkout and delete `~/.cache/stage-install-dev/`.

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
`--working-tree`, cleanup, purge paths on Linux and macOS) and the
stable/dev coexistence tests (`server/internal/install/coexist_test.go`).
To try a real build by hand, point `HOME`, `XDG_DATA_HOME`,
`XDG_CONFIG_HOME` and `XDG_CACHE_HOME` at a scratch directory and put a
failing `systemctl` stub first on `PATH`, so no real service is registered.

## For successors

- The build interface between the script and a commit: the commit's `mise run dist` honors `STAGE_DIST_OUT`, writes `stage-release.json` with `version` and `commit`, and `stage-dev-<v>-<os>-<arch>.tar.gz` with `<base>/stage`, whose `install --from DIR --desktop|--headless [--cli-name NAME]` installs it. The gate in `scripts/install-dev.sh` probes for it; a change to that interface needs a new probe and must keep older supported commits building.
- Not verified: macOS (paths are covered by fixtures only) and a real run of the script into a home.
