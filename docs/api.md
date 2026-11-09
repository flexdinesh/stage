# t3-stage REST API

Base path `/api`. JSON in and out (`Content-Type: application/json`). Field names camelCase. TypeScript mirror: `web/src/api/types.ts` (change both together).

## Conventions

- Timestamps: ISO 8601 strings (UTC, e.g. `"2026-11-08T10:00:00Z"`). Nullable fields are `null`, never omitted.
- IDs: app ids (`id`, `projectId`, `taskId`, `afterTaskId`, `nextTaskId`) are JSON numbers. T3 ids (`t3ProjectId`, `t3ThreadId`) are strings.
- Lists are wrapped: `{ "projects": [...] }`, `{ "tasks": [...] }`, ...
- PATCH bodies are partial: omitted fields are unchanged; `null` clears a nullable field.
- Repeatable list query params accept repetition or commas: `?state=running&state=needs_input` == `?state=running,needs_input`.
- `from` / `to` query params: ISO date (`2026-11-01`) or datetime. `from` inclusive, `to` exclusive. Filter on `createdAt` unless stated.
- Errors: non-2xx responses have body `{ "error": string }`.

| Status | Meaning |
|---|---|
| 400 | invalid body or query (bad enum, missing required field, bad JSON) |
| 404 | resource not found |
| 409 | conflict: action not allowed in current state, duplicate unique value |
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
| ProjectSource | `app` (created via t3-stage) `t3` (imported from T3) |
| ThreadSource | `app` (thread belongs to a task) `t3` (any other thread) |

## Shared shapes

```jsonc
// ModelOption: T3 ProviderOptionSelection
{ "id": "effort", "value": "low" }          // value: string | boolean

// Workspace
{ "type": "root" }
{ "type": "worktree", "baseRef": "origin/main", "branch": "feat-x" }   // branch: string | null (null = T3 picks)
```

`Workspace` maps to T3 `workspaceStrategy`: root → `{type:"root"}`; worktree → `{type:"worktree", baseRef, branch?, startFromOrigin: baseRef has a "<remote>/" prefix}` (remote = `origin` or `upstream`; `feature/x` is treated as local).

## Health

### `GET /api/health`
200 / 503:
```json
{ "status": "ok", "db": "ok" }
```

## T3 connection, sync, catalog

### `GET /api/t3/status`
200 `T3Status`:
```json
{
  "connected": true,
  "t3Url": "http://127.0.0.1:3773",
  "publicUrl": "http://controlbox:3773",
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
Run a full sync now (all T3 projects + threads into the mirror; creates app projects for new T3 projects; project title, root and archive state follow T3). Synchronous.
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
  "title": "t3-stage",
  "workspaceRoot": "/home/dee/workspace/t3-stage",
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
  "projectTitle": "t3-stage",
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
  "afterTaskId": null,
  "nextTaskId": null,
  "attachments": [{ "id": 4, "name": "error.png", "mimeType": "image/png", "sizeBytes": 48211 }],
  "launchAttempt": 1,
  "lastError": null,
  "t3WebUrl": "http://controlbox:3773/env_123/thread_abc",
  "t3AppUrl": "https://app.t3.codes/threads/env_123/thread_abc",
  "launchedAt": "2026-11-08T10:00:00Z",
  "finishedAt": null,
  "settledAt": null,
  "createdAt": "2026-11-08T09:59:00Z",
  "updatedAt": "2026-11-08T10:00:00Z"
}
```
- `providerInstanceId` / `model` null = T3 default; they must be set together (400 otherwise). `modelOptions` default `[]`.
- `afterTaskId`: runs after that task (launches when it is settled or cancelled; see "Link rules"). `nextTaskId`: the task that runs after this one. Both nullable.
- `attachments`: the task's images (`Attachment`, upload order), sent with the prompt on every launch. Default `[]`. See "Images".
- `t3WebUrl` = `T3_PUBLIC_URL/<environmentId>/<t3ThreadId>` (null without thread or environment id). `t3AppUrl` = T3 thread `link` as served to external MCP clients (`https://app.t3.codes/threads/<env>/<thread>`; null without thread).
- `finishedAt`: first entry into `done|failed|interrupted|cancelled` (per launch attempt). `settledAt`: entry into `settled`.

`TaskEvent`:
```json
{ "id": 3, "taskId": 7, "fromState": "launching", "toState": "running", "detail": "thread status running", "at": "2026-11-08T10:00:01Z" }
```
`fromState` null for the creation event.

