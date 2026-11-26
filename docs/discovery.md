# Workspace discovery and project identity

Stage owns the project and its stable ID. A harness reports its workspaces and owns its sessions and their checkouts. This is the target contract for [ADR 0008 D9](adr/0008-harness-foundation.md#d9) and [D10](adr/0008-harness-foundation.md#d10).

| Layer | Delivers |
| --- | --- |
| 07 | Evidence, identity keys, a single resolver, bindings, review data, moves, merge and split. Updates API, CLI and MCP consumers and keeps existing screens working. |
| 08 | Project workspace views, discovery review and confirmation, merge and split, task and schedule workspace choice. |
| 09 | Extracts the discovery reader and refresh loop from sync and introduces the adapter's narrow inventory role. Keeps layer 07 behavior and the existing cadence. |

## Workspace evidence (07)

Each inventory row identifies a workspace by connection, external ID and reported path. The path may be empty. Stage stores the latest usable evidence on that row, with its source and time. Rows carried forward from before the migration retain their old evidence until refreshed.

| Evidence | Meaning |
| --- | --- |
| File host | The connection's machine when the adapter can attest that the workspace lives on its filesystem. Unknown otherwise. |
| Canonical path | The cleaned, symlink-resolved absolute directory on the file host, with case preserved. |
| Checkout key | The canonical Git common directory. Linked worktrees share this key. Independent clones have separate common directories. |
| Subpath | The slash-separated path relative to the checkout top. A known root is `.`. An unknown subpath stays unknown. A submodule has its own checkout. |
| Normalized remote | The identity of the checkout's `origin`. With an unknown file host, Stage can use the harness-reported origin and its subpath. |
| Inode hint | When supported, the directory's device and inode on its file host. Used only as move evidence on that same host. |

`machine.Ops` reads filesystem evidence where the files live, in process or on the worker. It reads Git metadata directly, including worktree pointers and the common configuration. Evidence collection is read-only and requires no Git binary. A loopback URL can be a tunnel or a container. A connection's machine and matching path strings alone provide no file host proof. Layer 07 removes shared path mappings. A proven missing directory makes the row unavailable and retains its last evidence. Other inspection errors supply no fresh filesystem proof.

Normalize supported network origin URLs to `host[:port]/repository/path`. SSH and HTTPS spellings can converge. Lowercase the host, strip its trailing dot, drop credentials, query strings, fragments and default ports, and trim surrounding path slashes and a terminal `.git`. Preserve repository path case and non-default ports. Local paths, drive paths, file URLs and ambiguous paths yield no remote key. Mirrors, SSH host aliases, Git URL rewrites and provider-specific spelling can require manual review.

## Identity keys and resolution (07)

A pathless workspace binds by connection and external ID, including when the harness also supplies an origin. Layer 07 must apply this rule before considering the remote key. For workspaces with paths, use the first applicable key. These tuples are schematic. Stored keys preserve component boundaries.

| Key | Scope |
| --- | --- |
| Remote + known subpath | All connections and machines. The same origin and subpath identify one project across clones and worktrees. |
| File host + checkout key + subpath | One file host. Covers a checkout with no usable remote key and its linked worktrees. |
| File host + canonical path | One file host. Covers a plain directory or another locally proven path with no checkout key. |
| Connection + external ID | One connection. Covers pathless workspaces and workspaces without stronger evidence. |

Import, Resolve now and bind preview use one resolver over stored evidence. It evaluates available unbound rows and preserves existing bindings, including those whose workspace is unavailable.

1. **Bound.** Keep the existing project. Other projects with active bindings under the same key are merge suggestions.
2. **Review, declined.** A saved decline prevents automatic placement.
3. **Join or review, moved.** Compare an available workspace with active bindings whose old workspace is unavailable on that same connection. A matching key or a matching inode hint on the same known file host can connect them. Exactly one old binding and one eligible replacement rebind automatically. Multiple plausible old bindings or replacements go to review with candidates.
4. **New, external.** An external-key workspace with no existing binding or move can start its own project, subject to import policy.
5. **Join or review by key.** Join when one project owns the key. Several owners yield **review, ambiguous**. Active bindings on retired connections still count here. For local keys, unrefreshed legacy bindings can match the old same-machine path until refreshed. A manual-bind-only or archived workspace with one ordinary match yields **review, suggested**.
6. **New.** With no key match, the workspace can start a project. Automatic import creates it only when the harness policy allows import and the workspace is unarchived. Other new rows are available for manual placement.

Identity writes serialize across connections on both storage engines. Automatic import plans and applies in one transaction. Resolve now previews placements and replaces the old Match action. Confirmation rechecks the preview against current evidence and bindings. A changed plan requires fresh confirmation. The migration preserves existing projects and can surface duplicate suggestions. Stage keeps its own archive state when a harness archives a workspace.

## Bindings and review (07 backend, 08 UI)

A project can have several active workspaces on a connection and one primary. Tasks and schedules use the primary by default and can choose a specific binding on their connection. An unavailable explicit choice blocks launch with a reason. Task detail shows the workspace used for its stored launch. A move preserves the primary designation, carries explicit task and schedule choices forward, and records a project event.

Discovery review shows **New**, **Suggested**, **Ambiguous**, **Declined** and **Moved** with reasons and candidate projects. A person can bind a workspace to an existing project, create a project, decline it or undo the decline. A decline is scoped to the connection and recorded under both the workspace's identity key and external key. It can survive a changed external ID or a changed path. Unbinding declines that workspace, and removing a primary promotes a remaining binding. A manual bind clears its decline and can replace a binding to resolve a move.

Merge is directional. Its preview names the survivor and counts the workspaces, tasks, schedules and snippets that move. Confirmation checks for stale inputs and can be blocked by a launching task in the source project. The merged ID becomes an alias. An existing project link opens the current surviving project, including after a later merge, and API responses use the survivor's ID. Keep separate dismisses the pair's suggestion. Split moves one binding to a new project and dismisses the pair's merge suggestion. Tasks, schedules and snippets stay with the source. A task that explicitly chose the split workspace needs a new choice. The source must keep at least one active workspace.

The layer 08 views show machine, harness, path or external ID, availability and primary. Cached offline rows stay visible with their machine status. Declines remain visible outside the actionable count. The exact layout and interaction design belong to layer 08.

## Worked examples (07)

- On a laptop, Codex reports a trusted directory at `/home/jo/app`, whose origin is `git@git.example:Org/App.git`. On a build machine, Claude Code reports the checkout root at `/work/app`, whose origin is `https://git.example/Org/App`. Both have the key `(git.example/Org/App, .)`. The first import creates the project. The second joins it. A separate clone or linked worktree of that origin at the root also joins, subject to its import policy.
- A fork at `git.example/Jo/App` has its own key even when it lists `git.example/Org/App` as upstream. In a monorepo, `packages/api`, `packages/web` and `.` have distinct keys under the same origin.
- An OpenCode network service reports `/home/jo/app` with unknown file host and no origin. That path provides no evidence about the laptop directory, so Stage uses the service's connection and external ID. A service that reports a usable origin and subpath can still join by remote evidence. Equal paths on two known hosts also give separate local keys when both lack an origin.

## Refresh and adapter contract (07 behavior, 09 ownership)

A harness implements `Workspaces` with stable external IDs within its connection, display titles, and paths where available. It reports import eligibility and any known archive, deletion or activity information. It sets locality only when the files are local to the connection's machine. It can supply an origin and checkout-relative subpath learned from its harness. `machine.Ops` supplies filesystem evidence. An adapter can expose a descriptor hint for how to add a workspace in layer 08.

Return success only for a complete snapshot. A successful empty snapshot is authoritative and marks prior rows unavailable. An unreachable service, missing inventory source or known partial response returns an error. Stage retains its last good inventory, skips refresh and import, reports the inventory error and continues polling linked tasks. Layer 08 distinguishes an empty review from an unavailable inventory source.

The shipped discovery loop runs once on start, then every five minutes per connection, and accepts refresh kicks. Connections refresh independently, with one full sync in flight per connection. Layer 07 changes the failure path. Layer 09 gives the reader and loop to `discovery`, which consumes `execution.Inventory` and machine operations, with task polling through a narrow coordinator interface. It carries the existing timing forward. Additional retry or backoff policy remains open. How T3 could verify its file host remains open for layer 07. Until verified, T3's file host is unknown.

## Shipped inventory

The table follows the adapters' `Workspaces` methods. Where an adapter supplies locality and an import condition, the current machine inventory reader evaluates that condition.

| Harness | External ID and path | Origin source | Import policy | Failure or partial inventory |
| --- | --- | --- | --- | --- |
| [T3](../server/internal/t3/adapter.go) | T3 project ID and reported workspace root. Locality is unset. | The [repository locator](../server/internal/t3/wire.go) supplies the remote when it names `origin` and a subpath when known. The fork-parent field is ignored. Machine inventory can also use a declared shared path. | Live, unarchived T3 projects are eligible. Current import requires a path. | [Paging](../server/internal/t3/tools.go) discards accumulated rows on call or decode failure, a nonadvancing cursor or a page-limit overrun. A final page without a project array can decode as empty. |
| [OpenCode](../server/internal/opencode/workspace.go) | Project ID and canonical path from the service. Local only in local-auth mode. Worktrees are a separate inventory. | No origin from the project endpoint. Machine inventory can read a local or mapped checkout. | Unarchived VCS projects except `global`. Plain directories are manual-bind only. | Request or decode failure, a null list, or any row missing ID, canonical path or required timestamps fails the inventory. An empty array succeeds. |
| [Claude Code](../server/internal/claudecode/adapter.go) | Clean absolute path from the state file's project map doubles as ID. Local. Claude-managed worktree paths are skipped. | Machine inventory reads the local checkout. | Checkout top only. Other directories are manual-bind only. | Stat, read or parse errors fail the inventory. A missing state file currently returns a successful empty list. A parsed list can be reused when the file and modification time match. |
| [Codex](../server/internal/codex/adapter.go) | Clean absolute path from trusted `config/read` projects doubles as ID. Local. | Machine inventory reads the local checkout. | Any trusted directory inside a Git checkout. Other trusted directories are manual-bind only. | RPC failure, missing config or malformed projects fails the inventory. Missing or null `projects` in an otherwise valid config yields an empty list. A failed directory stat leaves the workspace listed. |

The shipped import and Match paths use different evidence. Match calls Git for checkout proof. Import relies on connection machine and path in ways that can conflate network-service and local paths. Shipped bindings require a path and allow only one active binding per project per connection. A move can produce a second project and strand task launches. Merge and split are absent. Import follows harness archive state, and its per-connection lock allows cross-connection races on Postgres. A full sync returns early on inventory error, although separate observation loops still run. Claude Code inventory also attempts a one-time cleanup of legacy Stage image files in project directories. Layer 07 owns the read-only discovery requirement and the backend fallout from these behaviors.
