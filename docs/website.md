# Website deployment

The marketing site and public user guides (`apps/website`, `@stage/website`,
[architecture.md](architecture.md)) are served at `https://stage.hoplabs.dev`
by the Cloudflare Worker `stage-website` (Workers static assets,
`apps/website/wrangler.jsonc`). This repository's `main` branch is the only
source: Cloudflare Workers Builds builds and deploys it on every push to
`main` that touches a watch path. Nothing else deploys the site.

## Source of truth

- Repository `flexdinesh/stage`, branch `main`, directory `apps/website`.
- Deploying is merging to `main`. Never run `wrangler deploy` by hand unless
  the owner asks (for example, a rollback the dashboard cannot do).
- Local preview, served the way Cloudflare serves it:
  `mise run website:preview` (`wrangler dev`, :8787).
- Branches other than `main` never build or deploy (preview builds are off).

## Cloudflare build settings

Worker `stage-website`, Settings > Build. These values are the contract;
change them here in the same PR as any change in the dashboard.

| Setting | Value |
| --- | --- |
| Git repository | `flexdinesh/stage` (the Cloudflare Workers and Pages GitHub app needs access to it) |
| Production branch | `main` |
| Preview builds (Branch control) | off |
| Root directory | `apps/website` |
| Build command | `pnpm install --frozen-lockfile --filter '@stage/website...' && pnpm build` (the same as `mise run website:build`) |
| Deploy command | `npx wrangler deploy` (the `wrangler` pinned in `apps/website/package.json`) |
| Build variables | `NODE_VERSION=26.13.0`, `SKIP_DEPENDENCY_INSTALL=1` |
| Build watch paths, include | `apps/website/*`, `package.json`, `pnpm-lock.yaml`, `pnpm-workspace.yaml`, `mise.toml` |
| Build watch paths, exclude | none |
| API token | the existing Workers Builds token |
| Custom domain | `stage.hoplabs.dev` (unchanged) |

- **Node**: the build image does not read `mise.toml`. `NODE_VERSION` must
  match `node` in `mise.toml`; bump both together.
- **pnpm**: Corepack takes `packageManager` (`pnpm@12.15.0`) from the root
  `package.json`.
- **Install**: `SKIP_DEPENDENCY_INSTALL=1` turns off Cloudflare's automatic
  install, so the one lockfile at the root is installed frozen and filtered
  to the website, exactly as locally.
- **Watch paths**: the website's own source, plus the shared inputs that can
  change its build: the root manifest (`packageManager`), the workspace
  definition and its install policy, the lockfile, and the tool versions.
  Product-only changes elsewhere never build the site. A lockfile or
  `mise.toml` change made for the product rebuilds the same site; that is
  harmless.

## Independence from product releases

- Product releases (`.github/workflows/release.yml`, `scripts/release.sh`,
  [release.md](release.md)) never install, build or deploy the website.
- A website build never tags, uploads or publishes a release, and there is
  no GitHub Actions workflow for the site.

## Provenance

A deploy is traced to a commit three ways:

1. GitHub shows a check run named `Workers Builds: stage-website` on the
   `main` commit, with the Build ID and the Worker Version ID.
2. Live pages link "Edit this page on GitHub" to
   `github.com/flexdinesh/stage/edit/main/apps/website/src/pages/...`.
3. The hashed `/assets/*` files a live page references match those of
   `mise run website:build` at that commit.

## Rollback

- **Content**: in the dashboard, Worker `stage-website` > Deployments, deploy
  the previous version (or `npx wrangler rollback <version-id>` from
  `apps/website`). The next build from `main` replaces it, so revert the bad
  change on `main` as well.
- **Build trigger**: reconnect the Worker to the previous repository with its
  previous settings (below), then deploy its last version.

## Previous source

Until 2026-11, the site was built from `flexdinesh/stage-website` (private).
It is kept, not archived, for history and rollback. Its deploy trigger must
stay disconnected so it cannot overwrite a deploy from this repository.

- Last commit: `888459f` ("feat: adopt current Stage logo (#1)"), imported
  here in #86.
- Last production build: `04516fd3-dc01-4d81-9446-480eacb1c624`, Worker
  version `05f093a6-6c55-42ba-bee0-f51bf05213bc` (2026-11-15).
- Its settings, read from that repository (confirm in the dashboard before
  relying on them): root directory `/`, build command `pnpm build`, deploy
  command `npx wrangler deploy`, Node from `.node-version` (`26`), pnpm from
  `packageManager`.

## Ownership

The repository owner holds the Cloudflare account, the Worker, the domain
and the build settings. Changes to those settings, the watch-path inputs or
`apps/website/wrangler.jsonc` update this document in the same PR.