`TaskDetail` = `Task` + `{ "events": TaskEvent[] (oldest first), "thread": Thread | null }`.

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
  "afterTaskId": null,           // optional: run after this task (link rules)
  "attachmentIds": [4],          // optional: staged uploads to attach (max 8)
  "start": false                 // optional: true = Save & start (ignored when afterTaskId set)
}
```
201 `Task` (state `backlog`, or `launching` when started). 400 (incl. an `attachmentIds` entry that is not a staged upload, more than 8). 404 project / afterTaskId. 409 link rule broken. Start errors after save: task is still created; it lands in `failed` with `lastError`.

### `GET /api/tasks/:id`
200 `TaskDetail`. 404.

### `PATCH /api/tasks/:id`
Body `UpdateTaskRequest`: any of `projectId title prompt providerInstanceId model modelOptions runtimeMode interactionMode workspace tags notes` `afterTaskId` `attachmentIds`.
In `backlog` all fields are editable; otherwise only `title`, `tags`, `notes` (409 for others). `attachmentIds` is the exact image set: the task's own ids to keep plus staged uploads to add; the task's images not listed are deleted (400 for ids that are neither, more than 8). `afterTaskId`: set links per "Link rules" (409 / 404); `null` unlinks (any state). "Then run" on task A = `PATCH` the follower with `afterTaskId: A`.
200 `Task`. 404.

### `DELETE /api/tasks/:id`
204. 409 when state is `launching|running|needs_input` (stop first). Its follower is relinked to its predecessor (A→X→B becomes A→B; B unlinked when X had none).

### `POST /api/tasks/:id/start`
Launch in T3 (`t3_thread_launch`, `clientRequestId = "task-<id>-<uuid>"`, new attempt, new `clientRequestId` and new thread each start). T3 dedupes launches by `clientRequestId` across clients; the random part keeps other databases (dev, tests, restored backups) on the same T3 from colliding. A launch answered with a thread in another project than the task's is a collision: the task moves to `failed` without adopting the thread. Allowed from `backlog|failed|interrupted|cancelled`; else 409. "Start now": a `backlog` task waiting on a predecessor that is not settled or cancelled is unlinked from it first (its own follower stays linked). Each start clears `t3ThreadId`, `lastError`, `finishedAt`, `settledAt`; the previous thread stays in history (as `source: "t3"`).
A transport failure (timeout, network, dropped session; not a T3 tool error) is retried once with the same `clientRequestId`. If that also fails the task moves to `failed` and the next start reuses the attempt (same `launchAttempt` and `clientRequestId`), so T3 returns the thread if the lost launch did create one. The same applies to a task left in `launching` by a server restart (failed at startup with `launch interrupted by server restart`). On shutdown the server waits up to 100s for in-flight launches (compose `stop_grace_period: 2m`).
Images: before each launch every task image is uploaded to T3 afresh (`t3_attachment_prepare_upload`, then the bytes POSTed to the signed `relativeUrl` on `T3_URL`) and passed as `attachments` to `t3_thread_launch`. A failed upload or launch fails the start (`lastError`) and discards that start's uploads (best effort).
200 `Task` (state `launching` or later). 502 / 503 (task moves to `failed` with `lastError` on T3 error).

### `POST /api/tasks/:id/followup`
Body `{ "message": string, "mode"?: SendMode, "attachmentIds"?: number[] }`. Without images: `t3_thread_send` (message required). With `attachmentIds` (staged uploads, max 8): the images are uploaded to T3 and sent with the optional message as one new message (`t3_thread_send_attachments`); `mode` is not allowed (400). Sent images are deleted; on a T3 error they stay staged (no automatic retry). 409 when task has no thread.
200 `Task`. 400. 502 / 503.

### `POST /api/tasks/:id/stop`
`t3_thread_interrupt` (stop). State updates via poller. 409 when task has no thread.

Follow-up and stop bump `updatedAt`; that re-arms polling of a `failed|interrupted` task for 10 minutes (see polling scope). They then poll the thread once: the thread mirror is refreshed, but task state does not change for `backlog` tasks or `settled`/`cancelled` tasks that have a follower (one without may reopen).
200 `Task`. 502 / 503.

### `POST /api/tasks/:id/duplicate`
New `backlog` task with the same project, title, prompt, images (copied), model config, workspace, tags, notes; not linked.
201 `Task`. 404.

## Images

`Attachment`: `{ "id": 4, "name": "error.png", "mimeType": "image/png", "sizeBytes": 48211 }`. Types: `image/png|jpeg|gif|webp` (sniffed from the bytes), max 10 MB each, max 8 per task or follow-up. Stored in Postgres. An upload is *staged* until a task create/update attaches it or a follow-up sends it; staged uploads older than 24h are deleted hourly.

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
  "projectTitle": "t3-stage",
  "title": "Say OK",
  "status": "completed",
  "settled": false,
  "settledAt": null,
  "pendingRequestCount": 0,
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
  "t3WebUrl": "http://controlbox:3773/env_123/thread_abc",
  "t3AppUrl": "https://app.t3.codes/threads/env_123/thread_abc",
  "createdAt": "2026-11-08T10:00:00Z",
  "updatedAt": "2026-11-08T10:01:00Z",
  "syncedAt": "2026-11-08T10:01:05Z"
}
```
`projectId` / `projectTitle` null when no app project mirrors `t3ProjectId`. `relationship`: `fork|subagent|null`. `creationSource`: T3 `web|mobile|mcp|provider|server`.

