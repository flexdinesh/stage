# Install, update and uninstall

Stage installs per user, without root, from its GitHub releases: the
desktop app with its CLI/server (Linux, macOS when a release ships the
Mac app), or the headless CLI/server for remote machines. One authority,
the release's own `stage install`, does every install, reinstall and update,
so the desktop app, the CLI on `PATH` and the per-user service always run
one version. Code: `scripts/install.sh`, `server/internal/install`,
`server/internal/cli/install.go`. Releases: [release.md](release.md).
Developer (Stage Dev) installs: [development.md](development.md).

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/flexdinesh/stage/main/scripts/install.sh | sh             # desktop
curl -fsSL https://raw.githubusercontent.com/flexdinesh/stage/main/scripts/install.sh | sh -s -- --headless
```

While the repository is private, fetch the script with an authenticated
[`gh`](https://cli.github.com) (the script then downloads through `gh` too):

```sh
gh api repos/flexdinesh/stage/contents/scripts/install.sh -H 'Accept: application/vnd.github.raw' | sh
gh api repos/flexdinesh/stage/contents/scripts/install.sh -H 'Accept: application/vnd.github.raw' | sh -s -- --headless
```

| Flag | Meaning |
| --- | --- |
| `--headless` | CLI/server only (no desktop app, no GTK/WebKit needed) |
| `--version X.Y.Z` | install that version (the only way to get a pre-release) |
| `--cli-name NAME` | the CLI's name in `~/.local/bin` (default `stage`) |

The script detects the OS (Linux, macOS) and CPU (amd64, arm64), finds the
latest stable release (not a draft or pre-release: GitHub's
`releases/latest`), downloads `stage-release.json`, `checksums.txt` and the
one archive this machine needs into a temp dir, checks its sha256 and its
member paths, unpacks only the release's `stage`, and runs
`stage install --from <dir>`. That command:

1. checks the manifest (schema, identity `stage`, channel `stable`, version, this platform), that `checksums.txt` lists exactly the manifest's artifacts with the same hashes, the archive's size and sha256, and that it is itself that release's CLI;
2. takes the install lock, unpacks into a staging dir next to the app (safe paths only: no absolute or `..` names, no links leaving the tree, no devices), checks the unpacked CLI against the manifest's `cli.sha256`, runs it (`version --json`) and, for the desktop, the app's selfcheck (`STAGE_DESKTOP_SELFCHECK=1`);
3. refuses files it does not own (below) and, if the running server's database is newer than the release knows, refuses the release;
4. only then disrupts anything: quits the desktop app, swaps the app in, points the CLI link at it, registers the desktop entry, restarts the service if it was running this install, checks the server reports the new version, and reopens the desktop app.

**Integrity.** Releases are not signed ([release.md](release.md#integrity-and-updates)). The checks above catch corrupt, truncated or substituted downloads and wrong-platform or wrong-identity releases; trust in the release itself rests on the GitHub account and HTTPS. Any failed check exits 11 with nothing written outside the staging dir.

**Requirements.** Linux desktop: glibc, GTK 3 and WebKitGTK 4.1. When they are missing the install stops (exit 7) and prints the command for your distribution (`apt-get`, `dnf`, `pacman`, `zypper`); it never installs packages itself. The headless CLI/server needs nothing. macOS 12 or later; releases ship the Mac desktop only once it can be signed and notarized, until then use `--headless` on a Mac.

## Layout

| What | Linux | macOS |
| --- | --- | --- |
| Install root (record, lock, staging; the headless app) | `~/.local/lib/stage/` | same |
| Desktop app | `~/.local/lib/stage/app/` (`stage-desktop`, `bin/stage`, icon, `.desktop`) | `~/Applications/Stage.app` |
| Headless app | `~/.local/lib/stage/app/bin/stage` | same |
| Previous version (one, for rollback) | `~/.local/lib/stage/app.prev` | `~/Applications/Stage.app.prev` (headless: `app.prev`) |
| CLI on `PATH` | `~/.local/bin/stage` -> the app's `bin/stage` | same |
| Desktop entry (launcher, icon, `stage://` handler) | `$XDG_DATA_HOME/applications/stage-desktop.desktop` (`Exec` and `Icon` are absolute paths into the app) | the bundle's `Info.plist` (registered with Launch Services) |
| Install record | `~/.local/lib/stage/install.json` (0600) | same |

