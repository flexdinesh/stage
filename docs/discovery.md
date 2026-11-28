# Workspace discovery and project identity

[ADR 0010](adr/0010-harness-workspaces-and-logical-projects.md) defines
Stage-owned logical groups and harness-owned destinations.

## Logical project groups

Every usable catalog row can join a project group, including ordinary
folders and workspaces ineligible for legacy import. Stage assigns an
unplaced row to a single group when the normalized origin and known
subpath match. Without that evidence, a known subpath on the same attested
Git common directory or a canonical non-Git directory on the same
attested host can match. An unknown Git subpath, a bare path or a label
cannot establish membership. Multiple owners or competing move candidates
give the row its own group. Evidence refresh can add members and suggest
related projects. Existing memberships remain fixed until a person edits
them, so fresh evidence cannot undo a separation.

Members can move an exact row into an existing project or separate it
into a new one. Moving the row back restores its previous membership.
The edit leaves all project IDs, history, snippets and execution
selectors in place. Retired and forgotten connections retain grouped
rows as history. Groups with only retired rows and groups emptied by
edits stay addressable. Group archive and harness eligibility remain
independent.

Projects shows one row per group and summarizes its contributing connections.
Open a group to see the exact workspaces, including last-known paths and
connection status. Move a workspace to another project or separate it into
a new project from that list. Members review the source and destination
before confirming. If another member edits the grouping first, Stage refreshes
the preview and asks for confirmation again. The History view keeps empty,
retired and archived projects available by their original links.

