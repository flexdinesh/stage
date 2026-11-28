# ADR 0014: Session surface

**Status: Implemented.** Accepted on 2026-11-28. Planned in Stage task
#530 (tree #215, journal entry #5766). The contract shipped in task #531
(01) and the integrated review is in #540 (10). The plan calls this ADR
0012. [Harness workspace add](0012-harness-workspace-add.md) and
[tree steering](0013-tree-steering.md) already occupy 0012 and 0013.

The bounded activity view and per-task attempt switcher are described in
[PRODUCT](../../PRODUCT.md#capabilities-and-constraints) and
[DESIGN](../../DESIGN.md#sessions). Full transcripts and global session
browsing stay in the harness.

Task 06 revises [ADR 0006 decision 7](0006-codex-app-server-harness.md#decisions)
for Codex approvals. The base already answers command and file-change
approvals and `requestUserInput` questions through `thread/resume`.
Stage holds the subscription and also answers permission approvals.
Experimental questions use the Codex client.
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
  is delivered on the next `PostToolUse` or `PostToolBatch`
  (`additionalContext`) or eligible `Stop` (decision block). Codex steer
  uses stable `turn/steer`.
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
- When `queueManage` is available, a non-empty native queue appears under
  the current composer. Pending messages can be cancelled or steered.
  The view refreshes on the activity cadence and after a queue action.

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
| OpenCode (shipped in 04) | Permissions (once/always/reject) and forms | Idle prompt | Prompt with `steer` | Prompt with `queue` | `/message`, `/diff`, `Session.Info` | Native `/inbox` list, delete and delivery update (shipped in 09) |
| T3 (shipped in 05) | Questions via `t3_pending_request`. Permission approvals use **Open in T3**. | `t3_thread_send` with `queue` after the turn ends | `t3_thread_send` with `steer` | `t3_thread_send` with `queue` while a turn runs | `t3_thread_read` activity | `t3_queue` list, cancel and promote (shipped in 09) |
| Codex (shipped in 06 and 07) | Command, file-change and permissions approvals on tracked active threads through `thread/resume` and a held subscription (0.172+) | `turn/start` after a finished turn | `turn/steer` with `expectedTurnId` | Unavailable (experimental) | `thread/items/list` with thread metadata | Codex queues messages only in its own client. |
| Claude Code | `PermissionRequest` hooks | Send between turns | `PostToolUse` / `PostToolBatch` context, with `Stop` fallback | `Stop` block | Owning-machine transcript | Claude Code has no message queue Stage can read. |

Claude Code steer and queue apply only to sessions launched with the new
hooks. Pending delivery is memory-only in the hook receiver and is lost
on Stage restart. Task #530's journal and
the task prompts hold the API evidence. The plan checked the Codex
0.172.0 schema, OpenCode v2.3.2 OpenAPI and T3 tool schemas.

## P2 review checklist

The integrated baseline was `0e1d80afa8cf764068413b4a5f073ee2bbc36e17`
on freshly fetched `origin/main`. The embedded build served the browser,
HTTP and MCP from a branch database at
`.scratch/dev/session-surface-closeout/stage.db`. Interactive runs used
new sessions in a scratch folder. T3 used a separate data directory and
Stage pairing. OpenCode used a scripted local model. A foreground Stage
worker joined the scratch server and hosted the OpenCode connection for
delivery, queue and history checks.

| Feature | Shared path | OpenCode | T3 | Codex | Claude Code |
| --- | --- | --- | --- | --- | --- |
| Requests and respond | Requests and needs-input rows · `/requests` · CLI `task requests`, `task respond` · MCP `list_requests`, `respond_request` | Task 20 native form answered in the browser. `TestLiveOpenCodeInteractive` checks permissions and forms | Task 3 question listed and answered via CLI. Task 15 displayed the native approval fallback | Task 13 command approval listed and answered via stdio MCP. `TestContractCodexAnswersTrackedApprovals` covers file and permission decisions and competing answers | Task 5 question answered in the browser. `TestLiveClaude` verifies permission hooks |
| Idle Send | Composer · `POST /messages` · `task send` · `send_message` | Task 7 stdio MCP Send. `TestLiveOpenCodeInteractive/idle_send_and_recent_activity` | Task 2 CLI and stdio MCP Send | Task 1 CLI and stdio MCP Send. `TestLiveAdapterRoundTrip` checks idle delivery and retry | Task 4 CLI and stdio MCP Send. `TestLiveClaude` checks the same session wakes |
| Active Steer | Composer · `delivery: steer` · `task send --steer` · `send_message` | Task 7 on the worker and `TestLiveOpenCodeInteractive/steer_the_current_turn` | Task 8 CLI steer | Task 6 CLI steer. `TestLiveAdapterRoundTrip` verifies the same active turn | Task 9 CLI accepted delivery. `TestLiveClaude` verifies both tool-hook boundaries |
| Active Queue | Composer · `delivery: queue` · `task send --queue` · `send_message` | Task 7 on the worker and `TestLiveOpenCodeInteractive/queue_while_busy` | Task 8 native admission, confirmed by list | Task 6 returned 409. Codex queues messages only in its own client | Task 9 CLI accepted delivery. `TestLiveClaude` verifies delivery at Stop |
| Native queue list, cancel and promote | Queue under composer · `/queue` · `task queue` · `queued_messages` | Task 7 listed two, cancelled one and promoted one through the worker. `TestLiveOpenCodeQueueManagement` | Task 8 listed two, cancelled one and promoted one through native MCP. T3 omitted time and source | Task 1 stdio MCP and task 6 CLI returned the Codex queue reason | Task 4 stdio MCP and task 9 CLI returned “Claude Code has no message queue Stage can read.” |
| Bounded activity | Recent activity and optional session slot · `/activity` · `task activity` · `get_session_activity` | Task 7 and native diff check in `TestLiveOpenCodeInteractive` | Task 2 and `TestActivityReadsTailOnForwardOnlyT3` | Task 1 and `TestLiveAdapterRoundTrip` | Task 4 and `TestLiveClaude` |
| Past attempts | Attempt switcher · `/session/:attempt` · `/sessions/:attempt` and activity by attempt | Task 10 distinct refs, snapshot and CLI/MCP activity | Task 12 distinct refs, snapshot and CLI/MCP activity | Task 11 distinct refs, snapshot and CLI/MCP activity | Task 14 distinct refs and snapshot. CLI/MCP activity returned an empty feed for the interrupted first turn |
| Declared reasons | Connection Capabilities, per-action composer reasons, HTTP 409 propagated by CLI and MCP | `TestQueueManagementDegradesWithoutBreakingLegacyInboxRecovery` checks a service missing inbox management | Task 15 browser fallback. `TestFeaturesGateSessionSurface` checks missing tools | Queue and queue management reasons above. Questions and MCP elicitations use Copy resume | Queue management reason above. `TestOlderClaudeSessionCannotTakeDelivery` checks pre-hook launch guidance |

### Evidence

- Scratch task IDs in the table refer to the branch database. Browser
  captures use `agent-browser set viewport`, `snapshot` and `screenshot`
  at 390 and 1280 px. On the verification machine, see
  `/var/tmp/stage-session-closeout-evidence/<stem>-<width>.png`.
  The stems `opencode-running`, `t3-running`, `codex-running` and
  `claude-running` show active controls. OpenCode and T3 queued and
  promoted a message in the browser. The `opencode-form`,
  `codex-approval`, `t3-request`, `t3-approval` and `claude-request`
  stems show pending requests and native fallback. The `opencode-past`, `t3-past`,
  `codex-past` and `claude-past` stems show read-only attempt routes.
- Live adapter commands were `mise run codex:live`, `mise run claude:live`
  with `STAGE_TEST_CLAUDE`, and `go test -count=1 -v -run
  'TestLiveOpenCode(Interactive|QueueManagement)$' ./internal/opencode`
  inside `server/` with `STAGE_TEST_OPENCODE`. The first Claude live
  run missed its active-turn window. The retry passed.
- The branch server used `bin/stage serve --local-harnesses` with an
  explicit dev data dir. The worker used `bin/stage worker run --listen
  unix` with isolated config and runtime dirs. `stage machine ping 2`
  confirmed its channel. The CLI used `task requests`, `task respond
  --answers --expect-revision`, `task send --steer`, `task send --queue`,
  `task queue --cancel`, `task queue --steer` and `task activity --attempt
  1 --limit 3`. A stdio client sent `tools/call` to `bin/stage mcp` for
  the methods in the table.
- The [engine tests](../../server/internal/engine/) ran
  `TestT3SessionSurface`, `TestContractOpenCodeSessionSurface`,
  `TestContractCodexAnswersTrackedApprovals`,
  `TestContractCodexDeliveryAndActivity`, `TestContractClaudeHookDelivery`,
  `TestContractClaudeActivity` and `TestContractQueueManagement` in local
  and worker placements. `TestContractSessionSurfaceWithOlderWorker`
  checks update guidance. `TestTaskSessionHistoryAfterTwoRestarts` checks
  three distinct refs, cached snapshot reads and historical ref resolution.
  `TestGuardedRespond`, `TestRespondRefusesOwnSession`,
  `TestTaskSessionSurfaceCommands`, `TestTaskQueueCommand`,
  `TestSessionSurfaceTools` and `TestQueuedMessagesTool` cover the shared
  route and client contracts.

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

## Follow-ups

- **OpenCode SSE inbox and usage (#559).** Task #534 retained polling for
  activity, usage and queue reconciliation. Verify native inbox and usage
  events before using them to refresh those reads.
- **T3 WebSocket approvals (#560).** Task #536 left permission approvals
  in T3. Verify the stable endpoint, paired-client authentication and
  scopes, pending request correlation and competing responses before
  adding a Stage control.
- **Queue edit and reorder (#561).** Task #538 shipped list, cancel and
  promotion. Verify native edit and reorder APIs before adding either.
  OpenCode can acknowledge a cancellation after another client consumed
  the item. T3's queue response omits creation time and source.
- **Codex experimental queue (#562).** Revisit queue and queue management
  once Codex exposes stable operations outside its own client. Task #539
  also recorded that `turn/start` has no atomic idle precondition and
  native repeated client IDs can start another turn. Stage rechecks idle
  status and reconciles same-ID retries against the latest finished
  turn. Revisit those safeguards if the native guarantees change.

## Out of scope

- Full transcript and a Stage-held queue.
- Interaction with past sessions or several live sessions per task.
- T3 approvals and Codex experimental APIs.
- Editing or reordering queued messages (follow-up).