### `GET /api/threads`
Query (all optional): `project` (app project id), `status` (repeatable ThreadStatus), `settled` (`true|false`), `provider` (providerInstanceId), `source` (`app|t3`), `q` (case-insensitive title substring), `from`, `to` (createdAt), `subagents` (`include|exclude`, default `include`; `exclude` = root threads only, `parentThreadId` null), `needsInput` (`1|true`: only external threads counted by the badge, see "Notification rules"; other filters still apply), `running` (`1|true`: only external running threads, see "Notification rules"; other filters still apply), `limit` (default 100, max 500), `offset` (default 0).
200 `{ "threads": Thread[], "total": number }` ordered by `createdAt` desc; `total` = matches before limit/offset.

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

New-task model defaults. A new task prefills from `pinned`, else `lastUsed`, else the built-in default (T3 default model, `full-access`, `default`).

`ModelDefaults`:
```json
{ "providerInstanceId": "claude", "model": "claude-haiku-5-5", "modelOptions": [{ "id": "effort", "value": "low" }], "runtimeMode": "full-access", "interactionMode": "default" }
```
`providerInstanceId` / `model` null = T3 default (set together).

### `GET /api/preferences`
200 `{ "pinned": ModelDefaults | null, "lastUsed": ModelDefaults | null }`. `lastUsed` is written by every `POST /api/tasks` (its model fields).

### `PUT /api/preferences/pinned`
Body: `ModelDefaults` (pin; `runtimeMode` / `interactionMode` default to `full-access` / `default` when empty) or `null` (unpin). Empty body 400.
200 `Preferences`. 400 invalid enum or provider/model not set together.

## Stats

### `GET /api/stats`
Query: `from`, `to` (default last 30 days: `to` = now, `from` = now - 30d), `tz` (IANA zone for day buckets, default `UTC`), `subagents` (`include|exclude`, default `include`; `exclude` drops subagent threads from all thread counts). Thread counts use mirrored threads by `createdAt` in range; task counts are current totals (not range-filtered).
200 `Stats`:
```json
{
  "from": "2026-10-09T10:00:00Z",
  "to": "2026-11-08T10:00:00Z",
  "tz": "UTC",
  "threadsByDay": [{ "day": "2026-11-08", "count": 14 }],
  "threadsByProject": [{ "projectId": 1, "t3ProjectId": "proj_abc", "title": "t3-stage", "count": 9 }],
  "threadsByStatus": [{ "status": "completed", "count": 20 }],
  "tasksByState": [{ "state": "running", "count": 2 }],
  "needsInput": 1
}
```
`threadsByDay` ascending, days with zero omitted. `threadsByProject` count desc; `projectId` null and `title` = `t3ProjectId` when no app project. `needsInput` = tasks in `needs_input`. 400 bad `tz` or `subagents`.

## Notifications (Web Push)

The server sends Web Push (VAPID, RFC 8291 aes128gcm) to every subscribed browser. The VAPID key pair is generated on first start and stored in Postgres; the VAPID subject is `APP_PUBLIC_URL`.

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
JSON, shown by the service worker:
```json
{ "title": "Needs input", "body": "Say OK · t3-stage", "url": "/tasks/7", "tag": "task-7", "badge": 2 }
```
`tag` replaces an older notification for the same task (`task-<id>`) or thread (`thread-<t3ThreadId>`); the service worker sets `renotify` so a replacement alerts again. iOS ignores tag replacement (WebKit bug 258922), so the service worker also closes shown notifications with the same tag (`tag`, or the copy in `data.tag`) before showing the new one. `badge` = `GET /api/badge` at send time. TTL 24h; urgency `high` for needs input, `normal` otherwise. Header `Topic` = first 32 chars of base64url(sha256(tag)): the push service keeps only the latest undelivered push per task/thread.

