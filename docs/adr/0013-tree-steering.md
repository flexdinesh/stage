# ADR 0013: Tree steering

Accepted on 2026-11-28. Planned in Stage task #479 (tree #216); later
tree tasks ship it. Tree #216's prompts call this "ADR 0012"; that number
went to [harness workspace add](0012-harness-workspace-add.md) first, so
this is 0013.

This ADR supersedes the "only the user sends" sentence of
[ADR 0005 decision 18](0005-claude-code-harness.md) for sends Stage makes
to a steerer ([ADR 0011](0011-agent-operation-parity.md) already lets
agents send). It respects [ADR 0007](0007-harness-boundary.md), applies
[ADR 0008 P2](0008-harness-foundation.md#p2) and builds on ADR 0011.

## Context

A tree runs on its own until a member asks a question, finishes or fails.
Then it waits for the user, who answers, accepts and decides what runs
next, task by task. ADR 0011 gave agents the operations to do that work
(accept, answer, send), but nothing tells an agent when a tree needs it,
and the user still gets every notification first.

Steering gives a tree one agent session that hears about the tree's
members and acts on them in the user's place, with the user still able to
answer first and to take over at any time.

## Decision

### The steerer

- A steerer is an ordinary task bound to one tree. It is not a member of
  the DAG: binding a member is 400 `steerer_not_member`, and it never
  blocks or releases dependents.
- One active steerer per tree. A task steers at most one tree.
- Replace ends the old binding, stops the old session when the harness
  offers stop, then launches the new task.
- Stop steering ends the binding and stops the session.
- The steerer accepts member tasks on its own (`get_completion_preview`
  then `complete_task`), under ADR 0011's rules. The self-action guard is
  unchanged: it never acts on its own task.
- To escalate, the steerer ends its turn with a question (its task goes
  `needs_input` and notifies as usual), or calls `escalate_to_user
  {taskId, reason}` for a held member.

### Availability by capability

Per [P2](0008-harness-foundation.md#p2), a connection can host a steerer
only when its `send` operation is available. Otherwise the choice is
disabled with the harness's reason.

Workers: the steerer answers a member through `respond` or `send` where
the member's harness offers them. Where it offers neither, the member's
wait escalates to the user at once (no hold). Remote and worker forwarding
of `Interactive` is already generic.

### Send semantics

Every harness has the same send contract: a message is delivered when the
session's current turn ends (queued), or refused with `execution.Busy`
(409 `session_busy`). Stage never interrupts or steers a turn mid-way.

### Per-harness interaction

| Harness | Send | Respond |
| --- | --- | --- |
| Claude Code | Done: idle-only resume (ADR 0005 decision 18). | Done: hooks. |
| T3 | Send mode `queue` with `clientRequestId` (today `auto`, which steers a running turn). | Questions through `t3_pending_request`. Approvals stay unavailable: T3's MCP cannot list or answer them. |
| OpenCode | Send waits for idle. [ADR 0014](0014-session-surface.md#d4-operations) | Permission reply (once, always, reject) and form reply. Forms with external or conditional fields stay in OpenCode. |
| Codex | When idle: `thread/resume` then `turn/start`. Busy while a turn is active; no `turn/steer`. | `thread/resume` replays pending server requests: `commandExecution` and `fileChange` approvals, `requestUserInput`. |

Anything not verified live stays unavailable with a reason. Codex
verifies its live gates before it declares either operation.

### First answer wins

The user and the steerer can both answer a member. The first answer wins.

- `tasks.state_revision` is bumped on every real state transition;
  `tasks.answered_revision` records the revision last answered.
- `send` and `respond` take an optional `expectedStateRevision`. With it,
  the coordinator takes a per-task lock and requires `needs_input`, a
  matching revision and no answer at that revision, else 409
  `already_answered {state, stateRevision}`. Success sets
  `answered_revision`.
- The app's Reply and Requests and every steerer always pass it.
- The API resolves the caller session once into the request context;
  `send` and `respond` record it as `Cause.Session`.
- Answers typed directly inside a harness cannot be prevented. Stage sees
  them only as the member's next state.

### Steering events

`steering_events` is a durable per-tree outbox with a monotonic id. Each
event is enqueued in the same transaction as the write that caused it, and
only while the tree has an active steerer.

Kinds: `member_needs_input` (with `stateRevision`), `member_done` (reply
excerpt), `member_failed`, `member_interrupted`, `member_cancelled`,
`member_ready`, `member_added`, `member_removed`, `member_accepted`,
`member_reopened`, `member_answered`, `note` (a member's blocker or
decision), `tree_status`, `hold_escalated`, `steering_started`,
`steering_ended`, `tree_finished`.

Each event records its `caused_by` caller session and task. Delivery
skips the steerer's own actions. The feed (`get_steering_events
after=N`) returns every event, each with a `self` flag.

### Delivery

- A dispatcher in the `schedule.Runner` pattern coalesces events into
  digests: 10 s debounce, 60 s maximum wait, one delivery in flight per
  tree.
- A digest is capped at 32 KiB; overflow ends with a pointer to the feed.
  It is wrapped in `<stage-steering tree= through=N>`.
- At least once: the send's request ID is `steering-<binding>-<through>`,
  so a retry is idempotent.
- Delivered while the steerer is running, done, or waiting on the user
  (`needs_input`); the digest then reminds it of its open question. Not
  delivered while it is failed, interrupted or cancelled.
- Busy retries on the next kick or tick; other errors back off up to 5 min.

### Notification hold

- A member's `needs_input` push, toast and inbox row is held for the
  tree's `steeringHoldSeconds` (default 300, 0 off, max 3600) while a live
  steerer (running, done or `needs_input`) can answer it, that is, the
  member's harness offers `respond` or `send`.
- Holds live in `notification_holds`, durable across restart. The
  notification row is inserted only at release, because the SSE cursor is
  id-ordered.
- Release on expiry if the member is still unanswered at the same
  revision; cancel when it is answered.
- `escalate_to_user` releases at once; only the tree's active steerer may
  call it.
- Ending, replacing or losing the steerer releases all its holds.
- Task JSON carries `hold {until, steererTaskId}`; the UI shows a chip.

### Launch brief and playbook

- Stage composes a `<stage-steering-brief>`: tree snapshot, the last 20
  tree notes, the steering rules and the playbook, capped at 24 KiB.
- The brief is frozen per attempt in `steering_launch_context` and shown
  on task detail as `currentSteering`.
- The playbook is the global snippet `steering-playbook`, created lazily
  from an embedded default. A user edit or override wins. It carries
  merging, review and follow-up guidance; Stage enforces none of it.

### Tree finish

- When every member is completed, or the tree is marked done or archived,
  Stage sends a final digest (write an outcome note, then end) and ends
  the binding.
- When the steerer's turn then ends (done), Stage auto-completes the
  steerer task: system actor, provenance shown, once per run. Never for a
  failed or interrupted run. Reopen undoes it.

### Surfaces

| Operation | HTTP | MCP | CLI |
| --- | --- | --- | --- |
| Set or replace | `POST /api/trees/{id}/steerer {prompt, title?, connectionId, workspaceId, executionConfig?, replace?, requestId}` | `set_tree_steerer` | `stage tree steer` |
| Stop | `DELETE /api/trees/{id}/steerer` | `end_tree_steering` | `stage tree unsteer` |
| Feed | `GET /api/trees/{id}/steering/events?after&limit` | `get_steering_events` | `stage tree steering-events` |
| Escalate | `POST /api/tasks/{id}/escalate {reason}` | `escalate_to_user` | `stage task escalate` |
| Hold | `PATCH /api/trees/{id}` `steeringHoldSeconds` | `update_tree` | `stage tree edit --hold` |
| Guarded answer | `expectedStateRevision` on send and respond | `send_message`, `respond_request` | `stage task send\|respond --expect-revision` |

Tree JSON gains `steerer {taskId, title, state, since, deliveredThrough,
lastDeliveredAt, pending, lastError} | null` and `steeringHoldSeconds`.
Task JSON gains `steering {treeId, active, endedAt?, endReason?}`, `hold`
and `stateRevision`. `get_context` gains `steeringTreeId`.

### Storage

Bespoke tables, not the [ADR 0004](0004-queue-port.md) jobs queue:
`tree_steerers`, `steering_events`, `steering_launch_context`,
`notification_holds`, plus columns on `tasks` (`state_revision`,
`answered_revision`) and `task_trees` (`steering_hold_seconds`). Each
migration has one version on both engines, expand only. Moving delivery
onto the jobs queue is a possible later migration.

### Relationships

- [ADR 0005 decision 18](0005-claude-code-harness.md): "only the user
  sends" no longer holds for Stage-originated steering sends.
- [ADR 0007](0007-harness-boundary.md): no git or merge logic in core.
  Merge order, review and follow-ups are the steerer's decisions, carried
  by the playbook.
- [ADR 0011](0011-agent-operation-parity.md): the steerer accepts and
  answers with the same operations and attribution as any agent.

## Out of scope

- Interrupting or steering a running turn, on any harness.
- More than one steerer per tree, or one task steering several trees.
- Stage-enforced merge order, review gates or git operations.
- Preventing answers typed directly inside a harness.
- Approvals through T3's MCP, and OpenCode forms with external or
  conditional fields.
- The ADR 0004 jobs queue as the delivery mechanism.

## Consequences and risks

- A steerer can accept members and release dependents with no person in
  the loop, as ADR 0011 already allows. Reopen, the journal and visible
  provenance keep it reversible.
- The hold delays the user's notification by up to `steeringHoldSeconds`
  when the steerer is slow or wrong. Setting 0 turns it off; escalation
  and steerer loss release at once.
- First answer wins only covers answers through Stage. An answer typed in
  the harness races the steerer; the loser's send queues behind it or is
  Busy.
- At-least-once delivery means a steerer can see a digest twice. The
  `through=N` tag lets it skip what it has seen.
- Follow-ups wait for the current turn to end. OpenCode also offers
  explicit Steer and Queue under [ADR 0014](0014-session-surface.md#d4-operations).
- A Stage-completed steerer has a system actor. Reopen undoes it, and it
  never fires for failed or interrupted runs.
- Four more tables and four more MCP tools. The outbox only grows while a
  steerer is active.