The app's path never changes across updates, so the service definition,
the desktop's login item and the desktop entry keep working. Data, config,
`service.env`, logs, database backups and desktop preferences are never
under the install root ([runtime.md](runtime.md#identity-and-paths),
[service.md](service.md#paths-per-identity)) and nothing here removes them.

`install.json` records the identity, channel, mode, version, commit, CLI
name and the files outside the app the installer wrote (the CLI link and the
desktop entry) with their fingerprints. **Ownership:** an existing file at
one of those paths is replaced only when it is the recorded one, unchanged
(or the link already points at the app). Anything else (an old `stage`
from `mise run install-cli`, MyGo's `stage-desktop-install.sh` install, a
hand-edited entry) stops the install (exit 7) with the fix: remove it, or
pick another `--cli-name`.

Agent instructions (the launch `<stage-journal>` block, MCP server
instructions, `stage guide`, `stage setup instructions`) name the CLI by the
recorded CLI name, else the identity, so agents run the CLI on `PATH`. The
server reads it at start; an install restarts the service, picking up a new
`--cli-name`.

## Update

```sh
stage update            # latest stable release, same mode (headless stays headless)
stage update --check    # installed, latest and running server versions
stage update --version 1.4.0      # a specific version, also a pre-release or an older one
stage update --from DIR           # a downloaded asset directory (offline)
```

Updates are explicit: nothing checks or updates in the background. `stage
update` refuses binaries the installer did not install (system packages:
use the package manager; development builds), and refuses Stage Dev, which
updates from source ([development.md](development.md)); a Stage Dev install
can never become stable, or the other way round. It downloads and verifies
the release into the install root's staging dir (with `gh`'s login or
`GH_TOKEN`/`GITHUB_TOKEN` through the GitHub API, else anonymously through
`releases/latest`, which has no API rate limit), unpacks that release's CLI,
and runs its `stage install --from` (above), so the new version's own logic
installs it. Rerunning the install script does the same.

**What restarts.** The service restarts only if it was running this
install's CLI (`stage service restart --exe <app>/bin/stage`, which drains
in-flight requests for up to 100 s; a restart relaunches no task,
`RecoverLaunching`). T3 and its running agents are never touched; agent
commands during the restart briefly see "nothing answers". A stopped or
uninstalled service stays so (the desktop starts it on launch; headless:
`stage service start`). A foreground `stage serve` or a service running
another executable is left alone, with a note to restart it. A running
desktop app is quit with SIGTERM (a normal Quit: it flushes its notification
cursor) and reopened afterwards when this session has a display; otherwise
reopen it. Login startup (`stage service enable`, "Open Stage at login"),
pairing and all data are untouched.

**Locking.** One install, update or uninstall runs at a time per identity
(`install.lock`, flock): a second one exits 3 at once. The desktop's own
`stage service start` is serialized with the restart by the service's lock.

**Reinstall** of the installed version with an intact app only repairs the
CLI link and desktop entry; a damaged app is replaced.

## Recovery

Updates migrate the database forward when the new server starts. Before it
applies migrations to an existing database, the server copies it to
`<data dir>/backups/stage-schema<N>-<UTC time>.db` (`VACUUM INTO`, mode
0600, newest 3 kept); if that copy fails (for example a full disk) it does
not migrate and exits. A server refuses to start (exit 78, not restarted by
systemd) on a database whose schema is newer than it knows, so an older
binary never runs against a migrated database ([runtime.md](runtime.md)).

If the new server does not come back after an update:

- **No new migrations** (the old and new versions know the same schema): the installer moves the previous version back, restarts it and exits 12 (rolled back). Nothing else to do; report the log lines it printed.
- **New migrations:** it keeps the new version and exits 13 with the log tail and the backup path. Rolling the binaries back would not help: the old server refuses the migrated database. Look at `stage service logs`, fix the cause (often `service.env`) and run `stage service restart`. To go back to the old version: `stage service stop`, copy the backup over `<data dir>/stage.db` (and delete `stage.db-wal` and `stage.db-shm` next to it), then `stage update --version <old>` (or the install script with `--version`) and `stage service start`. Writes made after the migration are lost that way.

An interrupted install (power loss, kill) is finished or undone by the next
install, update or uninstall, which reads the record's `activating` state.

## Diagnostics

`stage version [--json]` shows the binary's identity, version, commit, API
version and schema, and the installation it belongs to (`install`: mode,
version, channel, app, CLI, link). `stage update --check` adds the latest
release and the running server. `stage service status` shows the service's
executable and server version (`version_differs`, `exe_differs`).

## Uninstall

```sh
stage uninstall
```

Quits the desktop app, removes the service definition (`stage service
uninstall`), the app and the previous version, the CLI link and desktop
entry (only if unchanged), MyGo's URL-handler and login entries that point
into the app, and the install root. It keeps and lists the data dir, config
(`service.env`), logs, database backups and desktop preferences; delete
them yourself to remove everything. On macOS 13+, remove a leftover login
item in System Settings > General > Login Items.

## Service

The desktop app starts the per-user service itself. On a headless install:

```sh
stage service start     # register and start (systemd --user / launchd)
stage service enable    # optional: start at login
stage service status
```

See [service.md](service.md). Remote browsers reach the server over TCP
(`STAGE_LISTEN=unix,127.0.0.1:7380` in `service.env`, behind an
authenticating proxy: [runtime.md](runtime.md#request-origin-policy)).

## Exit codes

`stage install`, `update` and `uninstall` (and `install.sh`):

| Exit | Meaning |
| --- | --- |
| 0 | done, or already up to date |
| 1 | failed (disk full, permission denied, the desktop did not quit, ...; nothing changed unless said) |
| 2 | usage |
| 3 | another install, update or uninstall is running |
| 7 | unsupported: not an installer install, a file it does not own, wrong platform, missing requirements, root, a release older than the database |
| 10 | network: offline, GitHub rate limit, release or asset not found (a private repository needs `gh auth login`) |
| 11 | verification failed (corrupt, truncated or tampered asset, wrong identity or channel, unsafe archive); nothing written outside staging |
| 12 | the new version did not start; rolled back |
| 13 | the new version did not start; kept (migrated), see [recovery](#recovery) |

All three take `--json` and print one result document
(`{ok, code, message, fix, version, previous, mode, changed, service, desktop, cli, link, app, backup, notes, warnings, logTail, retained}`).

## Verification

- `mise run check` runs `server/internal/install` tests (fixture releases with stub binaries, temp homes, a fake GitHub: install, desktop registration, reinstall, update with service and desktop restart, headless kept, mode switch refused, tampered / truncated / missing assets, checksum and manifest disagreement, wrong identity, channel and platform, unsafe archives and link chains, collisions, the lock, concurrent updates, interrupted swap recovery, rollback vs keep by schema, older-than-database refusal, missing WebKit, disk full, desktop that does not quit, uninstall keeping data, GitHub redirect and API paths, rate limit, 404, offline) and `scripts/install.sh` tests (stub `uname`/`id`/`gh`, fixture server: platform and asset selection, gh vs anonymous, tamper, unsafe members, pre-release latest, pins, root, usage).
- `mise run install:smoke` (Linux with a systemd user manager): real binaries with the isolated `stage-svctest` identity in a scratch home: install, `stage service start`, `stage update --from` (version-coherent restart onto the app's CLI, new pid), reinstall no-op, a real failed restart rolled back, uninstall keeping data and removing the unit; then a real `mise run dist` Stage Dev build installed into a temp home with no service manager (desktop, or headless where WebKitGTK is missing), reinstalled and uninstalled.
- Not verified: macOS (the `.app` swap, Launch Services, `ps` matching, launchd), linux-arm64 natively, a real GitHub download (no release published yet).
