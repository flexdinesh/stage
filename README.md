# stage

Stage is a self-hosted task manager for T3 Code and OpenCode V2. Choose a harness for each task, schedule work, and link tasks across projects. Review the final reply in Stage and complete the task to make its dependents ready.

Connect an existing T3 service, an OpenCode 2.3.0 service, or both in Settings. Stage keeps projects, journals and accepted results available when a service is offline. Questions and approvals open in the task's harness.

## Install

Linux and macOS, per user, no root. The desktop app includes the CLI/server; `--headless` installs the CLI/server only, for remote machines (and Macs until the Mac desktop ships).

```sh
curl -fsSL https://raw.githubusercontent.com/flexdinesh/stage/main/scripts/install.sh | sh                  # desktop
curl -fsSL https://raw.githubusercontent.com/flexdinesh/stage/main/scripts/install.sh | sh -s -- --headless  # CLI/server only
```

While the repository is private, use an authenticated `gh`: `gh api repos/flexdinesh/stage/contents/scripts/install.sh -H 'Accept: application/vnd.github.raw' | sh`.

`stage update` installs the latest release (same mode), `stage update --check` compares versions, and `stage uninstall` removes stage and keeps your data. Headless: `stage service start` runs the server as a per-user service. Then open Settings to connect your harness.

## HTTPS and push notifications

Push notifications and installing the PWA need a secure context. `http://localhost` is one, so both work in a browser on the stage machine with no setup. Other devices (phone, laptop) need HTTPS with a certificate they trust, for example:

```sh
tailscale serve --bg http://127.0.0.1:7380    # https://<machine>.<tailnet>.ts.net, tailnet only
```

or Caddy (`reverse_proxy 127.0.0.1:7380`) with a real or mkcert certificate. Add that HTTPS origin to `STAGE_PUBLIC_URL` or `STAGE_ALLOWED_ORIGINS`, or stage answers `403 host … not allowed`. Then open the URL, add it to the Home Screen (iOS 16.4+) or Dock (macOS Safari), and enable Settings, Notifications. Full setup, proxy streaming settings and the security model: [Headless server](https://stage.hoplabs.dev/docs/deploy#https-and-push-notifications).

## Docs

User guides: [stage.hoplabs.dev/docs](https://stage.hoplabs.dev/docs) (install, connect a harness, desktop app, headless server, features, agents).

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
