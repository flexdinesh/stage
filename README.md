# stage

Self-hosted task manager for [T3 Code](https://github.com/pingdotgg/t3code). Queue tasks, launch them as T3 threads, run them as dependency trees, schedule them, keep journals and final replies, and open T3 when work needs you. A desktop app or a headless server beside T3, with an installable PWA and Web Push.

stage requires a running T3 Code server. Conversations stay in T3; every task links straight to its thread.

## Install

Linux and macOS, per user, no root. The desktop app includes the CLI/server; `--headless` installs the CLI/server only, for remote machines (and Macs until the Mac desktop ships).

```sh
curl -fsSL https://raw.githubusercontent.com/flexdinesh/stage/main/scripts/install.sh | sh                  # desktop
curl -fsSL https://raw.githubusercontent.com/flexdinesh/stage/main/scripts/install.sh | sh -s -- --headless  # CLI/server only
```

While the repository is private, use an authenticated `gh`: `gh api repos/flexdinesh/stage/contents/scripts/install.sh -H 'Accept: application/vnd.github.raw' | sh`.

`stage update` installs the latest release (same mode), `stage update --check` compares versions, and `stage uninstall` removes stage and keeps your data. Headless: `stage service start` runs the server as a per-user service. Then pair with T3 from the Dashboard.

## HTTPS and push notifications

Push notifications and installing the PWA need a secure context. `http://localhost` is one, so both work in a browser on the stage machine with no setup. Other devices (phone, laptop) need HTTPS with a certificate they trust, for example:

```sh
tailscale serve --bg http://127.0.0.1:7380    # https://<machine>.<tailnet>.ts.net, tailnet only
```

or Caddy (`reverse_proxy 127.0.0.1:7380`) with a real or mkcert certificate. Add that HTTPS origin to `STAGE_PUBLIC_URL` or `STAGE_ALLOWED_ORIGINS`, or stage answers `403 host … not allowed`. Then open the URL, add it to the Home Screen (iOS 16.4+) or Dock (macOS Safari), and enable Settings, Notifications. Full setup, proxy streaming settings and the security model: [Headless server](https://stage.hoplabs.dev/docs/deploy#https-and-push-notifications).

## Docs

User guides: [stage.hoplabs.dev/docs](https://stage.hoplabs.dev/docs) (install, connect to T3, desktop app, headless server, features, agents).

Engineering contracts:

- [architecture.md](docs/architecture.md): ownership, UI hosts, platform contract, documentation ownership
- [api.md](docs/api.md): REST API, MCP and CLI contract
- [features.md](docs/features.md): detailed feature behavior
- [install.md](docs/install.md), [release.md](docs/release.md): installer, updates, release assets
- [runtime.md](docs/runtime.md), [service.md](docs/service.md): server runtime, listeners, origin policy, per-user service
- [desktop.md](docs/desktop.md): desktop app
- [development.md](docs/development.md): development tasks and Stage Dev installs

## License

[MIT](LICENSE)
