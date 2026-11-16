# Releases

One release is one version and one commit for everything Stage ships: the
desktop app (`desktop/app`), the CLI/server bundled inside it and the
headless CLI/server for remote machines, with the web UI embedded in every
CLI/server binary. There are no separate desktop and server release
schedules. A release is built from an exact revision with the pinned
toolchain (`mise.toml`: Go, Node, pnpm, shellcheck; MyGo is a Go tool in
`desktop/app/go.mod`), then verified. Nothing in the repository uploads,
tags, installs or self-updates; installing (`install.sh`, `install-dev.sh`)
is the next task, and it reads the contract below.

## Modes

| Command | What | Identity, version | Source | Output |
| --- | --- | --- | --- | --- |
| `mise run dist` | developer build | `stage-dev`, `0.0.0-dev.<branch>` (`0.0.0-dev.g<sha12>` detached); commit `-dirty` when the tree is | the working tree | `STAGE_DIST_OUT`, default `.scratch/dist/<version>` |
| `mise run release VERSION=X.Y.Z` | release build | `stage`, `X.Y.Z` or `X.Y.Z-pre` | `git archive HEAD` in a temp dir (tracked files only), HEAD must have no uncommitted changes and tag `vX.Y.Z`, if it exists, must be HEAD | `STAGE_RELEASE_OUT`, default `.scratch/release/v<version>` |
| `mise run release:verify DIR=<dir>` | verify an asset directory | as built | | |

- `dist` builds this machine's targets (`TARGETS=host`, the default: `linux-<arch>`, or both darwin CLIs and `darwin-universal` on a Mac) or every target (`TARGETS=all`, which replaced `desktop:package`). It skips the DMG. An earlier `dist` in the same directory is replaced; any other non-empty directory is refused. It prints `version <v>` and `out <dir>` last, the contract `install-dev.sh` will use.
- `release` builds every CLI target and the Linux desktop targets. The macOS desktop needs a Mac with `MACOS_SIGNING_IDENTITY` (a Developer ID Application identity) and `MACOS_NOTARY_PROFILE` (an `xcrun notarytool store-credentials` profile); otherwise it is left out and the manifest records why (`omitted`).
- Pre-release versions use lowercase letters, digits and dots (`1.2.0-rc.1`); there is no build metadata, so package formats need no escaping.
- Both run `scripts/release.sh`, which builds, then `tools/release assemble` and `tools/release verify -smoke`. `scripts/release.sh` and `tools/release` (Go standard library only) are the whole implementation.

Both builds embed the web UI: `web/dist` is built once and copied into `server/internal/webui/dist` before the CLI/server is compiled (`CGO_ENABLED=0 -trimpath -ldflags "-s -w -X ..."`) for each target. The desktop's bundled CLI is the same file as the headless one for that target (byte for byte; a universal macOS CLI's two slices are the darwin binaries).

## Targets

| Target | Desktop | Headless | Built on |
| --- | --- | --- | --- |
| linux-amd64 | tar.gz (+ `install.sh`), deb, rpm, PKGBUILD | tar.gz | any machine |
| linux-arm64 | same | tar.gz | any machine (cross-compiled) |
| darwin-arm64, darwin-amd64 | (universal) | tar.gz | any machine (cross-compiled) |
| darwin-universal | tar.gz of the `.app`, DMG | | release: a Mac with Developer ID; dev: anywhere (unsigned off a Mac) |

Linux desktop: glibc, GTK 3 and WebKitGTK 4.1 (the packages depend on them). macOS 12.0 or later. The headless CLI/server needs nothing (static, no GTK or WebKit). Windows is out of scope ([ADR 0001](adr/0001-desktop-shell.md)).

**A cross-compiled artifact is not natively validated.** Verification checks its contents, names, hashes and build stamp, but only the host's own targets are run.

## Assets

`<id>` is the identity (`stage` or `stage-dev`), `<slug>` is `<id>-desktop`, `<v>` the version, `<debv>`/`<rpmv>` the version with `-` as `~`.