Clear on view: opening a task page closes its `task-<id>` system notification; opening the notifications inbox closes all of them.

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
  "projectTitle": "t3-stage",
  "url": "/tasks/7",
  "createdAt": "2026-11-08T10:00:00Z",
  "readAt": null,
  "currentState": "needs_input",
  "t3DeepLink": "t3code://threads/env-1/thread-abc"
}
```
`event`: `needs_input|done|failed`. `taskId` null for external threads (or once the task is deleted). `title` / `projectTitle` are snapshots at send time. `currentState` is live: the task's state, or for an external thread `settled` when settled, else its `observedState`; null when unknown.

### `GET /api/notifications`
Query: `limit` (default 50, max 200), `before` (id; only older entries), `group` (optional group key from the grouped inbox; restricts history to that task/thread). 200 `{ "notifications": Notification[], "unread": number, "nextBefore": number | null }` newest first; `nextBefore` = the `before` for the next (older) page, null when none. `unread` remains the global unread event count. 400 bad `limit` / `before`.

### `GET /api/notifications/groups`
Query: `limit` (default 50, max 200), `cursor` (opaque `nextCursor` from the previous page). 200 `{ "groups": NotificationGroup[], "unread": number, "latestId": number, "nextCursor": string | null }`. 400 bad `limit` / `cursor`.

`NotificationGroup` extends `Notification` with `key` (stable task/thread identity), `eventCount` (retained notifications for this group), and `unreadCount`. Its inherited notification fields describe the latest event; `currentState` stays live. Group by task across restarts, or by external thread, never by title. Deleted tasks retain their group via their task URL. Identity-less entries remain separate.

Groups whose current state is `needs_input` come first, then other groups; each section is newest-event-ID first. Aggregation precedes pagination, so repeated events cannot consume a page or cause older copies of a group to reappear. `latestId` is the global newest event ID captured before listing, independent of priority order; use it for read-on-open, then the newest observed `latestId` for arrivals while the drawer is open. Events inserted after that boundary remain unread. Unread markers captured on open stay visible until close. The bell still counts unread events.

Expandable history uses `GET /api/notifications?group=...`; it contains only recorded `needs_input`, `done`, and `failed` notifications, not every lifecycle transition. Unknown current state displays as unavailable rather than falling back to an old event.

### `POST /api/notifications/read`
Body `{ "upTo": number }`: marks every unread entry with `id <= upTo` read. 204. 400 missing `upTo`.

## Notification rules

- Tasks: a transition into `needs_input` ("Needs input"), `done` ("Done") or `failed` ("Failed") notifies; `url` = `/tasks/<id>`. Other states (incl. `interrupted`) do not. Links add no events of their own.
- External threads: non-subagent threads not owned by a task. Every 5s (when T3 is connected) the watcher reads (`t3_thread_read`) every unsettled root thread with status `preparing|queued|starting|running|waiting`, plus every watched thread whose `observedState` is `running|needs_input`, and stores the mapped state (State mapping rules) as `observedState`. A change into `needs_input|done|failed` notifies with the same titles; `url` = `/history?q=<title>`. The first pass after startup records states silently; afterwards a thread seen for the first time notifies only when created after startup. A thread T3 no longer knows becomes `cancelled` silently.
- External threads needing input (badge, `GET /api/threads?needsInput=1`): `observedState = needs_input`, not settled, not subagent, not owned by a task.
- External running threads (Dashboard Running group, `GET /api/threads?running=1`): `observedState = running`, not settled, not subagent, not owned by a task.

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

Applied only to tasks with a thread whose state is not `backlog`, and not to `settled` or `cancelled` tasks with a follower (it has been released). T3 reports a cancelled turn (e.g. a denied permission) as thread status `cancelled` and the thread can run on, so a `cancelled` task without a follower reopens like a settled one. A change writes a `task_events` row (`detail` = observed T3 status) and sets `finishedAt` / `settledAt` as above.

Polling scope (every 5s, `t3_thread_read`): tasks with a thread in `launching|running|needs_input`, `done` tasks updated in the last hour (older ones at most every 60s), plus `failed|interrupted|cancelled` tasks updated in the last 10 minutes or whose thread mirror (full sync, every 5 min) shows the thread unsettled and active (`preparing|queued|starting|running|waiting` or pending requests); a `cancelled` task with a follower is excluded. `settled` is not polled. A thread T3 no longer knows moves the task to `failed` (`lastError` "thread not found in T3"). Tasks left in `launching` by a server restart move to `failed` on startup.

## Link rules

- A task runs after at most one task (`afterTaskId`) and has at most one follower (`nextTaskId`): a run order is a linear list. Links may span projects.
- Linking (create or `PATCH afterTaskId`), 409 otherwise: the task is `backlog`; the predecessor exists (404), is not the task, is not `settled|cancelled`, has no other follower, and is not already after the task (no loops).
- A predecessor is passed when it is `settled` or `cancelled`. The link runner launches every `backlog` task whose predecessor has passed: on each transition into `settled|cancelled`, at startup and every 10s. The link stays (history).
- A `failed|interrupted` (or still running / `done`) predecessor keeps its follower waiting. Restart the predecessor, unlink the follower, or "Start now" on it.
- `GET /api/tasks/:id` returns `sequence`: the whole run order containing the task, head first.
