# Workspace discovery and project identity

Stage owns the project and its stable ID. A harness reports its workspaces and owns its sessions and their checkouts. The evidence and identity rules implement [ADR 0008 D9](adr/0008-harness-foundation.md#d9) and [D10](adr/0008-harness-foundation.md#d10).

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

Normalize SSH, HTTPS, HTTP and Git network origin URLs to `host[:port]/repository/path`. SSH and HTTPS spellings can converge. Lowercase the host, strip its trailing dot, drop credentials, query strings, fragments and default ports, and trim surrounding path slashes and a terminal `.git`. Reject empty paths and ambiguous escapes. Preserve repository path case and non-default ports. Local paths, drive paths, file URLs and ambiguous paths yield no remote key. Mirrors, SSH host aliases, Git `insteadOf` rewrites and provider-specific spelling can require manual review.

## Identity keys and resolution (07)

Choose the first applicable key in this table. Remote and checkout key components escape percent signs and hash signs to preserve tuple boundaries.

| Priority | Key | When it applies |
| --- | --- | --- |
| 1 | `external:<connection>:<externalId>` | The reported path is empty, even when the harness reported both origin and subpath. |
| 2 | `remote:<remote>#<subpath>` | The path is present and both remote and subpath are known. This key spans connections, machines, clones and worktrees. |
| 3 | `local:<fileHost>:<checkoutKey>#<subpath>` | The file host and Git common directory are known. This key spans linked worktrees on that host. |
| 4 | `dir:<fileHost>:<canonicalPath>` | The file host and canonical directory are known. |
| 5 | `external:<connection>:<externalId>` | The workspace lacks stronger evidence. |

Automatic import and Resolve now use one resolver over stored evidence. The bind picker uses the stored resolution from the same resolver. Resolve now plans available unbound rows, most recently active first, then by external ID and path. Each planned binding is visible to later placements in the same plan. Existing bindings stay attached, including when their workspace is unavailable.

1. **Bound.** Keep the existing project. Other projects with active bindings under the same key are merge suggestions.
2. **Review, declined.** A saved decline prevents automatic placement.
3. **Join or review, moved.** Compare an available workspace with active bindings whose old workspace is unavailable on that same connection. A matching key or a matching inode hint on the same known file host can connect them. Exactly one old binding and one available, unbound, undeclined replacement rebind automatically, including when the replacement is manual-bind only. Retired connections contribute to key matching and are excluded from move matching. Multiple plausible old bindings or replacements go to review with candidates.
4. **New, external.** An external-key workspace with no existing binding or move can start its own project, subject to import policy.
5. **Join or review by key.** Join when one project owns the key. Several owners yield **review, ambiguous**. Active bindings on retired connections still count here. For local keys, unrefreshed legacy bindings can match the old same-machine path until refreshed. A manual-bind-only or archived workspace with one ordinary match yields **review, suggested**.
6. **New.** With no key match, the workspace can start a project. Automatic import creates it only when the harness policy allows import and the workspace is unarchived. Other new rows are available for manual placement.

Identity writes serialize across connections on both storage engines. Automatic import plans and applies in one transaction. Resolve now previews placements and replaces the old Match action. Confirmation rechecks the preview against current evidence and bindings. A changed plan requires fresh confirmation. The migration preserves existing projects and can surface duplicate suggestions. Stage keeps its own archive state when a harness archives a workspace.

## Bindings and review (07 backend, 08 UI)

A project can have several active workspaces on a connection and one primary. Tasks and schedules use the primary by default and can choose a specific binding on their connection. An unavailable explicit choice blocks launch with a reason. Task detail shows the workspace used for its stored launch. A move preserves the primary designation, carries explicit task and schedule choices forward, and records a project event.

Discovery review shows **New**, **Suggested**, **Ambiguous**, **Declined** and **Moved** with reasons and candidate projects. A person can bind a workspace to an existing project, create a project, decline it or undo the decline. A decline is scoped to the connection and recorded under both the workspace's identity key and external key. It can survive a changed external ID or a changed path. Unbinding declines that workspace, and removing a primary promotes a remaining binding. A manual bind clears its decline and can replace a binding to resolve a move.

Merge is directional. Its preview names the survivor and counts the workspaces, tasks, schedules and snippets that move. Confirmation checks for stale inputs and can be blocked by a launching task in the source project. The merged ID becomes an alias. An existing project link opens the current surviving project, including after a later merge, and API responses use the survivor's ID. Keep separate dismisses the pair's suggestion. Split moves one binding to a new project and dismisses the pair's merge suggestion. Tasks, schedules and snippets stay with the source. Tasks and schedules that explicitly chose the split workspace need a new choice. The source must keep at least one active workspace.

Projects shows the primary workspace and extra workspace count. Project detail groups all workspaces by machine, with a copyable path or external ID, harness facts, state and primary marker. The Projects **Discovery review** filter shows new, suggested, ambiguous, declined and moved rows with candidate project links. Cached rows remain visible when a machine is offline. Live inventory, binding and resolution need its connection back. Decline and undo decline update cached decisions.

Use **Resolve now** to preview which workspaces will be created, joined or moved on one connection. Confirmation uses the preview token. Changed evidence or placements require a new confirmation. The bind drawer puts suggestions first, filters by repository and links any Stage owner. Its discovery instructions come from the harness descriptor when provided.

Merge names the survivor and previews the tasks, schedules, snippets and workspaces that move. **Keep separate** dismisses only the named pair. Split moves the chosen workspace to a new project while the other workspaces and records stay. Stage refuses to split the last workspace. Links to a merged project open its surviving ID.

## Resolver examples (07)

The verdict is for the second workspace unless specified. Rows are eligible for import unless the example says otherwise.

| # | Workspace evidence | Verdict |
| --- | --- | --- |
| 1 | Two harnesses on one file host attest the same path. | Join by remote and subpath when known, otherwise by local evidence. |
| 2 | A checkout root and a linked worktree root. | Join by remote or, without one, by common directory plus the root subpath. |
| 3 | Two independent clones on one host. | Join by remote when both have the same remote and subpath. Each clone without a remote has its own local checkout key. |
| 4 | Codex on a laptop reports `git@git.example:Org/App.git` at the root. Claude Code on a worker reports `https://git.example/Org/App` at the root. | Join by `remote:git.example/Org/App#.` |
| 5 | Separate checkouts on two machines, without remotes. | New, using distinct host-scoped keys. |
| 6 | A fork at `git.example/Jo/App` lists `git.example/Org/App` as upstream. | New. The fork's origin sets its key. |
| 7 | One monorepo reports `packages/api`, `packages/web` and `.`. | New for each subpath. |
| 8 | An old workspace becomes unavailable and the checkout reappears on the same connection. | Join as moved when the identity key or same-host inode hint identifies exactly one source and one replacement. Competing sources or replacements go to moved review. |
| 9 | A bound workspace changes origin URL. | The binding survives. A clone with either origin can join while an active binding still carries its key. |
| 10 | A harness reports an empty path, even with origin and subpath. | New by external key, then bound. If an earlier path-bearing row used that same external key and became unavailable, a unique pathless replacement can match a move. |
| 11 | A network service without an attested file host reports the same path as a local checkout. | New by external key if remote evidence is missing. A reported remote and known subpath can join when its reported path is present. |
| 12 | A workspace is unbound, then rediscovered under a different external ID but the same identity key. | Declined review on that connection. |
| 13 | A replacement instance reports a workspace whose key is still held on the retired connection. | Join by key. A locally attested workspace can join by the old same-machine path if the owner is an unrefreshed legacy row and the candidate uses a local key. |

## Layer 08 API handoff

See [the route reference](api.md#workspace-discovery-and-project-identity)
for actions, validation and errors. These examples use an existing project
`42` and connection `7`.

### Resolve preview and confirmation

`POST /api/connections/7/resolve` with `{"apply":false}` previews an eligible
second workspace joining project `42`.

```json
{
  "applied": false,
  "previewToken": "6c7341a963ef7c5e4f8eb376ac9bbfac8b62b3363e378f941fcb2272556c9bbb",
  "placements": [
    {
      "workspace": {
        "connectionId": 7,
        "externalId": "/work/app-review",
        "title": "App review checkout",
        "workspace": "/work/app-review",
        "available": true,
        "importable": true,
        "harnessArchived": false,
        "activeAt": null,
        "projectId": null,
        "bindingId": null,
        "primary": false,
        "fileHost": 2,
        "canonicalPath": "/work/app-review",
        "remote": "git.example/Org/App",
        "subpath": ".",
        "identityKey": "remote:git.example/Org/App#.",
        "evidenceAt": "2026-11-27T02:00:00Z",
        "evidenceSource": "machine",
        "declined": false,
        "resolution": {"outcome": "join", "reason": "", "rule": "remote", "candidates": []}
      },
      "outcome": "join",
      "reason": "",
      "rule": "remote",
      "projectId": 42,
      "newProjectTitle": "",
      "candidates": [],
      "acts": true
    }
  ],
  "created": 0,
  "joined": 1,
  "moved": 0,
  "review": 0
}
```

Confirm with `{"apply":true,"previewToken":"<token from preview>"}`.
If another operation already bound that workspace, confirmation can return
this 409 with a fresh empty plan. The same error envelope wraps a fresh
merge preview after a stale merge confirmation.

```json
{
  "error": "The preview changed. Review it again.",
  "code": "preview_changed",
  "preview": {
    "applied": false,
    "previewToken": "413685b32733b2658e2a57753ed03e3dc9a6146d93d9aa52d489397510dd8052",
    "placements": [],
    "created": 0,
    "joined": 0,
    "moved": 0,
    "review": 0
  }
}
```

### Review

`GET /api/workspaces/review` reads cached inventory. A row may still be
present when its machine is offline. Use its connection and machine
status to explain connectivity. The row adds a top-level display reason
to the workspace object. This example shows a Claude Code subdirectory
that can be manually bound to an existing project. Declines and new rows are excluded from `attention`.

```json
{
  "workspaces": [
    {
      "connectionId": 8,
      "externalId": "/work/app-extra/packages/web",
      "title": "Web",
      "workspace": "/work/app-extra/packages/web",
      "available": true,
      "importable": false,
      "harnessArchived": false,
      "activeAt": null,
      "projectId": null,
      "bindingId": null,
      "primary": false,
      "fileHost": 2,
      "canonicalPath": "/work/app-extra/packages/web",
      "remote": "git.example/Org/App",
      "subpath": "packages/web",
      "identityKey": "remote:git.example/Org/App#packages/web",
      "evidenceAt": "2026-11-27T02:00:00Z",
      "evidenceSource": "machine",
      "declined": false,
      "resolution": {"outcome": "review", "reason": "suggested", "rule": "remote", "candidates": [43]},
      "reason": "suggested"
    }
  ],
  "merges": [],
  "counts": {"new": 0, "suggested": 1, "ambiguous": 0, "declined": 0, "moved": 0, "merges": 0},
  "attention": 1
}
```

### Multiple workspaces

After apply, `GET /api/projects/42` includes these two bindings on the same
connection. The example shows the `bindings` field of the project response.

```json
{
  "bindings": [
    {
      "id": 55, "projectId": 42, "connectionId": 7, "machineId": 2,
      "harness": "codex", "externalProjectId": "/work/app", "title": "App",
      "workspace": "/work/app", "revision": 1, "status": "available",
      "observedWorkspace": "/work/app", "connectionActive": true, "connectionLabel": "Codex",
      "primary": true, "rule": "new", "remote": "git.example/Org/App",
      "subpath": ".", "fileHost": 2, "evidenceAt": "2026-11-27T02:00:00Z", "facts": []
    },
    {
      "id": 56, "projectId": 42, "connectionId": 7, "machineId": 2,
      "harness": "codex", "externalProjectId": "/work/app-review", "title": "App review checkout",
      "workspace": "/work/app-review", "revision": 2, "status": "available",
      "observedWorkspace": null, "connectionActive": true, "connectionLabel": "Codex",
      "primary": false, "rule": "remote", "remote": "git.example/Org/App",
      "subpath": ".", "fileHost": 2, "evidenceAt": "2026-11-27T02:00:00Z", "facts": []
    }
  ]
}
```

Send `bindingId: 56` with the task or schedule and the same connection ID.
Use `GET /api/connections/7/capabilities?projectId=42&bindingId=56` and
`/api/projects/42/actions?connectionId=7&bindingId=56` for scoped catalogs
and actions. The task reports `workspace: {"bindingId":56,"path":"/work/app-review","source":"chosen"}`
before launch and `source: "pinned"` once the destination is stored. The
pinned assignment includes `externalId`, which identifies a pathless
destination even after its binding is removed. In the task and schedule
drawers, use Primary to follow the connection's default, or select a
specific workspace to pin it.

## Refresh and adapter contract (07 behavior, 09 ownership)

A harness implements `Workspaces` with stable external IDs within its connection, display titles, and paths where available. It reports import eligibility and any known archive, deletion or activity information. It sets locality only when the files are local to the connection's machine. It can supply an origin and checkout-relative subpath learned from its harness. `machine.Ops` supplies filesystem evidence. The optional descriptor field `discoveryHint` tells a user how to make a workspace appear.

Return success only for a complete snapshot. A successful empty snapshot is authoritative and marks prior rows unavailable. An unreachable service, missing inventory source or known partial response returns an error. Stage retains its last good inventory, skips refresh and import, reports the inventory error and continues polling linked tasks. Layer 08 distinguishes an empty review from an unavailable inventory source.

The shipped discovery loop runs once on start, then every five minutes per connection, and accepts refresh kicks. Connections refresh independently, with one full sync in flight per connection. Layer 07 changes the failure path. Layer 09 gives the reader and loop to `discovery`, which consumes `execution.Inventory` and machine operations, with task polling through a narrow coordinator interface. It carries the existing timing forward. Additional retry or backoff policy remains open. T3 leaves its file host unknown.

## Shipped inventory

The table follows the adapters' `Workspaces` methods. Where an adapter supplies locality and an import condition, the current machine inventory reader evaluates that condition.

| Harness | External ID and path | Origin source | Import policy | Failure or partial inventory |
| --- | --- | --- | --- | --- |
| [T3](../server/internal/t3/adapter.go) | T3 project ID and reported workspace root. Locality is unset. | The [repository locator](../server/internal/t3/wire.go) supplies the remote when it names `origin` and a subpath when known. The fork-parent field is ignored. | Live, unarchived T3 projects are eligible. | [Paging](../server/internal/t3/tools.go) discards accumulated rows on call or decode failure, a nonadvancing cursor or a page-limit overrun. A final page without a project array can decode as empty. |
| [OpenCode](../server/internal/opencode/workspace.go) | Project ID and canonical path from the service. Local only in local-auth mode. Worktrees are a separate inventory. | No origin from the project endpoint. Machine inventory can read a checkout in local-auth mode. | Unarchived VCS projects except `global`. Plain directories are manual-bind only. | Request or decode failure, a null list, or any row missing ID, canonical path or required timestamps fails the inventory. An empty array succeeds. |
| [Claude Code](../server/internal/claudecode/adapter.go) | Clean absolute path from the state file's project map doubles as ID. Local. Claude-managed worktree paths are skipped. | Machine inventory reads the local checkout. | Checkout top only. Other directories are manual-bind only. | Stat, read or parse errors fail the inventory. A missing state file fails inventory. Run Claude Code once to create it. A parsed list can be reused when the file and modification time match. |
| [Codex](../server/internal/codex/adapter.go) | Clean absolute path from trusted `config/read` projects doubles as ID. Local. | Machine inventory reads the local checkout. | Any trusted directory inside a Git checkout. Other trusted directories are manual-bind only. | RPC failure, missing config or malformed projects fails the inventory. Missing or null `projects` in an otherwise valid config yields an empty list. A failed directory stat leaves the workspace listed. |