| Asset | Kind | Bundled CLI (`cli.path`) |
| --- | --- | --- |
| `<id>-<v>-<os>-<arch>.tar.gz` | headless CLI/server: `<id>-<v>-<os>-<arch>/{stage,LICENSE,README.md}` | `<id>-<v>-<os>-<arch>/stage` |
| `<slug>-<v>-linux-<arch>.tar.gz` | desktop (MyGo's archive: the app executable, `bin/stage`, `LICENSE`, `.desktop` file) | `bin/stage` (next to the executable) |
| `<slug>_<debv>_<arch>.deb`, `<slug>-<rpmv>-1.<x86_64\|aarch64>.rpm` | desktop, installs to `/opt/<slug>` | `/opt/<slug>/bin/stage` |
| `<slug>-<v>-linux-<arch>.PKGBUILD` | desktop for Arch (`makepkg -si` next to the tarball, renamed to `PKGBUILD`) | |
| `<slug>-install.sh` | MyGo's per-user installer for the Linux tarball (`~/.local/<slug>.app`) | |
| `<slug>-<v>-darwin-universal.tar.gz` | desktop: `<Name>.app` | `<Name>.app/Contents/Resources/bin/stage` |
| `<slug>-<v>.dmg` | desktop (signed, notarized releases only) | |
| `stage-release.json` | the manifest | |
| `checksums.txt` | `shasum -a 256` lines for every artifact | |

These names are the installer contract: change them only together with the installers. The desktop finds its CLI at `App.Path(PathResources)/bin/stage` (`Contents/Resources` on macOS, the executable's directory on Linux).

## Manifest

`stage-release.json` (schema 1):

```json
{
  "schema": 1,
  "name": "stage",
  "version": "0.1.0",
  "commit": "<40 hex, -dirty only for dev>",
  "date": "<commit date, RFC 3339>",
  "identity": "stage",
  "channel": "stable",
  "compatibility": {
    "apiVersion": 1,
    "macos": {"minimum": "12.0"},
    "linux": {"libc": "glibc", "desktopDepends": ["libgtk-3-0", "libwebkit2gtk-4.1-0"]},
    "note": "The desktop app and its bundled CLI are one build. Clients accept a server with the same apiVersion; another version shows a skew banner."
  },
  "artifacts": [
    {"name": "stage-0.1.0-linux-amd64.tar.gz", "kind": "cli", "format": "tar.gz", "os": "linux", "arch": "amd64",
     "size": 123, "sha256": "...", "cli": {"path": "stage-0.1.0-linux-amd64/stage", "sha256": "..."}}
  ],
  "omitted": [{"kind": "desktop", "os": "darwin", "arch": "universal", "reason": "needs a Mac with Developer ID signing and notarization (...)"}]
}
```

- `kind`: `cli`, `desktop` or `installer`; `format`: `tar.gz`, `deb`, `rpm`, `pkgbuild`, `dmg` or `sh`; `arch`: `amd64`, `arm64`, `universal`, or absent for any.
- `channel` is `stable` (identity `stage`, never dirty) or `dev` (identity `stage-dev`).
- `apiVersion` comes from running this machine's CLI (`stage version --json`); every binary of a build shares it.
- `cli.sha256` pins the bundled CLI, so an installer can check what it puts on `PATH` matches the headless binary.

## Build stamp

Go leaves `-ldflags` out of the build info under `-trimpath`, so release builds link a stamp into every CLI/server (`buildinfo.Stamp`) and desktop executable (`main.appBuild`):

```
stage-build/1 identity=<id> version=<v> commit=<sha>;
```

Verification reads it from binaries it cannot run (other architectures, each slice of a universal binary). `stage version --json` reports it as `build`, and the desktop's selfcheck does too. Unstamped builds (`go build`, `mise run build`) have none.

## Verification

`tools/release verify [-smoke] DIR` (run by `dist` and `release`):

- the manifest is valid, `checksums.txt` and the directory list exactly its artifacts, and every size and SHA-256 matches;
- every name is the one the contract above derives;
- every CLI/server and desktop executable carries this build's stamp; each bundled CLI is the headless binary for its target (universal: both slices);
- deb control (`Package`, `Version`, `Architecture`), PKGBUILD (`pkgver`, source, checksum) and `Info.plist` (`CFBundleShortVersionString`, `CFBundleVersion`) carry the version;
- every tar.gz and the deb payload hold `LICENSE` and no denied path: `.env*`, `*.db`, `*.db-wal`, `*.db-shm`, `*.sqlite*`, `*.key`, `*.pem`, `*.p12`, `*.pfx`, SSH keys, `service.env`, `.netrc`, `.npmrc`, or anything under `.scratch`, `.git`, `node_modules`, `.mygo`. The rpm packs the same staged files as the tarball and deb and is checked by name and hash only;
- `-smoke`, for this machine's targets: the headless `stage version --json`; `stage serve` on a temp socket with a temp `HOME`, XDG dirs and data dir and fake T3 (`/api/health` identity, version, commit, apiVersion, and `/` serving the embedded web app); and the desktop app with `STAGE_DESKTOP_SELFCHECK=1`, which resolves `<resources>/bin/stage` as the launcher does, runs it and exits before any window, lock or service (it needs no display).

Release builds also exclude local state by construction: they build from `git archive HEAD`, so `.env`, `.env.dev`, `.scratch` (dev databases), build outputs and keys never reach the build tree.

### Developer vs release builds

| | `dist` | `release` |
| --- | --- | --- |
| Layout, names, checksums, stamps, bundled CLI, denylist, licenses | verified | verified |
| Identity | `stage-dev` (own app id, CLI identity, service, socket, data dir) | `stage` |
| Exact clean revision | no (working tree, `-dirty` marked) | yes (`git archive HEAD`) |
| macOS signing, notarization, DMG | ad hoc on a Mac; unsigned when cross-built | Developer ID + notarized DMG, on a Mac only |
| Run natively | host targets only | host targets only (the workflow: linux-amd64) |
| Signatures | none | none |

## Integrity and updates

Releases are not signed. `checksums.txt` and the manifest's hashes catch a corrupted download, not a tampered release: trust rests on the GitHub account and HTTPS (as Zero does). Updates mean running the install script again. MyGo's signed self-update (`updates` in `mygo.config.ts`, an Ed25519 key) is deliberately not configured; turning it on later needs a key the maintainer owns, and apps installed before then reinstall once.

The repository is private for now: downloading release assets needs an authenticated `gh`.

## macOS

There is no Developer ID yet, so releases ship only the headless darwin CLIs; on a Mac, use a developer build (`mise run dist` on the Mac builds and ad-hoc signs the `.app`). Gatekeeper is never bypassed as a way to distribute. With a Developer ID: on the Mac, `security find-identity -v -p codesigning`, `xcrun notarytool store-credentials <profile>`, then `MACOS_SIGNING_IDENTITY="Developer ID Application: ..." MACOS_NOTARY_PROFILE=<profile> mise run release VERSION=...`; MyGo signs the bundle and its `bin/stage` inside-out with the hardened runtime, notarizes and staples the DMG. Credentials stay in the keychain, never in the repository.

## Publishing (manual)

The maintainer creates releases. Two equivalent ways:

1. **GitHub Actions**, `release` workflow (`.github/workflows/release.yml`), Run workflow on `main` with a version. It refuses a bad version or an existing tag or release, builds that exact commit with `mise run release` on ubuntu-24.04 (Linux desktop + every headless CLI; the linux-amd64 smoke runs there), and creates a **draft** release `v<version>` targeting the commit with every asset and generated notes (pre-release versions are marked pre-release). Review it and publish it in the web UI; publishing creates the tag. It needs no secrets. It has no other triggers.
2. **Locally**, from a clean `main`: `mise run release VERSION=0.1.0`, then `gh release create v0.1.0 --draft --target "$(git rev-parse HEAD)" --generate-notes .scratch/release/v0.1.0/*`.

Never rebuild a published version: publish a new patch instead.

## For successors

- The installer task reads `stage-release.json` and `checksums.txt`, and installs the CLI from the headless archive or the desktop's `cli.path`. `dist`'s `STAGE_DIST_OUT`, `TARGETS` and its last two lines are the `install-dev.sh` contract.
- Self-update needs: a key (`go tool mygo keygen`), `updates` in `mygo.config.ts`, an updater in the app, and coordinated service restarts (the service runs the bundled CLI by path; [service.md](service.md)).
- The macOS release path is implemented but unverified until a Mac with a Developer ID runs it.
