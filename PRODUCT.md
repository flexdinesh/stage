# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users
Developers running many T3 Code agent threads across several projects. They use stage mostly from a laptop and a phone on their private network, rarely a desktop: queueing work, checking what needs input, and glancing at what is running or done.

## Product Purpose
A self-hosted stage manager for T3 Code. It queues tasks (prompt + provider/model/effort + runtime/workspace), launches them as T3 threads, tracks thread status, runs linked tasks in order (a task linked to run after another starts when that thread is settled in T3), and gives a cross-project history of every T3 thread. Success: never miss a thread that needs input, keep a queue moving without babysitting, and see recent work at a glance.

## Positioning
Sits beside T3 rather than replacing it: T3 remains where conversations happen ("Open in T3"); stage owns the queue, run order, and the cross-project view T3 does not provide. The one exception is a session's pending question: stage shows it and lets the user answer it in place, so a needs-input thread can be unblocked from a phone in one step.

## Operating Context
- Self-hosted on the T3 host via docker compose, listening on 127.0.0.1:7380 by default (compose publishes it on host loopback; expose it only on a trusted private network or behind a proxy), no auth.
- Talks to T3 only through T3's MCP endpoint; polls active tasks every 5s, full sync every 5 min, watched transcripts every 2s while their run is active.
- Settling a thread happens in T3, not here; linked tasks wait for it.

