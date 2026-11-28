# ADR 0014: Session surface

Accepted on 2026-11-28. Planned in Stage task #530 (tree #215, journal
entry #5766). The contract ships in task #531 (01). The plan calls this
ADR 0012. [Harness workspace add](0012-harness-workspace-add.md) and
[tree steering](0013-tree-steering.md) already occupy 0012 and 0013.

When task 03 ships, the bounded activity view supersedes the earlier
[PRODUCT Transcript rule](../../PRODUCT.md#capabilities-and-constraints)
and [DESIGN Transcript rule](../../DESIGN.md#transcript). The per-task
attempt switcher supersedes the "No session list" clause of
[DESIGN Sessions](../../DESIGN.md#sessions). Full transcripts and global
session browsing stay in the harness.

Task 06 revises [ADR 0006 decision 7](0006-codex-app-server-harness.md#decisions)
for Codex approvals. The base already answers command and file-change
approvals and `requestUserInput` questions through `thread/resume`.
The target adds permissions approvals and holds the subscription.
This ADR extends [ADR 0007 decision 9](0007-harness-boundary.md#decisions)
with additional optional, declared session operations.

## Context

Task detail combines Stage's durable task with the harness session that
runs it. Split those surfaces so people can review the task, inspect the
current or an earlier attempt, and answer requests or deliver messages
through the native operations the harness exposes. Each harness gets the
shared session view and can add its own sections.

## Decisions

### D1. Boundary

- **Task area.** Title, prompt, images and handoffs, including the
  opening prompt of an adopted task. Acceptance results, State picker,
  Tree, journal and timeline. Config holds workspace, schedule, needs,
  attempts, dates and destination. Task actions are Start/Restart, Edit,
  Duplicate, Archive and Delete.
- **Session area, per session.** Header with harness, attempt switcher,
  state and reason, and session ID. Open in {harness} or Copy resume,
  Stop, and declared session actions. `LaunchRecovery`, requests,
  composer, last reply, recent activity and harness slot sections.
  Usage, launch metadata, reported branch and directory, and facts.

### D2. Layout

- URL-addressable **Task** and **{Harness} session** tabs sit under the
  header on phone and laptop. The routes are `/tasks/:id` (default),
  `/tasks/:id/session` and `/tasks/:id/session/:attempt`.
- The needs-input alert sits above the tabs on both views. The Task tab
  offers an **Answer** arrow link to `/tasks/:id/session#requests`.
  Needs-input notification and row links go to `/tasks/:id/session`.

### D3. Sessions

- Task 02 records the session for every attempt in `task_sessions`.
- The switcher lists attempts newest first. A task still has one current
  session, and only that session is interactive.
- Past sessions are read-only snapshots of the last observation, reply,
  usage, facts and links, with activity read by session reference.

### D4. Operations

- Existing operations are `respond` and `send` (start a new turn after
  the previous turn ends).
- New typed operations are `steer` (inject into the in-progress turn),
  `queue` (hold the next turn in the harness until the current one ends),
  `activity` (bounded read) and `queueManage` (list, cancel or promote
  native queued messages, task 09).
- `Message.Delivery` is `send|steer|queue`. The coordinator gates the
  matching operation. Adapters return `Busy` with guidance when the
  state does not fit. Timeline events omit message text.
- HTTP, MCP and CLI have operation parity and apply the
  `self_action_forbidden` guard.
- The mode split ships for OpenCode in task 04 and T3 in task 05.

### D5. Native delivery

- The harness owns message storage and scheduling. Stage relays messages
  through documented mechanisms.
- Claude Code uses documented hooks. The adapter's in-memory pending list
  is delivered on the next `PostToolUse` (`additionalContext`) or `Stop`
  (decision block). Codex steer uses stable `turn/steer`.
- The target excludes Codex experimental APIs (`thread/queue/*`,
  `requestUserInput`) and Claude Agent SDK, channels, stream-json and the
  control socket.
- T3 approvals stay **Answer in T3**. MCP has no approval tools, and the
  WebSocket API is unverified (follow-up).

### D6. Activity

- Recent activity returns the last 30 items by default, up to 100. Kinds
  are `message_user|message_agent|reasoning|tool|command|file_change|plan|event`.
  It includes plan steps, changed files (+/-), model and a truncated
  flag. Harness JSON in `details` is capped at 16 KiB and rendered only
  by that harness's UI slot.
- The visible view polls every 5 s while running or in `needs_input`.
  **Full transcript in {harness}** uses the existing destination.
- Surfaces are `GET /api/tasks/:id/activity?attempt=&limit=`, MCP
  `get_session_activity` and CLI `stage task activity`.

### D7. UI architecture

- Shared `SessionView` uses generic sections gated by operations in
  `packages/app/src/components/session/`.
- `HarnessSlots.session?: { HeaderExtra?, Sections? }` takes
  `{task, session, activity, current}`. The generic entry has no slot.
- The composer has one textarea. While running it offers **Steer** and
  **Queue**. After the turn ends it offers **Send**. Otherwise it shows
  the declared reason and Open in {harness} or Copy resume. The draft
  persists across tab switches.

### D8. Boundary tests

- Harness-name branches are confined to adapters and `src/harnesses`.
- Every control reads operations and their declared reasons.

### D9. Worker parity

- Worker channel kinds are `adapter.deliver` (steer and queue),
  `adapter.activity` and `adapter.queue` (task 09). Each has an
  `operationKinds` gate. An older worker shows **Update Stage on the
  worker machine**.

### D10. Docs

- This ADR is the reference from task 01. Each PR updates API, execution,
  harnesses, storage, PRODUCT and DESIGN docs where affected.
- Per task, use impeccable and plan-mode, a worktree off a fresh
  `origin/main`, execute-plan, then a PR and merge.

## Harness target matrix

| Harness | `respond` | `send` | `steer` | `queue` | `activity` | `queueManage` |
| --- | --- | --- | --- | --- | --- | --- |
| OpenCode (shipped in 04) | Permissions (once/always/reject) and forms | Idle prompt | Prompt with `steer` | Prompt with `queue` | `/message`, `/diff`, `Session.Info` | `/inbox` (task 09) |
| T3 (shipped in 05) | Questions via `t3_pending_request`. Permission approvals use **Open in T3**. | `t3_thread_send` with `queue` after the turn ends | `t3_thread_send` with `steer` | `t3_thread_send` with `queue` while a turn runs | `t3_thread_read` activity | `t3_queue` (task 09) |
| Codex | Command, file-change and permissions approvals on tracked active threads through `thread/resume` and a held subscription (task 06, Codex 0.172+) | `turn/start` | `turn/steer` | Unavailable (experimental) | `thread/items/list` | Unavailable |
| Claude Code | Existing `PermissionRequest` hooks | Existing send between turns | `PostToolUse` `additionalContext` | `Stop` block | Transcript | Unavailable |

Claude Code steer and queue apply only to sessions launched with the new
hooks. The target matrix lands by adapter task. Task #530's journal and
the task prompts hold the API evidence. The plan checked the Codex
0.172.0 schema, OpenCode v2.3.2 OpenAPI and T3 tool schemas.

## Rollout

| Task | Scope | Needs |
| --- | --- | --- |
| #531 (01) | ADR, operations, delivery and activity contracts, worker kinds, fixture | |
| #532 (02) | Session history, storage and API | #531 |
| #533 (03) | Tabs, SessionView, composer, activity and slot | #532 |
| #534 (04) | OpenCode | #533 |
| #536 (05) | T3 | #533 |
| #535 (06) | Codex approvals | #533 |
| #539 (07) | Codex send, steer and activity | #535 |
| #537 (08) | Claude Code steer, queue and activity | #533 |
| #538 (09) | Queue management | #534, #536 |
| #540 (10) | Closeout | #537, #538, #539 |

## Out of scope

- Full transcript and a Stage-held queue.
- Interaction with past sessions or several live sessions per task.
- T3 approvals and Codex experimental APIs.
- Editing or reordering queued messages (follow-up).
