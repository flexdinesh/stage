# stage REST API

Base path `/api`. JSON in and out (`Content-Type: application/json`). Field names camelCase. TypeScript mirror: `app/src/api/types.ts` (change both together).

## Conventions

- Timestamps: ISO 8601 strings (UTC, e.g. `"2026-11-08T10:00:00Z"`). Nullable fields are `null`, never omitted.
- IDs: app ids (`id`, `projectId`, `taskId`, `treeId`, `needs`) are JSON numbers. T3 ids (`t3ProjectId`, `t3ThreadId`) are strings.
- Lists are wrapped: `{ "projects": [...] }`, `{ "tasks": [...] }`, ...
- PATCH bodies are partial: omitted fields are unchanged; `null` clears a nullable field.
- Repeatable list query params accept repetition or commas: `?state=running&state=needs_input` == `?state=running,needs_input`.
- `from` / `to` query params: ISO date (`2026-11-01`) or datetime. `from` inclusive, `to` exclusive. Filter on `createdAt` unless stated.
- Case-insensitive substring filters (`q`, title search) fold ASCII letters only.
- Errors: non-2xx responses have body `{ "error": string }`. Some 409s add a machine-readable `"code"` (see "Pending questions").
- Every response carries `Stage-Api-Version: <n>` (currently `1`; changes only on breaking API changes). The Go client (CLI, stdio MCP) refuses a response with a different value; a missing header is accepted.
- Request origin policy (`/api` and `/mcp`; [runtime.md](runtime.md#request-origin-policy)): requests with a Host that is not allowed, cross-site requests and untrusted cross-origin writes get 403; a non-empty write body that is not `application/json` (or `multipart/form-data` for uploads) gets 415.

| Status | Meaning |
|---|---|
| 400 | invalid body or query (bad enum, missing required field, bad JSON) |
| 403 | rejected by the request origin policy (Host, cross-site, cross-origin) |
| 404 | resource not found |
| 409 | conflict: action not allowed in current state, duplicate unique value |
| 415 | write body not `application/json` / `multipart/form-data` |
| 502 | T3 call failed (T3 error message in `error`) |
| 503 | T3 not connected (no valid token) or DB unavailable |
| 500 | unexpected |

## Enums

| Name | Values |
|---|---|
| TaskState | `backlog` `launching` `running` `needs_input` `done` `settled` `failed` `interrupted` `cancelled` (`done`: the run finished, not yet settled in T3; T3 calls this `completed`) |
| ThreadStatus (T3) | `idle` `preparing` `queued` `starting` `running` `waiting` `completed` `interrupted` `failed` `cancelled` `rolled_back` |
| RuntimeMode | `approval-required` `auto-accept-edits` `auto` `full-access` (task default `full-access`) |
| InteractionMode | `default` `plan` (default `default`) |
| SendMode | `auto` `queue` `steer` `restart` (default `auto`) |
| StartMode | `auto` (a tree member launches once its needs settle) `manual` (it reads `ready` and notifies; the user starts it). Tree default `auto`; a task's own `startMode` null inherits the tree's |
| Readiness | `blocked` (a need is not settled) `ready` (every need settled, no start time, and manual or no needs); backlog tree members only, else null |
| NotificationEvent | `needs_input` `failed` `ready` `done` `settled` |
| ProjectSource | `app` (created via stage) `t3` (imported from T3) |
| ThreadSource | `app` (thread belongs to a task) `t3` (any other thread) |

## Shared shapes

```jsonc
// ModelOption: T3 ProviderOptionSelection
{ "id": "effort", "value": "low" }          // value: string | boolean

// Workspace
{ "type": "root" }
{ "type": "worktree", "baseRef": "origin/main", "branch": "feat-x", "base": "ref", "baseTaskId": null }   // branch: string | null (null = T3 picks)
```

`Workspace` maps to T3 `workspaceStrategy`: root → `{type:"root"}`; worktree → `{type:"worktree", baseRef, branch?, startFromOrigin: baseRef has a "<remote>/" prefix}` (remote = `origin` or `upstream`; `feature/x` is treated as local).

`base` (worktree only, always emitted; `ref` when omitted on input): `ref` starts from `baseRef`; `predecessor` starts the worktree from a need's branch, with `baseRef` as the fallback (see "Predecessor base"). `baseTaskId` (worktree only, always emitted, null unless `base` is `predecessor`): the need whose branch it starts from; null = the sole need. Invalid values, `predecessor` with `root`, `predecessor` on a schedule and `baseTaskId` without `predecessor` are 400. A needs change or splice clears a `baseTaskId` that is no longer a need.

## Health

### `GET /api/health`
200 / 503 `Health`:
```json
{
  "status": "ok",
  "db": "ok",
  "service": "stage",
  "identity": "stage",
  "version": "v1.4.0",
  "commit": "4202029f...",
  "apiVersion": 1,
  "instanceId": "9f2c4e1a7b3d5c60",
  "startedAt": "2026-11-16T09:00:00Z",
  "publicUrl": "https://stage.example.ts.net",
  "t3": { "connected": false, "lastError": "dial tcp 127.0.0.1:3773: connection refused" }
}
```
- A live Stage server answers 200 whenever its database works; T3 being offline is still 200 with `t3.connected: false`.
- 503: the DB ping failed (`status` and `db` are `"error"`; the other fields are still present).
- `service` is always `"stage"`, so a client can tell a Stage server from anything else on an address. `identity` is the installation identity, `version` the build (`"dev"` when unstamped), `commit` may be `""`. `instanceId` and `startedAt` identify the running process (`startedAt` is `null` only outside a real server). `publicUrl` is `STAGE_PUBLIC_URL` without a trailing slash, `null` when unset; the desktop uses it for copied links. `t3.lastError` is nullable.
- `apiVersion` equals the `Stage-Api-Version` header. See [runtime.md](runtime.md#health-and-version) for readiness and compatibility rules.

## T3 connection, sync, catalog

### `GET /api/t3/status`
200 `T3Status`:
```json
{
  "connected": true,
  "t3Url": "http://127.0.0.1:3773",
  "publicUrl": "http://t3.example.com:3773",
  "environmentId": "env_123",
  "clientId": "mcp_client_abc",
  "tokenExpiresAt": "2026-12-08T10:00:00Z",
  "adminTokenConfigured": true,
  "lastError": null,
  "lastFullSyncAt": "2026-11-08T10:00:00Z"
}
```
`environmentId`, `clientId`, `tokenExpiresAt`, `lastError`, `lastFullSyncAt` are nullable.

### `POST /api/t3/pair`
Paste fallback: exchange a pairing code from `t3 pair` for a token.
Body: `{ "code": "ABCD-1234" }`. 200 `T3Status`. 400 empty code. 502 pairing failed.

### `POST /api/sync`
Run a full sync now (all T3 projects + threads into the mirror; creates app projects for new T3 projects; project title, root and archive state follow T3). Also reads top-level session details to populate normalized state and run metadata independently of legacy task state. Synchronous.
200 `SyncResult`:
```json
{ "projects": 12, "threads": 776, "finishedAt": "2026-11-08T10:00:00Z" }
```
409 sync already running. 503 not connected. 502 T3 error.

### `GET /api/catalog`
Cached `orchestrator_capabilities` (refreshed every 10 min). Only providers with no T3 `constraints` are listed.
200 `Catalog`:
```json
{
  "providers": [
    {
      "providerInstanceId": "claude",
      "driverKind": "claude",
      "displayName": "Claude",
      "models": [
        {
          "id": "claude-haiku-5-5",
          "label": "Haiku 5.5",
          "options": [{ "id": "effort", "values": ["low", "medium", "high"], "default": "medium" }]
        }
      ],
      "runtimeModes": ["approval-required", "auto-accept-edits", "auto", "full-access"]
    }
  ],
  "fetchedAt": "2026-11-08T10:00:00Z"
}
```
`displayName` falls back to `providerInstanceId`; `label` falls back to `id`; `options` is `[]` when none; option `default` is nullable; `runtimeModes` is `null` when T3 does not report it. 503 not connected and no cache.

## Projects

`Project`:
```json
{
  "id": 1,
  "t3ProjectId": "proj_abc",
  "title": "stage",
  "workspaceRoot": "/home/user/workspace/stage",
  "source": "app",
  "archivedAt": null,
  "createdAt": "2026-11-08T10:00:00Z",
  "updatedAt": "2026-11-08T10:00:00Z",
  "lastActivityAt": "2026-11-08T11:30:00Z"
}
```

`lastActivityAt`: latest of the project's thread `updatedAt` / `settledAt` (incl. subagents) and task `updatedAt`; null when none.

T3 is the source of truth: `title`, `workspaceRoot` and `archivedAt` follow T3 on every sync (empty T3 title → `workspaceRoot`). Projects are read-only here; rename/archive in T3. A project deleted in T3 stays (tasks reference it) and is archived (`archivedAt` = T3 `deletedAt` unless already archived).

### `GET /api/projects`
Query: `archived` = `true` (only archived) | `false` (default, only active) | `all`.
200 `{ "projects": Project[] }` ordered by `title`.

### `POST /api/projects`
Body: `{ "title": string, "workspaceRoot": string }` (both required, trimmed; absolute roots are path-cleaned, e.g. trailing `/` dropped).
- An app project with the same `workspaceRoot` exists → 409.
- A T3 project with the same `workspaceRoot` exists (already imported, or found after refreshing `t3_project_list`) → return it as mirrored (`source: "t3"`): `title` and archive state stay T3's; the requested `title` is ignored. Projects deleted in T3 do not count: their root can be created again.
- Otherwise create it in T3 (`t3_project_create`) → `source: "app"`.

201 `Project`. 502 T3 error. 503 not connected.

### `GET /api/projects/:id`
200 `Project`. 404.


## Tasks

`Task`:
```json
{
  "id": 7,
  "projectId": 1,
  "projectTitle": "stage",
  "title": "Say OK",
  "prompt": "reply OK",
  "providerInstanceId": "claude",
  "model": "claude-haiku-5-5",
  "modelOptions": [{ "id": "effort", "value": "low" }],
  "runtimeMode": "full-access",
  "interactionMode": "default",
  "workspace": { "type": "root" },
  "tags": ["smoke"],
  "notes": "",
  "state": "running",
  "t3ThreadId": "thread_abc",
  "treeId": 3,
  "treeTitle": "Ship auth",
  "startMode": null,
  "effectiveStartMode": "auto",
  "readiness": null,
  "needs": [5],
  "dependents": [9],
  "adopted": false,
  "readyAt": null,
  "startAt": null,
  "startTz": null,
  "scheduleId": null,
  "scheduleTitle": null,
  "originThreadId": null,
  "originThreadTitle": null,
  "handoff": true,
  "attachments": [{ "id": 4, "name": "error.png", "mimeType": "image/png", "sizeBytes": 48211 }],
  "launchAttempt": 1,
  "lastError": null,
  "t3WebUrl": "http://t3.example.com:3773/env_123/thread_abc",
  "t3AppUrl": "https://app.t3.codes/threads/env_123/thread_abc",
  "t3DeepLink": "t3code://threads/env_123/thread_abc",
  "launchedAt": "2026-11-08T10:00:00Z",
  "finishedAt": null,
  "settledAt": null,
  "runStartedAt": null,
  "runEndedAt": null,
  "lastWordPreview": null,
  "lastWordAt": null,
  "pullRequests": [],
  "pendingQuestionCount": 0,
  "createdAt": "2026-11-08T09:59:00Z",
  "updatedAt": "2026-11-08T10:00:00Z",
  "stateSince": "2026-11-08T10:00:00Z"
}
```
- `stateSince`: when the task entered its current `state` (its latest task event into that state); null without one.
- `providerInstanceId` / `model` null = T3 default; they must be set together (400 otherwise). `modelOptions` default `[]`.
- `treeId` / `treeTitle`: the task's tree (see "Trees"); null outside one. `needs`: tasks in the same tree this one needs (by id); `dependents`: tasks that need this one. Both `[]` outside a tree.
- `startMode`: the task's own start mode (null = inherit the tree's); `effectiveStartMode`: `startMode`, else the tree's; null outside a tree. `readiness`: see "Tree rules". `readyAt`: when a manual task's needs all settled (the `ready` notification); cleared when it stops being ready or launches.
- `adopted`: the task tracks an existing T3 session (see "Adopt session"); `prompt` is `""`.
- `startAt` (UTC instant) / `startTz` (IANA zone it was entered in): one-shot start time of a `backlog` task; the scheduler launches it at that time (see "Schedule rules"). Both null or both set; non-null only while the task is in `backlog` (any launch clears them). Never set together with `needs`.
- `scheduleId` / `scheduleTitle`: the recurring schedule that spawned this task (null for ordinary tasks, or once the schedule is deleted).
- `originThreadId` / `originThreadTitle`: the T3 session that created the task (agent CLI/MCP provenance; spawned schedule tasks inherit the schedule's). Title is the mirrored thread title, null when not mirrored. Not editable.
- `handoff`: append one handoff block per finished need to the launch prompt (see "Handoff"). Default `true`; editable in `backlog` only.
- `attachments`: the task's images (`Attachment`, upload order), sent with the prompt on every launch. Default `[]`. See "Images".
- `t3WebUrl` = `T3_PUBLIC_URL/<environmentId>/<t3ThreadId>` (null without thread or environment id). `t3AppUrl` = T3 thread `link` as served to external MCP clients (`https://app.t3.codes/threads/<env>/<thread>`; null without thread). `t3DeepLink` = `t3code://threads/<environmentId>/<t3ThreadId>` (T3 desktop/mobile app; null without thread or environment id). Thread carries the same three.
- `finishedAt`: first entry into `done|failed|interrupted|cancelled` (per launch attempt). `settledAt`: entry into `settled`.
- `runStartedAt` / `runEndedAt`: the task thread's elapsed run span (see Thread); null without a thread or before a detail read.
- `lastWordPreview` / `lastWordAt`: the task thread's last word preview and reply time (see Thread); null without a thread or before one is read.
- `pullRequests`: the task thread's `PullRequest[]` (see Thread); `[]` without a thread.
- `pendingQuestionCount`: the task thread's answerable pending questions (see Thread); 0 without a thread.

`TaskEvent`:
```json
{ "id": 3, "taskId": 7, "fromState": "launching", "toState": "running", "detail": "thread status running", "at": "2026-11-08T10:00:01Z" }
```
`fromState` null for the creation event.

`TaskDetail` = `Task` + `{ "events": TaskEvent[] (oldest first), "thread": Thread | null, "tree": TreeDetail | null, "batch": CreatedTasks | null, "currentHandoffs": Handoff[], "currentLaunchBase": LaunchBase | null }`. `tree`: the task's tree (see "Trees"); null outside one. `currentHandoffs`: the current launch attempt's handoff records, one per need, by need id (see "Handoff"); `[]` before the first launch, without needs, or with `handoff` off. `currentLaunchBase`: the worktree base the current launch attempt used (see "Predecessor base"); null before the first launch or without `workspace.base` `predecessor`. `batch`: the tasks the task's origin session created (see "Session batch"); null when `originThreadId` is null. `thread.lastWord` carries the reply excerpt; `thread.recentRuns` the runs.

### `GET /api/tasks`
Query (all optional): `project` (project id), `state` (repeatable TaskState), `tag` (has tag), `q` (case-insensitive substring of title or prompt), `from`, `to` (createdAt), `settledFrom` (`settledAt >= settledFrom`).
200 `{ "tasks": Task[] }` ordered by `updatedAt` desc.

### `POST /api/tasks`
Body `CreateTaskRequest`:
```jsonc
{
  "projectId": 1,                // required
  "title": "Say OK",             // required
  "prompt": "reply OK",          // required
  "providerInstanceId": "claude",// optional, null
  "model": "claude-haiku-5-5",   // optional, null
  "modelOptions": [{ "id": "effort", "value": "low" }],  // optional, default []
  "runtimeMode": "full-access",  // optional
  "interactionMode": "default",  // optional
  "workspace": { "type": "root" },// optional
  "tags": [],                    // optional
  "notes": "",                   // optional
  "treeId": null,                // optional: join this tree (tree rules)
  "needs": [],                   // optional: tasks this one needs (tree rules)
  "needsSessionId": null,        // optional: also need this T3 session (adopted first; "afterMe")
  "startMode": null,             // optional: auto | manual; omitted = inherit the tree's
  "originThreadId": null,        // optional: T3 session creating the task (provenance)
  "attachmentIds": [4],          // optional: staged uploads to attach (max 8)
  "startLocal": null,            // optional: one-shot start, wall time "YYYY-MM-DDTHH:mm" in startTz
  "startTz": null,               // required with startLocal: IANA zone, e.g. "America/New_York"
  "handoff": true,               // optional, default true: append each need's handoff at launch
  "start": false                 // optional: true = Save & start (not with needs or needsSessionId)
}
```
201 `Task` (state `backlog`, or `launching` when started). 400 (incl. an `attachmentIds` entry that is not a staged upload, more than 8; `startLocal` without `startTz` or vice versa; unknown zone; unparseable `startLocal`; a start time not in the future; `startLocal` together with needs or `start: true`; `start: true` with needs; bad `startMode`). 404 project / tree / need / `needsSessionId` (session not mirrored). 409 tree rule broken (e.g. needs in different trees) or `needsSessionId` not adoptable. Start errors after save: task is still created; it lands in `failed` with `lastError`.

### `GET /api/tasks/:id`
200 `TaskDetail`. 404.

### `PATCH /api/tasks/:id`
Body `UpdateTaskRequest`: any of `projectId title prompt providerInstanceId model modelOptions runtimeMode interactionMode workspace tags notes` `attachmentIds` `startLocal` `startTz` `handoff` `startMode`.
In `backlog` all fields are editable; otherwise only `title`, `tags`, `notes` (409 for others). `attachmentIds` is the exact image set: the task's own ids to keep plus staged uploads to add; the task's images not listed are deleted (400 for ids that are neither, more than 8).
`startLocal` + `startTz` (backlog only, 409 otherwise) set the one-shot start (same 400 rules as create); both `null` clears it; a task with needs cannot have one (400). `handoff`: `true|false` (backlog only, 409 otherwise; 400 for `null`). `startMode`: `auto|manual`, `null` inherits the tree's (backlog only). Needs change with `PUT /api/tasks/:id/needs`; tree membership with the tree endpoints.
200 `Task`. 404.

### `PUT /api/tasks/:id/needs`
Body `{ "needs": number[] }` (required; `[]` clears them): replaces the task's needs per "Tree rules". A task outside a tree joins its needs' tree (or a new one). 200 `Task`. 400 missing `needs`, or needs on a task with a start time. 404 task / need. 409 tree rule broken.

### `DELETE /api/tasks/:id`
204. 409 when state is `launching|running|needs_input` (stop first). In a tree it is spliced out first: its dependents take over its needs (A→X→B becomes A→B).

### `POST /api/tasks/:id/start`
Launch in T3 (`t3_thread_launch`, `clientRequestId = "task-<id>-<uuid>"`, new attempt, new `clientRequestId` and new thread each start). T3 dedupes launches by `clientRequestId` across clients; the random part keeps other databases (dev, tests, restored backups) on the same T3 from colliding. A launch answered with a thread in another project than the task's is a collision: the task moves to `failed` without adopting the thread. Allowed from `backlog|failed|interrupted|cancelled`; else 409 (always 409 for an adopted task). Start early: a `blocked` task starts before its needs settle and keeps them (the `launching` event reads `launch requested before #3, #5 settled`); it gets a handoff only from needs that have finished. Each start clears `t3ThreadId`, `lastError`, `finishedAt`, `settledAt`; the previous thread stays in history (as `source: "t3"`).
A transport failure (timeout, network, dropped session; not a T3 tool error) is retried once with the same `clientRequestId`. If that also fails the task moves to `failed` and the next start reuses the attempt (same `launchAttempt` and `clientRequestId`), so T3 returns the thread if the lost launch did create one. The same applies to a task left in `launching` by a server restart (failed at startup with `launch interrupted by server restart`). On shutdown the server waits up to 100s for in-flight launches (compose `stop_grace_period: 2m`).
Images: before each launch every task image is uploaded to T3 afresh (`t3_attachment_prepare_upload`, then the bytes POSTed to the signed `relativeUrl` on `T3_URL`) and passed as `attachments` to `t3_thread_launch`. A failed upload or launch fails the start (`lastError`) and discards that start's uploads (best effort).
200 `Task` (state `launching` or later). 502 / 503 (task moves to `failed` with `lastError` on T3 error).

### `POST /api/tasks/:id/followup`
Body `{ "message": string, "mode"?: SendMode, "attachmentIds"?: number[] }`. Without images: `t3_thread_send` (message required). With `attachmentIds` (staged uploads, max 8): the images are uploaded to T3 and sent with the optional message as one new message (`t3_thread_send_attachments`); `mode` is not allowed (400). Sent images are deleted; on a T3 error they stay staged (no automatic retry). 409 when task has no thread.
200 `Task`. 400. 502 / 503.

### `POST /api/tasks/:id/stop`
`t3_thread_interrupt` (stop). State updates via poller. 409 when task has no thread.

Follow-up and stop bump `updatedAt`; that re-arms polling of a `failed|interrupted` task for 10 minutes (see polling scope). They then poll the thread once: the thread mirror is refreshed, but task state does not change for `backlog` tasks or `settled` tasks that have dependents (others may reopen).
200 `Task`. 502 / 503.

### `POST /api/tasks/:id/duplicate`
New `backlog` task with the same project, title, prompt, images (copied), model config, workspace, tags, notes; no tree, no needs, no start time, no schedule.
201 `Task`. 404. 409 for an adopted task.

## Trees

A tree is a DAG of tasks: each member's `needs` are members that must settle before it starts. It is the only way to express dependencies. A task is in at most one tree; trees may span projects. Rules in "Tree rules".

`Tree`:
```json
{ "id": 3, "title": "Ship auth", "startMode": "auto", "createdAt": "2026-11-08T09:59:00Z", "updatedAt": "2026-11-08T10:00:00Z" }
```
`TreeDetail` = `Tree` + `TreeTimes` + `{ "tasks": Task[] (by id), "edges": [{ "taskId": 9, "needId": 5 }], "levels": [[5, 7], [9]], "events": TaskEvent[] }`. `levels`: member ids per level, roots first; a task's level is the longest need path to a root; each level by id. `events`: the members' 50 newest task events, newest first (`{ id, taskId, fromState, toState, detail, at }`).

`TreeTimes` = `{ "startedAt": string | null, "lastActivityAt": string }`. `startedAt`: the earliest member `launchedAt` (null before any launch). `lastActivityAt`: the latest of the tree's `createdAt`, a member's `updatedAt` and its thread's `updatedAt` (so a member's agent working counts as activity).

`TreeSummary` = `Tree` + `TreeTimes` + `{ "counts": TreeCounts, "levels": number, "projects": string[] (member project titles, sorted), "settledAt": string | null }`. `counts`: `{ total, backlog, blocked, ready, launching, running, needsInput, done, settled, failed, interrupted, cancelled }` (`blocked` and `ready` split `backlog`). `settledAt`: the latest member settle once every member is settled, else null.

### `GET /api/trees`
Query (all optional): `all=1` (every tree), `settledFrom` (also completed trees whose latest settle is at or after it), `project` (a member is in that project). Default: active trees (a member not settled). 200 `{ "trees": TreeSummary[] }`, `lastActivityAt` desc.

### `POST /api/trees`
Body `{ "title": string, "startMode"?: StartMode, "tasks": [{ "taskId": number, "needs"?: number[] }] }`: creates the tree, moves each listed task into it (a member of another tree is spliced out there), then sets each listed `needs` (omitted keeps the task's needs). One transaction. 201 `TreeDetail`. 400 blank title, no tasks, a task listed twice, bad `startMode`. 404 task / need. 409 tree rule broken.

### `GET /api/trees/:id`
200 `TreeDetail`. 404.

### `PATCH /api/trees/:id`
Body: any of `title`, `startMode` (`auto|manual`). 200 `TreeDetail`. 400. 404. Switching to `manual` makes released members ready (each notifies once); switching to `auto` launches them on the next reconcile.

### `DELETE /api/trees/:id`
Dissolves the tree: its tasks stay, their needs and tree membership go. 204. 404.

### `POST /api/trees/:id/tasks`
Body `{ "taskId": number, "needs"?: number[] }`: adds the task (a member of another tree moves: it is spliced out there and loses its needs), then sets `needs` when given. 200 `TreeDetail`. 404 tree / task / need. 409 tree rule broken.

### `DELETE /api/trees/:id/tasks/:taskId`
Splices the task out: its dependents take over its needs; the task stays, standalone. An emptied tree is deleted. 204. 404 (also when the task is not in that tree).

### `POST /api/trees/:id/start`
"Start ready": starts every `ready` member (same as `POST /api/tasks/:id/start` each). 200 `{ "started": Task[], "errors": [{ "taskId": number, "error": string }] }`. 404.

## Schedules

A schedule is a recurring task template: on each cron fire it spawns a new task (`scheduleId` set) with its config and images and starts it. One-shot starts are not schedules; they are `startAt` on a backlog task.

`Schedule`:
```json
{
  "id": 3,
  "projectId": 1,
  "projectTitle": "stage",
  "title": "Nightly deps bump",
  "prompt": "bump deps, run tests",
  "providerInstanceId": "claude",
  "model": "claude-sonnet-5-5",
  "modelOptions": [],
  "runtimeMode": "full-access",
  "interactionMode": "default",
  "workspace": { "type": "worktree", "baseRef": "origin/main", "branch": null, "base": "ref" },
  "tags": ["deps"],
  "notes": "",
  "attachments": [],
  "cron": "0 2 * * *",
  "tz": "America/New_York",
  "paused": false,
  "nextRunAt": "2026-11-11T20:30:00Z",
  "lastRunAt": "2026-11-10T20:30:00Z",
  "lastTaskId": 41,
  "lastTaskState": "settled",
  "originThreadId": null,
  "createdAt": "2026-11-08T09:59:00Z",
  "updatedAt": "2026-11-08T10:00:00Z"
}
```
- `cron`: standard 5-field expression (minute hour day-of-month month day-of-week; `@hourly|@daily|@weekly|@monthly|@yearly` also accepted). `@every` and `CRON_TZ=`/`TZ=` prefixes are rejected (400). Evaluated in `tz` (IANA), so DST is handled by the zone.
- `nextRunAt`: next fire (UTC); null while `paused`. `lastRunAt`: fire time of the newest run row (any outcome); null before the first.
- `lastTaskId` / `lastTaskState`: newest task this schedule spawned (null before the first).
- `originThreadId`: the T3 session that created the schedule (agent provenance; set on create only). Spawned tasks inherit it as their `originThreadId`.

`ScheduleRun`:
```json
{ "id": 9, "scheduleId": 3, "fireAt": "2026-11-10T20:30:00Z", "outcome": "started", "taskId": 41, "detail": "", "at": "2026-11-10T20:30:04Z", "task": null }
```
- `outcome`: `started` (task spawned; launch result lives on the task), `skipped` (previous run still active), `missed` (server was down across the fire time; one row per outage), `manual` (Run now).
- `task`: the spawned `Task` on detail reads, else null.

`ScheduleDetail` = `Schedule` + `{ "runs": ScheduleRun[] }` (newest first, at most 50, each with `task`).

### `GET /api/schedules`
Query (optional): `project` (project id), `dueWithin` (Go duration, e.g. `24h`: only unpaused schedules with `nextRunAt <= now + dueWithin`).
200 `{ "schedules": Schedule[] }` ordered by `nextRunAt` asc (paused last), then id.

### `POST /api/schedules`
Body `CreateScheduleRequest`: the `CreateTaskRequest` config fields (`projectId title prompt providerInstanceId model modelOptions runtimeMode interactionMode workspace tags notes attachmentIds originThreadId`, same rules; `treeId`, `needs`, `needsSessionId`, `startMode`, `start`, `startLocal`, `startTz` are 400; spawned tasks are standalone) plus `"cron": "0 2 * * *"` (required), `"tz": "America/New_York"` (required), `"paused": false` (optional).
201 `Schedule`. 400 (invalid cron or zone, plus task field rules). 404 project.

### `GET /api/schedules/:id`
200 `ScheduleDetail`. 404.

### `PATCH /api/schedules/:id`
Body: any of the create fields (`attachmentIds` is the exact image set, as for tasks). Changing `cron`, `tz` or `paused` recomputes `nextRunAt` from now (resuming never fires a missed time). Spawned tasks are not changed.
200 `Schedule`. 400. 404.

### `DELETE /api/schedules/:id`
204. Spawned tasks are kept (their `scheduleId` becomes null). 404.

### `POST /api/schedules/:id/run`
Run now: spawn a task and start it immediately, ignoring `paused` and the overlap rule; records a `manual` run; `nextRunAt` unchanged. Launch errors land on the task (`failed` + `lastError`).
201 `Task`. 404.

### `POST /api/schedules/preview`
Body `{ "cron": "0 2 * * 1-5", "tz": "America/New_York" }`. 200 `{ "next": [ISO, ISO, ISO] }` (next three fires, UTC). 400 with the parse error message.

### Schedule rules
- The scheduler ticks every 15s (and on start).
- One-shot: a `backlog` task with `startAt <= now` is launched (`startAt` cleared); the `launching` event detail reads `scheduled start` or `scheduled start, late by 12m` when more than a minute late (e.g. server was down: late starts are not skipped). Manual Start now also clears `startAt`.
- Recurring, per unpaused schedule with `nextRunAt <= now`, in one transaction (holds the database write lock):
  - `now - nextRunAt > 2m` (server was down): record one `missed` run (`detail`: `missed N fires`), advance `nextRunAt` to the first fire after now. Nothing starts.
  - A previous spawned task still `backlog` (pending launch), `launching`, `running` or `needs_input`: record `skipped` (`detail`: `#41 still running`), advance. `done` but unsettled does not block.
  - Else spawn a task (config + images copied, `scheduleId`, `startAt = fireAt`, `startTz = tz`), record `started`, advance. The one-shot pass then launches it (a crash in between still launches it on restart).
- Fires are computed in the schedule's `tz`; a fire is never run twice (`nextRunAt` advances in the same transaction).
- Scheduled starts and skips do not notify; a failed launch notifies as usual (`failed`).

## Images

`Attachment`: `{ "id": 4, "name": "error.png", "mimeType": "image/png", "sizeBytes": 48211 }`. Types: `image/png|jpeg|gif|webp` (sniffed from the bytes), max 10 MB each, max 8 per task or follow-up. Stored in the database. An upload is *staged* until a task create/update attaches it or a follow-up sends it; staged uploads older than 24h are deleted hourly.

### `POST /api/uploads`
Multipart form, field `file` (one image). 201 `Attachment` (staged). 400 not an accepted image type, empty, over 10 MB.

### `GET /api/attachments/:id`
200 raw image bytes (`Content-Type` = `mimeType`, immutable cache, `nosniff`). 404.

### `DELETE /api/attachments/:id`
Deletes a staged upload, or an image of a `backlog` task. 204. 404. 409 when the task has left backlog (images are read-only after launch).

## Threads (history)

`Thread` (T3 mirror row):
```json
{
  "t3ThreadId": "thread_abc",
  "t3ProjectId": "proj_abc",
  "projectId": 1,
  "projectTitle": "stage",
  "title": "Say OK",
  "status": "completed",
  "state": "done",
  "archived": false,
  "activeRunId": null,
  "latestRunId": "run_2",
  "settled": false,
  "settledAt": null,
  "runStartedAt": "2026-11-08T10:00:00Z",
  "runEndedAt": "2026-11-08T10:01:00Z",
  "runCount": 2,
  "pullRequests": [
    { "host": "github.com", "repository": "flexdinesh/stage", "number": 41, "url": "https://github.com/flexdinesh/stage/pull/41", "source": "created", "state": "merged" }
  ],
  "pendingRequestCount": 0,
  "pendingQuestionCount": 0,
  "providerInstanceId": "claude",
  "model": "claude-haiku-5-5",
  "runtimeMode": "full-access",
  "interactionMode": "default",
  "branch": null,
  "worktreePath": null,
  "parentThreadId": null,
  "relationship": null,
  "creationSource": "mcp",
  "source": "app",
  "taskId": 7,
  "t3WebUrl": "http://t3.example.com:3773/env_123/thread_abc",
  "t3AppUrl": "https://app.t3.codes/threads/env_123/thread_abc",
  "t3DeepLink": "t3code://threads/env_123/thread_abc",
  "createdAt": "2026-11-08T10:00:00Z",
  "updatedAt": "2026-11-08T10:01:00Z",
  "syncedAt": "2026-11-08T10:01:05Z",
  "lastWordPreview": "Done. Fixed the race in PollTask and added a regression test.",
  "lastWordAt": "2026-11-08T10:00:58Z",
  "lastWord": null,
  "recentRuns": null
}
```
`lastWord` (detail reads only: `GET /api/threads/:id` `thread` and `GET /api/tasks/:id` `thread`; always null in lists): the thread's latest assistant reply (last word) as last synced, `{ "text": "## Done\nFixed **the race**.", "truncated": false, "runId": "run_2", "at": "2026-11-08T10:00:58Z" }`; null until one is read. `text` is the reply's markdown capped at 4000 chars (T3 UTF-16 units); `truncated` = the reply is longer (full text in T3); `runId` = the reply's run; `at` = the reply item's updatedAt. `lastWordPreview` (every Thread and Task): the same reply as one plain line (markdown markers dropped, whitespace collapsed, up to 240 chars); `lastWordAt` = `lastWord.at`. Sync reads the last word only when a thread is at rest (no active run, or pending input) and its `updatedAt` changed since the last check, from any detail read (task polls, watched threads, root threads in full sync); a running thread keeps its previous reply. A thread with no assistant reply keeps the stored one. `projectId` / `projectTitle` null when no app project mirrors `t3ProjectId`. `runStartedAt` / `runEndedAt`: elapsed run span from the last detail read (`t3_thread_read` recentRuns, up to 50): the oldest run's `startedAt` (`requestedAt` if it never started; thread `createdAt` when `runCount` exceeds the runs read) to the newest run's `completedAt`; null without runs, `runEndedAt` null while the newest run is incomplete. Full sync reads every root thread, so subagents have no span. `pendingRequestCount`: T3 pending requests (questions and approvals). `pendingQuestionCount`: how many of them are user-input questions stage can answer (`t3_pending_request` list, counted whenever sync reads a thread with pending requests; a failed count keeps the previous value; reset to 0 when nothing is pending; never above `pendingRequestCount`). `relationship`: `fork|subagent|null`. `creationSource`: T3 `web|mobile|mcp|provider|server`.

`pullRequests` (`PullRequest[]`, never null): PRs T3 associates with the thread, mirrored on every list and detail read (`t3_thread_list` / `t3_thread_read` `linkedPullRequest` first, then `pullRequests`, deduped by `url`, entries without a `url` dropped). `repository` is `owner/name`; `state` is T3's string (`open`, `merged`, `closed`, `draft`, ...); `source` is T3's (`created`, `agent`) or `linked` for an unlabelled `linkedPullRequest`. Fresh within the 5 min full sync (sooner for polled/watched threads).
`runCount`: runs T3 reported on the last detail read (each follow-up is a new run); 0 before one.

`recentRuns` (`ThreadRun[]`; detail reads only: `GET /api/threads/:id` `thread` and `GET /api/tasks/:id` `thread`; always null in lists): newest first, at most 50 (T3 `runLimit`), from the last detail read; `[]` before one. `runCount` can exceed its length; older runs live only in T3.
`ThreadRun`:
```json
{ "runId": "run:thread:t1:ordinal:2", "ordinal": 2, "status": "completed", "providerInstanceId": "claudeAgent", "model": "claude-opus-5-5", "executedModel": "claude-opus-5-5", "requestedAt": "2026-11-08T10:00:00Z", "startedAt": "2026-11-08T10:00:01Z", "completedAt": "2026-11-08T10:01:00Z" }
```
`status`: ThreadStatus. `model`: requested; `executedModel`: what the provider ran (null when T3 did not report it). `startedAt` null while queued, `completedAt` null while running. Queue time = `startedAt - requestedAt`; run time = `completedAt - startedAt`.

### `GET /api/threads`
Query (all optional): `project` (app project id), `status` (repeatable ThreadStatus), `state` (repeatable normalized TaskState), `active` (`1|true`: unarchived, unsettled running/needs-input sessions across origins, no implicit date cutoff), `sort` (`activity`: updatedAt desc; omitted: createdAt desc), `parent` (direct children of this T3 thread ID), `settled` (`true|false`), `provider` (providerInstanceId), `source` (`app|t3`), `q` (case-insensitive title substring), `from`, `to` (createdAt), `subagents` (`include|exclude`, default `include`; `exclude` = root threads only, `parentThreadId` null), `needsInput` (`1|true`: only external threads counted by the badge, see "Notification rules"; other filters still apply), `running` (`1|true`: only external running threads, see "Notification rules"; other filters still apply), `settledFrom` (ISO date or datetime: only external settled threads with `settledAt >= settledFrom`, ordered by `settledAt` desc, see "Notification rules"; other filters still apply), `limit` (default 100, max 500), `offset` (default 0).
200 `{ "threads": Thread[], "total": number }`; `total` = matches before limit/offset. Thread also includes `state`, `archived`, `activeRunId` and `latestRunId`. Session state follows live thread metadata independently of a legacy task's frozen state. Pending input, active runs and failed/interrupted/cancelled runs outrank a stale settled flag.

### `GET /api/threads/search`
Query: `q` (required, trimmed, 2 to 200 characters), `project` (app project id, optional), `limit` (1..50, default 20; applies to each source).

Finds sessions by title, then by what was said in them. Two sources, run concurrently:
1. stage: root threads whose title contains `q` (case-insensitive), newest activity first, up to `limit`.
2. T3 `t3_thread_search` (only when connected, cancelled after 2s): active, unarchived, non-subagent threads by title, branch, PR and project, then message text; not exhaustive; up to `limit`.

Results: stage matches first, then T3 matches whose thread is not already listed, in T3's order (dedupe by thread id; a stage title match wins over a T3 message match).

200 `{ "results": SearchResult[], "t3": "ok"|"offline"|"timeout"|"error" }`. T3 failures never fail the request: `t3` other than `ok` means stage title matches only.

`SearchResult`:
```jsonc
{
  "t3ThreadId": "thread-a",
  "title": "Fix auth token refresh",
  "projectTitle": "stage",          // null when unknown
  "matchSource": "branch",          // title|branch|project|pr|user|assistant
  "snippet": null,                  // message text around the match (user|assistant only, <= 240 chars)
  "messageCreatedAt": null,         // ISO, user|assistant only
  "t3DeepLink": "t3code://threads/<env>/thread-a",
  "thread": {}                      // Thread, or null when stage has not synced it yet (session detail still opens it)
}
```

`matchSource`: stage matches are `title`. T3 reports `title` for title, branch, PR and project matches alike; stage labels them from its mirrored row: title contains `q` (or every word of it) → `title`, else branch → `branch`, else project title → `project`, else `pr` (PRs are not mirrored). An unsynced thread keeps `title`. T3 message matches are `user` or `assistant`.

400 invalid `q`/`limit`/`project`; 404 unknown project.

### `GET /api/threads/:id`

200 `{ "thread": Thread, "children": Thread[], "task": Task|null, "createdTasks": CreatedTasks }`. `createdTasks`: the tasks this session created (see "Session batch"; empty arrays when none). Encode the complete T3 thread ID as one URL path segment. Refreshes the requested session when connected; reads cached metadata offline. Children are direct children, newest activity first. Never sends a message. 404 missing session; 502/503 T3 read errors.

### `POST /api/threads/:id/adopt`
"Track as task": makes a top-level session a task so it can be a tree node. Body (all optional) `{ "title": string, "notes": string, "tags": string[], "treeId": number }`. Rules in "Adopt session". 201 `Task` (created), 200 `Task` (a task already owns the session; with `treeId` it moves into that tree). 404 session not mirrored (refreshed from T3 first when connected) / tree. 409 subagent, archived, or its project is not in stage.

## Transcript

Read-only conversation of a session: user messages, assistant messages and plans (T3 `t3_thread_read` view `messages`). No tool calls, commands, reasoning or diffs; those stay in T3. Encode the T3 thread ID and item ID as single path segments. All endpoints: 404 when the session is not mirrored, 503 when T3 is not connected, 502 on T3 read errors.

`TranscriptItem`:
```json
{
  "position": 12,
  "itemId": "item_abc",
  "sourceThreadId": "thread_abc",
  "inherited": false,
  "kind": "assistant",
  "status": "completed",
  "streaming": false,
  "createdBy": "agent",
  "runId": "run_1",
  "text": "Done. Summary...",
  "truncated": false,
  "updatedAt": "2026-11-08T10:01:00Z"
}
```
`position`: T3 global timeline position, unique per item, ascending in time (tool items in between are skipped, so positions have gaps). `kind`: `user|assistant|plan`. `inherited`: the item belongs to a parent thread (fork history). `status`: T3 turn item status (`idle|pending|running|waiting|completed|failed|cancelled|interrupted`). `streaming`: the item belongs to the active run and is still pending/running/waiting, so its text may grow. `createdBy`: `user|agent|system|null`. `text`: at most 2000 characters; `truncated` true when the full text is longer (fetch it from the item endpoint). Items are upserts keyed by `position`: a newer copy (same or later `updatedAt`) replaces the older.

Cursor: the highest position at or below which every message is final (not streaming). Resuming from a cursor resends everything after it, including streaming items.

### `GET /api/threads/:id/transcript`
Query: `limit` (default 50, max 200), `before` (position; only items below it). 200 `{ "items": TranscriptItem[], "hasEarlier": boolean, "cursor": number|null, "active": boolean, "status": ThreadStatus }`. `items` ascending by position: the newest `limit` messages (below `before` when given). `hasEarlier`: older messages exist (page with `before` = first item's position). `cursor`: resume point for the stream, null when `before` is set; -1 for an empty session. `active`: a run is in flight (active run or status preparing/queued/starting/running/waiting). T3 has no backward paging, so each page scans the session forward.

### `GET /api/threads/:id/transcript/items/:itemId`
200 `{ "itemId": string, "text": string, "truncated": boolean }`: the item's full text, assembled from T3 text pages; `truncated` only when it exceeds 1,000,000 characters.

### `GET /api/threads/:id/transcript/opening`
200 `{ "item": TranscriptItem|null }`: the session's opening prompt, its first own user message (inherited fork history skipped). `null` while the session has none yet. A truncated `text` is the 2000-character preview; fetch the full text from the item endpoint. The scan stops at the T3 page holding the match.

### `GET /api/threads/:id/transcript/stream`
Server-Sent Events (`text/event-stream`). Query `after` (cursor; default -1). A `Last-Event-ID` header (browser reconnect) wins over `after`. On connect the server sends `retry: 3000`, every item after the cursor, then a `state` event, then live changes. It never sends items at or below the cursor, and event ids never go below it.

| Event | `data` | Meaning |
|---|---|---|
| `item` | TranscriptItem | new or changed item (upsert). `id:` is the current cursor. |
| `state` | `{ "active": boolean, "status": ThreadStatus, "error": string\|null }` | sent on connect and whenever activity, status or the T3 read error changes. |
| `reset` | `{}` | positions no longer line up, or the client fell behind. The server closes the stream; refetch the REST page and reconnect with its cursor. |

Comment lines (`: ping`) every 15s keep proxies from timing out. Headers: `Cache-Control: no-cache, no-transform`, `X-Accel-Buffering: no`; every event is flushed. Caddy's `reverse_proxy` flushes event streams by default; keep this path out of `encode` if responses arrive in bursts.

Polling: one server poller per session, shared by every open stream. While a run is active it reads T3 every 2s from the cursor (re-reading streaming items). After the run ends it reads once more, then stops calling T3 and watches the mirrored session status (refreshed by sync every 5s) to resume. It stops 10s after the last stream closes. Child (subagent) sessions are not refreshed by the watcher, so a new run in one goes live only after reconnecting.

## Pending questions

A session waiting on its user has pending requests: user-input questions (T3 `AskUserQuestion`-style) and permission approvals. stage reads and answers questions through T3 `t3_pending_request` (needs `orchestration:operate`); approvals are only counted and must be resolved in T3.

`Question` (T3 user-input question):
```jsonc
{
  "id": "approach",
  "header": "Approach",
  "question": "Which rollout approach should I take?",
  "options": [{ "label": "Incremental", "description": "Run both loops, then switch", "value": "incremental" }], // value optional
  "multiSelect": false,        // optional
  "allowCustomAnswer": null,   // optional; free text allowed unless false
  "initialAnswer": null,       // optional; prefilled custom answer
  "required": null             // optional; stage, like T3's client, requires every question
}
```

### `GET /api/threads/:id/questions`
Reads the thread and its pending questions fresh from T3 and refreshes the mirror (`pendingRequestCount`, `pendingQuestionCount`). 200:
```json
{ "pendingRequestCount": 2, "approvalCount": 1, "requests": [{ "requestId": "req-1", "questions": [/* Question */] }] }
```
`approvalCount` = pending requests T3 does not list as questions. Requests answered between list and read are skipped. 404 unknown thread; 502 T3 error; 503 T3 not connected.

### `POST /api/threads/:id/questions/:requestId/answer`
Body `{ "answers": { "<questionId>": string | string[] } }`. Rules (T3's client rules): every question answered, no unknown ids; a string is an option (its `value`, or its trimmed `label` when it has no value) or custom text (not blank unless `initialAnswer` is set; only when `allowCustomAnswer` is not false); an array is for `multiSelect` only, non-empty, known options, no duplicates. stage re-reads the request, validates, sends the answers with labels normalized, records them, then refreshes the thread and polls its task (a task usually leaves `needs_input` at once).
200 `{ "requestId": string, "respondedAt": string, "thread": Thread }`.

Idempotent: resending the answers stage already sent for a request returns the first `respondedAt` without calling T3 (also when two identical submits race). Errors:

| Status | `code` | When |
|---|---|---|
| 400 | | invalid answers (message names the question) |
| 404 | | unknown thread |
| 409 | `already_answered` | answered in T3, or by stage with different answers |
| 409 | `request_stale` | the request is gone or no longer pending (e.g. expired) |
| 502 | | other T3 failure |
| 503 | | T3 not connected |

## References

### `GET /api/refs/resolve?ref=`
What a pasted reference names. `ref` accepts: a stage task or session link (`…/tasks/<id>`, `…/sessions/<id>`; any host, query and fragment ignored, a sub-path prefix allowed), the markdown link the web UI copies (`[stage task #12: Title](…/tasks/12)`), a desktop deep link (`stage://tasks/<id>`, `stage://sessions/<id>`, also under `stage://localhost/`; any `stage-<suffix>://` developer scheme), a T3 web thread link (`T3_PUBLIC_URL/<environmentId>/<threadId>`, matched only on that host), a `t3code://threads/<environmentId>/<threadId>` app link, `#12` or `12` (task), or any other single token (T3 thread id). Stage paths win over T3 links on the same host. 200:
```json
{ "kind": "task", "taskId": 12, "threadId": "th-1" }
```
A task's `threadId` is its T3 thread (null before launch); a session's `taskId` is the task that owns it (null for sessions started outside stage). A session stage has not mirrored yet is read from T3 once when connected. 400 not a reference; 404 unknown task or session.

## Snippets

Reusable prompt text, inserted into prompts, follow-ups and notes by typing `/` in the web app. `projectId` null = global (shown in every project); set = only in that project's tasks.

`Snippet`:
```json
{
  "id": 1,
  "name": "plan-first",
  "body": "Before editing, write a plan and wait for approval.",
  "projectId": null,
  "createdAt": "2026-11-08T10:00:00Z",
  "updatedAt": "2026-11-08T10:00:00Z"
}
```

- `GET /api/snippets` → 200 `{ "snippets": Snippet[] }`: global first, then by project title, then `name`.
- `POST /api/snippets` body `{ "name": string, "body": string, "projectId"?: number|null }` → 201 `Snippet`. 400 blank name/body or unknown `projectId`. 409 duplicate name (names are unique across all scopes).
- `GET /api/snippets/:id` → 200 `Snippet`. 404.
- `PATCH /api/snippets/:id` partial of the POST body → 200 `Snippet`. 400, 404, 409 as above.
- `DELETE /api/snippets/:id` → 204. 404.

## Preferences

New-task model defaults and the global notification filter. A new task prefills from `pinned`, else `lastUsed`, else the built-in default (T3 default model, `full-access`, `default`).

`ModelDefaults`:
```json
{ "providerInstanceId": "claude", "model": "claude-haiku-5-5", "modelOptions": [{ "id": "effort", "value": "low" }], "runtimeMode": "full-access", "interactionMode": "default" }
```
`providerInstanceId` / `model` null = T3 default (set together).

### `GET /api/preferences`
200 `{ "pinned": ModelDefaults | null, "lastUsed": ModelDefaults | null, "notifyEvents": NotificationEvent[] }`. `lastUsed` is written by every `POST /api/tasks` (its model fields). `notifyEvents` (default all: `["needs_input","failed","ready","done","settled"]`, in that order) is the global filter of "Notification rules".

### `PUT /api/preferences/pinned`
Body: `ModelDefaults` (pin; `runtimeMode` / `interactionMode` default to `full-access` / `default` when empty) or `null` (unpin). Empty body 400.
200 `Preferences`. 400 invalid enum or provider/model not set together.

### `PUT /api/preferences/notifications`
Body: `{ "events": NotificationEvent[] }` (`needs_input|failed|ready|done|settled`; duplicates dropped; `[]` = all off). Applies to every device.
200 `Preferences`. 400 unknown event or missing `events`.

## Stats

### `GET /api/stats`
Query: `from`, `to` (default last 30 days: `to` = now, `from` = `to` - 30d), `tz` (IANA zone for day buckets, default `UTC`), `subagents` (`include|exclude`, default `include`; `exclude` drops subagent threads from all thread counts). Thread counts use mirrored threads by `createdAt` in range; task counts are current totals (not range-filtered).
200 `Stats`:
```json
{
  "from": "2026-10-09T10:00:00Z",
  "to": "2026-11-08T10:00:00Z",
  "tz": "UTC",
  "threadsByDay": [{ "day": "2026-11-08", "count": 14 }],
  "threadsByProject": [{ "projectId": 1, "t3ProjectId": "proj_abc", "title": "stage", "count": 9 }],
  "threadsByStatus": [{ "status": "completed", "count": 20 }],
  "tasksByState": [{ "state": "running", "count": 2 }],
  "needsInput": 1
}
```
`threadsByDay` ascending, days with zero omitted. `threadsByProject` count desc; `projectId` null and `title` = `t3ProjectId` when no app project. `needsInput` = tasks in `needs_input`. 400 bad `tz` or `subagents`.

## Notifications (Web Push)

The server sends Web Push (VAPID, RFC 8291 aes128gcm) to every subscribed browser. The VAPID key pair is generated on first start and stored in the database; the VAPID subject is `STAGE_PUBLIC_URL` when it is https, else `https://github.com/flexdinesh/stage`.

### `GET /api/push/config`
200 `{ "publicKey": string }` (VAPID public key, base64url, uncompressed P-256 point; the browser's `applicationServerKey`).

### `POST /api/push/subscriptions`
Body: browser `PushSubscription.toJSON()` subset `{ "endpoint": string, "keys": { "p256dh": string, "auth": string } }`. Upsert by `endpoint` (keys and user agent refreshed). 204. 400 missing field or non-https endpoint.

### `DELETE /api/push/subscriptions`
Body `{ "endpoint": string }`. 204 (also when unknown). 400 missing endpoint.

### `POST /api/push/test`
Body `{ "endpoint": string }`. Sends a test notification to that subscription synchronously. 204. 404 unknown endpoint. 502 push service rejected it (`error` = status and body); a 404/410 from the push service also deletes the subscription.

### `GET /api/badge`
200 `{ "needsInput": number }`: tasks in `needs_input` + external threads needing input (see below).

### Push payload
JSON, shown by the service worker. Built from the stored inbox row by one server function, shared with `GET /api/notifications/stream`:
```json
{ "title": "Needs input", "body": "Say OK · stage", "url": "/sessions/thread-abc#answer", "tag": "task-7", "badge": 2 }
```
Needs-input payload URLs end in `#answer`: the page scrolls to the Needs input panel and focuses its first control.
`tag` replaces an older notification for the same task (`task-<id>`) or thread (`thread-<t3ThreadId>`); the service worker sets `renotify` so a replacement alerts again. iOS ignores tag replacement (WebKit bug 258922), so the service worker also closes shown notifications with the same tag (`tag`, or the copy in `data.tag`) before showing the new one. `badge` = `GET /api/badge` at send time. TTL 24h; urgency `high` for needs input, `normal` otherwise. Header `Topic` = first 32 chars of base64url(sha256(tag)): the push service keeps only the latest undelivered push per task/thread.

Clear on view: opening a task page closes its `task-<id>` system notification and marks group `task:<id>` read; opening session detail closes its `thread-<id>` notification and the related task notification when present, and marks those groups read (`POST /api/notifications/read` with `groups`). Clicking a notification opens one of these pages. Opening the notifications inbox closes all of them and marks everything read.

Cross-device clear (lazy): every device closes shown `task-*` / `thread-*` notifications whose tag is not in `GET /api/notifications/unread-tags`, i.e. viewed on any device. The service worker does this after showing each push; the app does it on load, when it becomes visible, and on every push; the desktop app does it on every `unread` event of `GET /api/notifications/stream`. No dismiss pushes are sent: iOS revokes subscriptions for pushes that show no notification, and Chromium/Firefox only tolerate a small budget of them. WebKit ignores `close()` on notifications younger than ~30s, so those clear at a later sync. Other tags (e.g. `test`) are never closed by sync.

Service worker: when a window of the app is focused and visible on a Chromium browser, it skips the system notification (the app shows an in-app toast instead); other browsers (Safari, Firefox) always show it, since a push without a notification there can revoke the subscription. Either way it updates the app badge and posts `{ "type": "push" }` to every open window so the app refetches at once.

## Notifications inbox

Every notification the notifier sends is stored first (also when no browser is subscribed), so the app can show in-app toasts and an inbox. Entries older than 30 days are pruned hourly.

`Notification`:
```json
{
  "id": 12,
  "event": "needs_input",
  "taskId": 7,
  "t3ThreadId": "thread-abc",
  "title": "Say OK",
  "projectTitle": "stage",
  "url": "/tasks/7",
  "createdAt": "2026-11-08T10:00:00Z",
  "readAt": null,
  "currentState": "needs_input",
  "pendingQuestionCount": 1,
  "t3DeepLink": "t3code://threads/env-1/thread-abc"
}
```
`event`: `needs_input|ready|done|failed|settled`. `taskId` null for external threads (or once the task is deleted). `title` / `projectTitle` are snapshots at send time. `currentState` is live: the task's state, or for an external thread `settled` when settled, else its `observedState`; null when unknown. `pendingQuestionCount` is live too: answerable questions the task's thread (or the external thread) is waiting on.

Task notifications retain `/tasks/<id>` in their stored `url` to preserve grouped identity after restart/deletion. Push payloads and in-app navigation open `/sessions/<encoded-thread-id>` when a thread exists; tasks without a thread retain the task destination. External entries store their session URL.

### `GET /api/notifications`
Query: `limit` (default 50, max 200), `before` (id; only older entries), `group` (optional group key from the grouped inbox; restricts history to that task/thread). 200 `{ "notifications": Notification[], "unread": number, "nextBefore": number | null }` newest first; `nextBefore` = the `before` for the next (older) page, null when none. `unread` remains the global unread event count. 400 bad `limit` / `before`.

### `GET /api/notifications/groups`
Query: `limit` (default 50, max 200), `cursor` (opaque `nextCursor` from the previous page). 200 `{ "groups": NotificationGroup[], "unread": number, "latestId": number, "nextCursor": string | null }`. 400 bad `limit` / `cursor`.

`NotificationGroup` extends `Notification` with `key` (stable task/thread identity), `eventCount` (retained notifications for this group), and `unreadCount`. Its inherited notification fields describe the latest event; `currentState` stays live. Group by task across restarts, or by external thread, never by title. Deleted tasks retain their group via their task URL. Identity-less entries remain separate.

Groups whose current state is `needs_input` come first, then other groups; each section is newest-event-ID first. Aggregation precedes pagination, so repeated events cannot consume a page or cause older copies of a group to reappear. `latestId` is the global newest event ID captured before listing, independent of priority order; use it for read-on-open, then the newest observed `latestId` for arrivals while the drawer is open. Events inserted after that boundary remain unread. Unread markers captured on open stay visible until close. The bell still counts unread events.

Expandable history uses `GET /api/notifications?group=...`; it contains only recorded `needs_input`, `ready`, `done`, `failed`, and `settled` notifications, not every lifecycle transition. Unknown current state displays as unavailable rather than falling back to an old event.

### `POST /api/notifications/read`
Body `{ "upTo": number }`: marks every unread entry with `id <= upTo` read. Or `{ "groups": string[] }` (1 to 20 group keys, e.g. `task:12`, `thread:<t3ThreadId>`): marks every unread entry in those groups read. 204. 400 when neither or both are sent, `upTo` < 1, or `groups` is empty, too long or has an empty key.

### `GET /api/notifications/unread-tags`
200 `{ "tags": string[] }`: push tags of groups with unread entries (`task:<id>` → `task-<id>`, `thread:<id>` → `thread-<id>`; identity-less groups have none). Devices close shown notifications whose tag is missing (see "Clear on view").

### `GET /api/notifications/stream`
Server-sent events: inbox rows after a cursor, then live rows and unread snapshots (the desktop app's native notifications and badge). Headers `Content-Type: text/event-stream`, `Cache-Control: no-cache, no-transform`, `X-Accel-Buffering: no`; first line `retry: 3000`. 400 when the cursor is not an integer >= 0.

Cursor = a notification id: `Last-Event-ID` (reconnect) wins over `?after`. No cursor → cursor = the newest id (no replay). A cursor above the newest id (e.g. a reset database) → cursor = the newest id and `reset: true`.

Events, in order:
- `hello` (`id: <cursor>`): `{ "latestId": number, "reset": boolean }`. `latestId` = newest id at connect (0 when empty).
- `gap` (no id): `{}`, sent when more than 100 rows follow the cursor; only the newest 100 are replayed.
- `notification` (`id: <id>`), per row id > cursor, ascending: `{ "id": 12, "event": "needs_input", "createdAt": "2026-11-08T10:00:00Z", "read": false, "tag": "task-7", "title": "Needs input", "body": "Say OK · stage", "url": "/sessions/thread-abc#answer" }`. `tag`, `title`, `body` and `url` are the Push payload of that row; `tag` is `""` for identity-less rows. Replay includes rows already read (`read: true`).
- `unread` (no id): `{ "tags": string[], "badge": number }`: the `GET /api/notifications/unread-tags` tags and `GET /api/badge` `needsInput`. Sent once after the replay, then only when it changes.

Live: every new inbox row, read (`POST /api/notifications/read`), prune and task transition wakes the stream; it sends rows after the last one sent (no cap), then `unread` if changed. `: ping` every 15s, which also recomputes `unread` (external threads' needs-input changes reach the badge within 15s). The stream ends when the server shuts down; reconnect with `Last-Event-ID`.

## Notification rules

- Filter: an event not in `preferences.notifyEvents` (read when the event is sent) is dropped entirely: no inbox row (so no toast or bell entry) and no push, on any device. If the filter cannot be read, the event notifies.

- Tasks: a transition into `needs_input` ("Needs input"), `done` ("Done"), `failed` ("Failed") or `settled` ("Settled") notifies. Stored `url` = `/tasks/<id>` for stable group identity; push destination = `/sessions/<encoded-thread-id>` when a thread exists, otherwise the task URL. Other states (incl. `interrupted`) do not.
- Ready: a manual tree member with at least one need whose needs all settled (see "Tree rules") notifies once per readiness episode ("Ready", `url` = `/tasks/<id>`, tag `task-<id>`). Roots and auto members never send it.
- External threads: non-subagent threads not owned by a task. Every 5s (when T3 is connected) the watcher reads (`t3_thread_read`) every unsettled root thread with status `preparing|queued|starting|running|waiting`, plus every watched thread whose `observedState` is `running|needs_input|done` (done until settled), and stores the mapped state (State mapping rules) as `observedState`. A change into `needs_input|done|failed|settled` notifies with the same titles; `url` = `/sessions/<encoded-thread-id>`. The first pass after startup records states silently; afterwards a thread seen for the first time notifies only when created after startup. A thread T3 no longer knows becomes `cancelled` silently. Task-owned active sessions also refresh their mirror, even if legacy task state is frozen.
- External threads needing input (badge, `GET /api/threads?needsInput=1`): `observedState = needs_input`, not settled, not subagent, not owned by a task.
- External running threads (Dashboard Running group, `GET /api/threads?running=1`): `observedState = running`, not settled, not subagent, not owned by a task.
- External settled threads (Dashboard Settled group, `GET /api/threads?settledFrom=<iso>`): normalized state `settled`, `settledAt >= settledFrom`, not subagent, not owned by a task; forks and archived threads included.

## State mapping (thread → task), pure function

Input: T3 thread detail (`status`, `settled`, `pendingRequestCount`) and the latest run status (`recentRuns[0].status`, may be absent). First matching rule wins:

| # | Condition | Task state |
|---|---|---|
| 1 | `settled == true` | `settled` |
| 2 | `pendingRequestCount > 0` | `needs_input` |
| 3 | `status in (preparing, queued, starting, running, waiting)` | `running` |
| 4 | `status == completed` | `done` |
| 5 | `status == failed` | `failed` |
| 6 | `status == interrupted` | `interrupted` |
| 7 | `status in (cancelled, rolled_back)` | `cancelled` |
| 8 | `status == idle` and latest run status present | apply rules 3-7 to the latest run status |
| 9 | otherwise (idle, no runs) | unchanged (keep current state) |

T3 `waiting` is the checkpoint gap after a provider turn ends (T3 shows it as "Working"), not a wait on the user; a pending approval or question keeps the thread `running` with `pendingRequestCount > 0`. T3's UI "Waiting" (blocked on a child thread, or awaited background work such as a PR watch after the turn ended) is not exposed over MCP: those threads report `running` or `completed` and map to `running` or `done`.

Applied only to tasks with a thread whose state is not `backlog`, and not to `settled` tasks with dependents (they have been released). T3 reports a cancelled turn (e.g. a denied permission) as thread status `cancelled` and the thread can run on, so a `cancelled` task reopens like a settled one (a cancelled need keeps its dependents blocked until it settles). A change writes a `task_events` row (`detail` = observed T3 status) and sets `finishedAt` / `settledAt` as above.

Polling scope (every 5s, `t3_thread_read`): tasks with a thread in `launching|running|needs_input`, `done` tasks updated in the last hour (older ones at most every 60s), plus `failed|interrupted|cancelled` tasks updated in the last 10 minutes or whose thread mirror (full sync, every 5 min) shows the thread unsettled and active (`preparing|queued|starting|running|waiting` or pending requests); `settled` is not polled. A thread T3 no longer knows moves the task to `failed` (`lastError` "thread not found in T3"). Tasks left in `launching` by a server restart move to `failed` on startup.

## Tree rules

- Needs (create `needs`, `PUT /api/tasks/:id/needs`, tree `needs`), 409 unless noted: change only in `backlog` (an unchanged set is a no-op in any state); not with a start time (400); each need exists (404), is not the task, is in the task's tree, and does not (transitively) need the task (no loops). A settled need is allowed.
- Create with `needs` and no `treeId` joins the needs' tree; needs outside any tree are pulled in; with no tree among them a new `auto` tree is created, titled after the first need. Needs in different trees (or another tree than `treeId`) 409. `needsSessionId` adopts that session first (see "Adopt session") and adds it as the first need. One transaction.
- `start: true` or a start time with needs is 400. A non-backlog task may join a tree only without needs.
- A need is passed when it is `settled`. `failed|interrupted|cancelled` (or still running / `done`) keep dependents `blocked`. Restart the need, change the needs, or start the dependent early.
- `readiness` (backlog members only, else null): `blocked` while a need is not settled; `ready` when every need settled, there is no start time, and the task is manual or has no needs. Roots read `ready` (auto never launches them; the user starts them, or Start ready).
- Release runner (on every transition into `settled` or out of `backlog`, every tree change, at startup and every 10s): launches every `auto` member whose needs all settled (no start time, not adopted); a `manual` member with needs that became ready gets `readyAt` and a `ready: needs settled` event once per episode and notifies `ready`. `readyAt` clears when it stops being ready or launches.
- Splice (remove from tree, delete, move to another tree): each dependent drops the task and takes over its needs (A→X→B becomes A→B, deduplicated); the task loses its needs and leaves the tree; stale `workspace.baseTaskId` values are cleared; an emptied tree is deleted. Splicing an unfinished middle task can make dependents ready at once.
- Moving between trees = remove + add (no merge). `DELETE /api/trees/:id` dissolves a tree.
- A settled task with dependents is frozen: its thread no longer changes its state.

## Adopt session

`POST /api/threads/:id/adopt` ("Track as task") and `needsSessionId` ("afterMe") make a top-level T3 session a task:

- `adopted: true`, `prompt: ""`, title from the request, else the session title, else `Session <id8>`; model, runtime and interaction mode copied from the session; state from the session (State mapping, idle reads `done`); `launchAttempt` 1. Task event `adopted session`.
- stage never starts, restarts or duplicates it (409); follow-up and stop work; sync keeps its state.
- A session a task already owns returns that task (200; `needsSessionId` uses it as the need). Unknown session 404; a subagent, an archived session, or one whose project is not in stage 409.
- With `treeId` the task joins that tree (no needs).

## Handoff

When a task with needs launches (release runner, Start, or restart from `failed|interrupted|cancelled`) with `handoff` on, every need that is `settled` or `done` and has a thread is a source (by need id). stage reads each from T3 (`t3_thread_read` and its latest assistant reply, 20s budget each) and sends `prompt + "\n\n" + blocks` (one block per source, joined by a blank line):

```
<handoff from="stage">
This task needs "Build API" (task #12), settled 2026-11-14T10:02:00Z.
- T3 thread: <threadId> <link> (read more with t3_thread_read)
- Branch: feat/api · Worktree: /path/to/worktree
- Pull requests:
  - owner/repo#41 (open) https://github.com/owner/repo/pull/41
Final reply:
<the need's final assistant reply, at most N chars>
[truncated at N chars; full reply via t3_thread_read <threadId>]
</handoff>
```
- A `done` need reads `finished, not settled yet` instead of `settled <time>` (start early). Empty branch/worktree and PR lines are omitted; no reply gives `Final reply: none found.`; the truncation line appears only when the reply is longer.
- Reply cap per need: `min(4000, max(1000, 8000 / sources))` chars.
- Composed at launch. Each attempt records one `Handoff` per source in `task_handoffs` plus a `launching` task event (`handoff from #12 appended (N chars)`). A source this attempt already sent (lost launch response, same clientRequestId) resends the stored text.
- A failed read never blocks the launch: that source is recorded as `skipped` with `reason` (event `handoff from #12 skipped: <reason>`) and left out; the others are sent.

`Handoff`:
```jsonc
{ "attempt": 1, "status": "sent", // sent | skipped
  "sourceTaskId": 12,             // null only for records from before trees (a session source)
  "sourceThreadId": "thread-abc", "sourceTitle": "Build API",
  "text": "<handoff ...",          // "" when skipped
  "reason": null,                  // why skipped
  "at": "2026-11-14T10:02:01Z" }
```

## Predecessor base

A task with `workspace.base` `predecessor` (opt-in per task, default `ref`) starts its T3 worktree from a need's branch, so stacked work builds on unmerged changes. The need is `workspace.baseTaskId` when it is a need, else the sole need. Resolved at launch (release runner, Start, restart), from the same live `t3_thread_read` as the handoff:

- Uses the need's local branch: `{type:"worktree", baseRef: <branch>, startFromOrigin: false, branch: <the task's branch, else T3 picks>}`. Worktrees share refs, so the need's worktree branch resolves in the project repo, unpushed commits included.
- Falls back to the task's own `baseRef` (same `startFromOrigin` rule as `Workspace`) when, in order: the task has no needs; it has several needs and no `baseTaskId` among them; the chosen need has not finished or has no thread; the read fails; the need ran in another T3 project; it ran at project root (no worktree); it has no branch; it has pull requests and all are `merged` (its work is already on the default branch).
- T3 rejects a base ref that does not resolve before creating a thread (`invalid_request`, `Base ref "<branch>" does not resolve…`; e.g. the branch was deleted). stage then relaunches once from the fallback with the same clientRequestId and records reason `branch <x> not found in project`.
- The launch is never blocked by the base. Each attempt records `LaunchBase` in `task_launch_bases` plus a `launching` task event: `base from #12's branch feat/api` or `base fallback origin/main: <reason>`. An attempt that reuses its clientRequestId resends the stored base.
- The task's own branch name is unchanged (`workspace.branch`, else T3 picks). A branch equal to the need's fails in T3 (already checked out).

`LaunchBase`:
```jsonc
{ "attempt": 1, "baseRef": "feat/api", "startFromOrigin": false,
  "source": "predecessor",          // predecessor (a need's branch) | fallback
  "sourceTaskId": 12,               // null when no need was chosen
  "sourceThreadId": "thread-abc",   // null when no need was chosen
  "sourceTitle": "Build API",
  "reason": null,                   // why it fell back
  "at": "2026-11-14T10:02:01Z" }
```

## Session batch

A session batch is the set of tasks one session created (`originThreadId`), usually a plan an agent broke into tasks.

`CreatedTasks`:
```jsonc
{
  "trees": [{ "tree": { "id": 3, "title": "Ship auth", "startMode": "auto" }, "tasks": [Task, Task] }],
  // batch tasks per tree, ordered by each group's lowest task id; tasks by id
  "independent": [Task]           // batch tasks outside any tree, by id
}
```
- A tree group lists only batch tasks; the tree may hold others (e.g. the adopted session of `afterMe`, which is in the batch only when that session created it).

## Agent interface

`stage` subcommands (CLI, `--json`), `stage mcp` (stdio MCP) and `POST /mcp` (streamable HTTP MCP, stateless, served by the app; other methods 405) expose the same operations, all through this REST API (HTTP MCP serves `/api` in-process, no network dial). Tools: `get_context list_projects list_models list_tasks get_task create_task update_task delete_task start_task stop_task followup_task duplicate_task wait_task list_trees get_tree create_tree update_tree add_to_tree remove_from_tree set_needs start_tree adopt_session list_schedules get_schedule create_schedule update_schedule delete_schedule run_schedule preview_schedule list_sessions search_sessions get_session get_session_questions read_ref` (`read_ref` / `stage show REF` resolve a pasted reference via `GET /api/refs/resolve` and return `{ kind, task: TaskDetail|null, session: ThreadDetail|null, next }`: a task reference carries its session, a session reference its owning task, `next` points at `t3_thread_read` for the full conversation; `get_session_questions` / `stage sessions questions ID` read pending questions; agents cannot answer them). `search_sessions` (`stage sessions search QUERY [--project P] [--limit N]`) wraps `GET /api/threads/search` and returns `{ results: [{ t3ThreadId, title, match, snippet?, project?, state?, taskId?, lastWordPreview?, t3WebUrl? }], t3 }`. Results are the REST shapes (lists as compact rows; `list_tasks` rows carry `treeId`, `needs`, `readiness`, `adopted` and `lastWordPreview`, `list_sessions` rows `lastWordPreview`; `get_task` and `get_session` carry the excerpt in `thread.lastWord`, and the CLI prints it as "last word"; `get_task` and `get_session` also carry `pullRequests` and `thread.recentRuns`, which the CLI prints as a `prs` block and a `runs` block: newest 10, `(10 of N runs)`, `--json` has every stored run); errors are tool errors carrying the API `error`.

- Caller: `T3CODE_THREAD_ID` and cwd from the environment (CLI, stdio), or tool args `callerThreadId` / `cwd` (override; required for context over HTTP).
- Project: explicit `project` (id or exact title) > the caller session's project (`GET /api/threads/:id`) > the project whose `workspaceRoot` is the longest prefix of cwd.
- Model: explicit (`provider` inferred from `model` via the catalog) > `preferences.pinned` > `preferences.lastUsed` > T3 default.
- `get_session` returns `createdTasks` (see "Session batch"); `get_task` returns `batch` and `tree`.
- `create_task` / `create_schedule` set `originThreadId` to the caller session. `afterMe` = `needsSessionId` of the caller session (adopted into the tree as a task; error without a session).
- Trees: `create_task` takes `treeId`, `needs`, `startMode` (`auto|manual|inherit`) and `baseTaskId` (CLI `task add --tree ID --needs 3,5 --start-mode M --base-task ID --after-me`); `update_task` takes `startMode` (`inherit` = null) and `baseTaskId` (0 = the sole need; CLI `task edit --start-mode --base-task`). `set_needs {taskId, needs}` (CLI `task needs ID [NEED...]`, none clears). `create_tree {title, startMode?, tasks: [{id, needs?}]}` (CLI `tree new --title T [--start-mode M] ID[:NEED,NEED]...`), `get_tree {id}` (CLI `tree show ID`: levels with state, `#id`, title, needs, readiness), `list_trees {all?, project?}` (compact rows `{id, title, startMode, tasks, levels, projects, states, settledAt}`, `states` = non-zero counts with `blocked`/`ready` split from `backlog`; CLI `tree ls [--all] [--project P]`), `update_tree {id, title?, startMode?}` (CLI `tree edit`), `add_to_tree {treeId, taskId, needs?}` (CLI `tree add TREE TASK [--needs ..]`), `remove_from_tree {taskId}` (looks up the task's tree; CLI `tree rm TASK`), `start_tree {id}` (CLI `tree start ID`). Dissolving a tree is REST/web only.
- `adopt_session {threadId?, title?, notes?, tags?, treeId?}` (default: the caller session) returns `{ task, created }` (CLI `sessions adopt [THREAD_ID] [--title --notes --tag --tree]`).
- `start_task` on a blocked task starts it early (needs kept).
- `create_task` and `update_task` take optional `handoff` (CLI `task add --handoff=false`, `task edit --handoff=true|false`); omitted keeps the task's value (default true). `get_task` returns `handoff` and `currentHandoffs`; `stage task show` prints `handoff  on|off` for tasks with needs and the current attempt's records, plus `tree` and `readiness` lines.
- `create_task`, `create_schedule`, `update_task` and `update_schedule` take optional `base` (`ref|predecessor`; CLI `task add|edit --base`). On create it needs `baseRef` (the fallback); on `update_task` without `baseRef` it keeps the task's current worktree (error for a root task). `get_task` returns `currentLaunchBase`; `stage task show` prints `worktree from #12's branch (fallback origin/main, branch -)` and, once launched, `base     feat/api (from #12)` or `base     origin/main (fallback: <reason>)`.
- `imagePaths` reads local files and uploads them (`POST /api/uploads`); CLI and stdio only (HTTP MCP rejects them).
- `wait_task` polls `GET /api/tasks/:id` every 3s until the state is in `until` (default `needs_input done settled failed interrupted cancelled`) or `timeoutSeconds` (default 600, max 3600); CLI `task wait` exits 3 on timeout.