## Capabilities and Constraints
- Projects mirror T3 projects 1:1; existing T3 projects import automatically.
- Task states: backlog, launching, running, needs input, done, settled, failed, interrupted, cancelled.
- Task actions: start/restart, follow-up (auto/queue/steer/restart, or with images), stop, duplicate, delete, open in T3.
- Pending questions: a session waiting on T3 user input shows a Needs input panel at the top of task and session detail (single/multi select, own answer, one Send answer per request). Rows, toasts and inbox entries get Answer; needs-input pushes deep link to the panel. Approvals cannot be answered here: the panel counts them and links to T3. Sync counts answerable questions per waiting thread; questions are read fresh when shown. A resent answer is idempotent; answered-elsewhere and stale requests say so. Agents can read questions (CLI/MCP), not answer them.
- Screenshots: images dropped on (or picked for) the prompt are sent with it on every launch; read-only once the task leaves backlog.
- Run order: a task can run after one other task (linear, may span projects), set while creating, viewing or editing a task; it starts when that task settles; a failed predecessor keeps it waiting; Start now unlinks it.
- Handoff: on by default per task (toggle beside Runs after in the form and the Run order panel; REST, CLI and MCP flag). When a linked follower (task or session link) launches after its predecessor settles, stage reads the predecessor live from T3 and appends a block to the prompt: final reply (4000 chars, truncation marked), PR links with state, branch/worktree, thread id and link. Composed at launch; each attempt's block is stored and shown in the task's Prompt panel (backlog shows what will be appended). A failed read never blocks the launch: it launches bare and records why.
- Predecessor base: opt-in per task (Workspace "#12's branch" cell, shown in the form when Runs after is set; REST, CLI and MCP `base: predecessor`). A linked follower's T3 worktree starts from its predecessor's branch (local ref, unpushed commits included), so stacked work builds on unmerged changes. Falls back to the task's own base ref, with a task event and reason, when the predecessor ran at root, in another project, has no branch, has only merged PRs, or T3 rejects the branch; the launch is never blocked. Task detail shows the planned base, then the base the launch used.
- Scheduling: a backlog task can start at a time (one-shot, IANA zone, starts late after downtime; never together with Runs after). A schedule is a recurring template (presets or 5-field cron, own zone) that spawns and starts a new task per fire; skipped while the previous run is pending/running/needs input; fires missed while down are skipped. No new notifications.
- Sessions: Active and All views across the connected T3 instance, paginated and ordered by recent activity. Active has no creation cutoff. Session detail exposes the opening prompt (the linked task's prompt and images, else the session's first own message read live from T3; first in Overview, clamped, above Last word, same as task detail), live state, workspace, related task, children, Run order, and the tasks the session created (Created tasks: run-order chains head first, then independent tasks; start a chain or task, link selected backlog tasks in order, reorder an unstarted chain, edit). Task detail shows the same batch as a compact Batch panel linking back to it. Top-level sessions can join continuation chains; children are inspectable. The Ctrl K palette (and `stage sessions search`) finds sessions by title, then by branch, PR, project or message text via T3's thread search; offline it falls back to titles.
- Transcript: session and task detail switch between Overview and a read-only Transcript (user/assistant messages and plans only, safe markdown, collapsed long messages, newest 50 with Load earlier). Live over SSE while a run is active; the server polls T3 only while a run is active and someone watches. Full detail (tools, commands, diffs) stays in T3.
- Queued continuations: save a prompt to resume existing B after A settles and B finishes with no pending input. Running either session continues unchanged; restarting B keeps the continuation waiting. Linear chains gate later steps on the delivered continuation's run and subsequent settlement. Failures/interruption/cancellation block; uncertain delivery requires review. Saved prompts append only A's reference. Existing backlog-task order remains separate and conflicting membership must be unlinked first.
- Last word: each session's latest assistant reply, read during sync only when a thread is at rest and its updatedAt changed; stored as a 4000-char excerpt (truncated replies link to T3) plus a one-line preview. Task and session detail show the excerpt as sanitized markdown; Needs input and Done rows (dashboard, project, sessions) show the preview as one clamped line. A running thread keeps its previous reply. Agents get it via get_task/get_session (excerpt) and list rows (preview). Read-only; replying stays in T3.
- Pull requests and runs: PRs T3 associates with a thread are mirrored on every sync and shown as state chips on task and session rows (Dashboard, Project, Sessions, History; two, then +n) and as a list on task and session detail linking to the PR (open sky, merged green, closed/draft gray). Task and session detail show a Runs panel (each follow-up is a new run): ordinal, status, model, start, run time, queue time; five newest, Show all for up to 50 stored. REST, `get_task`/`get_session` and CLI show expose both.
- Dashboard Settled (last 7 days): tasks and external T3 threads in one list, newest settle first, each with its settle time and elapsed run time.
- History: all mirrored threads (often hundreds, many of them subagent threads), time-window and attribute filters.
- Agent interface: agents (and scripts) manage tasks through the `stage` CLI (`--json`), a stdio MCP server (`stage mcp`) or HTTP MCP at `/mcp`, all over the REST API. Create backlog tasks, start/stop/follow up, link run order (incl. "after me": start when the calling session settles), schedules, session reads (incl. the tasks the session created, so a planning agent can verify its batch), ordering several backlog tasks in one step. The calling session and cwd pick the project; tasks and schedules record the session that created them (shown as "Created by" / "by agent").
- Snippets: reusable prompt text, global or scoped to one project, inserted by typing `/` in a prompt, follow-up or notes. Settings: connection, pairing, sync now, global notification event filter (needs input / failed / done; unchecked events are dropped from inbox and push).

## Brand Commitments
- Name: stage (lowercase).
- User-pinned visual direction: inspired by zed.dev and px0.ai; sharp borders, mildly colored borders; Ink accent (black on white / near-white on near-black); pure white light mode; color only on states; Geist UI text with JetBrains Mono for metadata.

## Evidence on Hand
Real data lives in the user's T3 instance; there are no testimonials, customers, or metrics to show and none should be invented.

## Product Principles
1. Needs-input is never missable: it leads every view it appears in.
2. T3 is the place to talk; stage is the place to queue, watch and answer what blocks a thread. Every task links straight to its thread.
3. Phone and laptop are both first-class; phone gets the glance and the quick action.
4. Dense and scannable over decorative; state carries the color.