Use the [group API](api.md#logical-project-groups) to read current
membership and preview and confirm edits. A catalog project can appear in
the group list without an equivalent legacy project listing. A project
with recorded work can have no current workspace members.

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

`machine.Ops` reads filesystem evidence where the files live, in process or on the worker. It reads Git metadata directly, including worktree pointers and the common configuration. Evidence collection is read-only and requires no Git binary. A loopback URL can be a tunnel or a container. A connection's machine and matching path strings alone provide no file host proof. A proven missing directory makes the row unavailable and retains its last evidence. Other inspection errors supply no fresh filesystem proof.

Normalize SSH, HTTPS, HTTP and Git network origin URLs to `host[:port]/repository/path`. SSH and HTTPS spellings can converge. Lowercase the host, strip its trailing dot, drop credentials, query strings, fragments and default ports, and trim surrounding path slashes and a terminal `.git`. Reject empty paths and ambiguous escapes. Preserve repository path case and non-default ports. Local paths, drive paths, file URLs and ambiguous paths yield no remote key. Mirrors, SSH host aliases, Git `insteadOf` rewrites and provider-specific spelling can require manual review.

## Choose a launch destination

Select a connection and an exact workspace on it. The human catalog at
`GET /api/connections/:connectionId/workspaces` and the scoped destination
summary at `GET /api/projects?view=destinations` expose `workspaceId`.
The summary is also available through `stage projects ls --json` and MCP
`list_projects`. Use the same pair when fetching workspace capabilities
and creating a task or schedule. The selector distinguishes different
roots under the same native workspace ID. A pathless row keeps its
connection-scoped native identity.

A new root or replacement connection can appear in the same group while
an existing pin still names the old row. Use the repair reason to choose
the exact workspace for future launches. The recorded launch, session,
project and attribution stay with the original work. See
[workspace selection](api.md#task-and-schedule-workspace-selection) and
[retired routes](api.md#retired-workspace-routes).

### Eligibility

A listed row is an eligible destination unless its harness archived it or
the harness will not start a session there. The second case is
`blockedReason`, in the harness's words, for example a Claude Code folder
that is not trusted. A blocked row stays in the catalog, keeps its group
membership and appears in the destination summary with `available: false`
and its `reason`. Creating or editing a task or schedule with it, and the
launch claim after the pre-launch refresh, return `destination_unavailable`
with that reason. The next refresh after the person fixes it in the harness
makes the row eligible again. A blocked row still takes project actions,
and a harness may declare one that fixes it: Claude Code's "Trust in Claude
Code" trusts the folder, and the inventory refresh after the action makes
the row eligible. Project detail opens a blocked row's harness actions
under its reason. See the
[Claude Code inventory](execution.md#claude-code-adapter).

## Refresh and adapter contract (07 behavior, 09 ownership)

A harness implements `Workspaces` with stable external IDs within its connection, display titles, and paths where available. It reports import eligibility, any known archive, deletion or activity information, and a `BlockedReason` (or a `TrustRule` that machine evidence settles) for a row it will not launch in. It sets locality only when the files are local to the connection's machine. It can supply an origin and checkout-relative subpath learned from its harness. `machine.Ops` supplies filesystem evidence. The optional descriptor field `discoveryHint` tells a user how to make a workspace appear.

Return success only for a complete snapshot. A successful empty snapshot is authoritative and marks prior rows unavailable. An unreachable service, missing inventory source or known partial response returns an error. Stage retains its last good inventory, reports the inventory error and continues polling linked tasks. The human inventory endpoint distinguishes these states and exposes the last successful read and the latest failure. Selection summaries include eligible catalog rows without paths or discovery diagnostics.

The discovery loop runs once on start, then every five minutes per connection, and accepts refresh kicks. Connections refresh independently, with one full sync in flight per connection. Layer 07 changes the failure path. Layer 09 gives the reader and loop to `discovery`, which consumes `execution.Inventory` and machine operations, with task polling through a narrow coordinator interface. It carries the existing timing forward. Additional retry or backoff policy remains open. T3 leaves its file host unknown.

## Shipped inventory

The table follows the adapters' `Workspaces` methods. The import column describes legacy import eligibility. Exact selection follows current catalog availability and the owning adapter's policy.

| Harness | External ID and path | Origin source | Import policy | Failure or partial inventory |
| --- | --- | --- | --- | --- |
| [T3](../server/internal/t3/adapter.go) | T3 project ID and reported workspace root. Locality is unset. | The [repository locator](../server/internal/t3/wire.go) supplies the remote when it names `origin` and a subpath when known. The fork-parent field is ignored. | Live, unarchived T3 projects are eligible. | [Paging](../server/internal/t3/tools.go) discards accumulated rows on call or decode failure, a nonadvancing cursor or a page-limit overrun. A final page without a project array can decode as empty. |
| [OpenCode](../server/internal/opencode/workspace.go) | Project ID and canonical path from the service. Local only in local-auth mode. Worktrees are a separate inventory. Verification reads only the project and worktree lists, never a location. | No origin from the project endpoint. Machine inventory can read a checkout in local-auth mode. | Unarchived VCS projects except `global`. Plain directories can be listed outside legacy import. | Request or decode failure, a null list, or any row missing ID, canonical path or required timestamps fails the inventory. An empty array succeeds. |
| [Claude Code](../server/internal/claudecode/adapter.go) | Clean absolute paths from the state project map and the current native session list. The path doubles as ID. Local. Claude-managed worktree paths are skipped. | Machine inventory reads the local checkout. | Checkout top only. Other folders are discoverable without automatic import. Folders Claude does not trust stay listed but blocked (Claude's trust rule, settled by machine evidence). | Missing or malformed state, an unavailable or malformed native roster, or a changed config identity fails the refresh. A valid fresh state may omit the project map. |
| [Codex](../server/internal/codex/adapter.go) | Clean absolute path from trusted `config/read` projects doubles as ID. Local. | Machine inventory reads the local checkout. | Any trusted directory inside a Git checkout. Other trusted directories can be listed outside legacy import. | RPC failure, missing config or malformed projects fails the inventory. Missing or null `projects` in an otherwise valid config yields an empty list. A failed directory stat leaves the workspace listed. |

Claude's default state is `$HOME/.claude.json`. An explicit
`CLAUDE_CONFIG_DIR` places `.claude.json` inside the config directory,
including when the override is `$HOME/.claude`. Stage checks the reported
config identity and reads the exact working directories reported by
`claude agents --json --all`. Encoded transcript directory names cannot
recover an exact path, so they supply no inventory evidence.
