# Development installs (Stage Dev)

Stage Dev is the developer identity (`stage-dev`): its own app ("Stage
Dev"), CLI (`stage-dev`), service (`stage-dev.service` /
`io.github.flexdinesh.stage-dev`), socket, data dir and install root
(`~/.local/lib/stage-dev`). It never shares anything with the stable
install ([desktop.md](desktop.md#identities)), so both can be installed at
once. Day-to-day development (`mise run dev`, `mise run desktop:dev`) needs
no install at all: see the README's Develop section.

## Install or update from a checkout

```sh
scripts/install-dev.sh                 # Stage Dev desktop app + stage-dev CLI/server
scripts/install-dev.sh --headless      # stage-dev CLI/server only
scripts/install-dev.sh --cli-name NAME # another name in ~/.local/bin
```

It runs `mise run dist` for this machine into a temp dir (identity
`stage-dev`, version `0.0.0-dev.<branch>`, the working tree with `-dirty`
marked: [release.md](release.md#modes)), then the built `stage-dev` runs
`stage-dev install --from <dist>`: the same authority, checks, layout,
locking, service restart and rollback as a release install
([install.md](install.md)), with Stage Dev's paths. Rerun it to update to
the current checkout; an unchanged build only repairs links.

`stage-dev update` refuses: Stage Dev never installs releases, and a stable
release never replaces it. Remove it with `stage-dev uninstall` (data kept).

## Testing the installer

Never install into your real home to test: the tests use temp homes and the
isolated `stage-svctest` identity (`mise run check`, `mise run
install:smoke`; [install.md](install.md#verification)). To try a release
build by hand, point `HOME`, `XDG_DATA_HOME` and `XDG_CONFIG_HOME` at a
scratch directory and put a failing `systemctl` stub first on `PATH`, so no
real service is registered.
