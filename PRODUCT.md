# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users
Developers running T3 Code and OpenCode sessions across several projects. They use stage mostly from a laptop and a phone on their private network, rarely a desktop: queueing work, checking what needs input, and glancing at what is running or done.

## Product Purpose
A self-hosted task manager for T3 Code and OpenCode V2. Users plan tasks, review final replies, complete work and start ready dependents across projects.

## Positioning
Stage owns projects, tasks, trees, journals, acceptance and durable handoff. T3 and OpenCode own conversations, questions, approvals and execution.

## Operating Context
- Self-hosted on the T3 host via docker compose, listening on 127.0.0.1:7380 by default (compose publishes it on host loopback; expose it only on a trusted private network or behind a proxy), no auth.
- Or as a per-user OS service with the Linux/macOS desktop app: the same UI in a native window over the local Unix socket; the app starts the service, stays resident when its window closes and shows native notifications and a needs-input badge while resident; Quit stops them and leaves the server running. Phones and other machines keep using the browser.
- The desktop app can also switch to saved remote Stage servers on a trusted network (no auth, plain TCP to that server's listener; a hostname needs that server's `STAGE_ALLOWED_ORIGINS`). One server at a time in the window (Settings Servers: add with Test connection, edit, remove, switch; a top-bar server menu once more than one is saved); notifications and the badge cover every saved server. Open in T3 uses native links locally and T3 web links for remote servers. Each Stage server owns its harness connections; the desktop never starts or configures a remote.
- One existing T3 MCP service and one existing OpenCode V2 service may be used independently or together. Each instance (a T3 environment, an OpenCode service) is its own connection; one per harness is active. Replacing an instance keeps the old connection's tasks and bindings as read-only history and binds nothing on the new one until the user rebinds; connecting the old instance again restores them.
- Only acceptance in Stage releases dependent tasks: user completion, or a per-connection auto-complete (on by default) when a successful run's T3 thread is settled or OpenCode session is archived. New trees default to auto start; tasks inherit.

## Capabilities and Constraints
- Projects have stable Stage IDs and Stage-owned titles/archive. Create and plan offline. Workspace bindings gate launch. Discovery imports deliberate harness projects (live T3 projects; OpenCode Git/hg checkouts it has not archived): a workspace path names one Stage project across harnesses, so a matching path binds to the existing project and a new path creates one. Plain directories stay manual-bind only. Unbinding declines a workspace for good; a user archive or unarchive sticks; a discovered project with no tasks follows its harness archive. Bind existing workspaces or explicitly register existing directories. Repository remotes are search metadata only. Changed paths require confirmation, and external removal never erases Stage history.
- A compact Dashboard prompt links to Settings. Each connection has setup and diagnostics. A user can plan work before connecting either service.
- Task states: backlog, launching, running, needs input, done, settled, failed, interrupted, cancelled.
- Task actions: start, stop, duplicate, archive/unarchive, delete unstarted work without recorded history, and opening the selected harness. Adopted sessions are stopped or opened there, never started or duplicated by Stage.
- Pending questions: Needs-input tasks and notifications open the source harness for questions, permission approvals and follow-ups.
- T3 screenshots: images dropped on (or picked for) the prompt are sent with it on every launch; read-only once the task leaves backlog.
- Trees: a tree is a DAG of tasks (may span projects); a task's needs are tree members that require user completion first. Tree start mode auto launches a task once the user completes its needs; manual marks it ready (Ready group and stat cell, a `ready` notification with Start). A task may override the mode. Roots never auto-launch and read ready. Close work: Mark done (the rest will not be worked on; members keep their states, nothing more starts) or Archive a tree, Reopen/Unarchive to undo; not while a member is launching, running or needs input. Closed trees require explicit reopening. Done and archived trees, their tasks and archived tasks leave the Dashboard and default lists. Delete a tree (with its tasks) only when no member started and no recorded work exists. Protection survives tasks leaving. Dissolve detaches tasks and archives the tree; emptying also archives it, preserving identity and journal. Views: Trees list (Active | Done | Archived | All; last activity and created date per tree), tree view (graph list in level order with time in state per task, or a List | Graph toggle to a top-down layered graph (step bands, solid/dashed need arrows), side panel: tree Activity feed or the selected task), a Tree panel on task detail (mini levels, needs, dependents, start mode, base, handoff; Add to tree when outside one), New tree from a session batch selection (Parallel or Chain). Dependents remain blocked until their needs are accepted. Removing a task splices it out (its dependents take its needs). An existing harness session can be tracked as a task (adopted: Track session on Stage or a project, or Add session in a tree) so it can be a node; stage never starts, restarts or duplicates it, and its prompt is read from its harness.
- Handoff uses a Stage snapshot of each accepted result. The successor prompt includes the bounded final reply, task and journal references, and available artifact links. The source harness can be offline.
- A T3 worktree can use an eligible predecessor branch on the same harness. An unavailable base requires an explicit workspace choice. OpenCode supports its project root and a harness-managed default-base worktree.
- Scheduling: a backlog task can start at a time (one-shot, IANA zone, starts late after downtime; never together with needs). A schedule is a recurring template (presets or 5-field cron, own zone) that spawns and starts a new task per fire; skipped while the previous run is pending/running/needs input; fires missed while down are skipped. No new notifications.
- Sessions: Track session accepts a link or ID on the selected harness. Preview title, project and state; confirm with an optional task title and initial journal note. Existing owners are reused. No global session browser.
- Journals: append-only task/tree notes and events with immutable IDs and provenance. Optional categories (finding, decision, blocker, outcome), text/task filters, cursor pagination, 600-rune previews and full-entry reads. Task notes follow current membership in tree aggregates; tree notes/events remain in their original tree. Moves log both IDs in both trees. Closed work accepts notes without reopening or scheduling. Initial notes on creation/adoption; schedules retain note templates; duplicates copy no notes. API v2 discards old task notes, preserves task event history, and starts accurate tree history at rollout. Agents use read_journal, get_journal_entry, append_note (CLI task notes / tree notes). Every launch receives persisted, retry-stable instructions to refresh membership, read context, checkpoint risky work, and record outcomes; notes are not instructions/proof, failed writes must be reported, and handoff is independent. No retroactive messages to existing/adopted sessions.
- Transcript: Conversations, detailed runs and session history stay in the source harness. Stage retains the adopted task opening prompt and its bounded final reply.
- Last word: Task detail retains the latest assistant reply as sanitized markdown (4000 characters, longer replies link to the harness). Task rows show a one-line preview. Cached replies remain available when the harness is offline. Reply polling only runs for task-owned sessions at rest when their updated time changed.
- Pull requests and runs: Historical artifact URLs remain available as plain task links. Stage no longer tracks PR status or stores new detailed run history. Existing data is preserved. A missing predecessor base requires an explicit workspace choice.
- Dashboard: a Trees group of tree rows (segmented state bar, next step, Start ready) rolls up tree members out of the state groups; members that need input or are ready also keep their own row with a tree chip. Completed groups recently accepted work and completed trees.
- History: Global session/history browsing is retired. Task and tree journals retain their recorded history.
- Agent interface: CLI and MCP manage tasks, trees, journals and schedules. Explicit session adoption and read_ref remain. list_tasks with originThreadId (CLI task ls --origin-thread ID) lists an agent-created batch; get_task returns its grouped batch. Conversation actions belong in the source harness.
- References: Task headers copy markdown references or bare URLs. Task rows and the palette copy task links. read_ref / stage show resolve task references and explicitly named sessions. Historical session references resolve to their owning task or native harness destination. References remain context, never dependencies.
- Snippets: reusable prompt text, global or scoped to one project, inserted by typing `/` in a prompt or notes. Settings: connection (state, T3 identity, Disconnect: forgets the token; pairing the same T3 resumes, pairing another replaces the connection and keeps its tasks read-only), previous connections (replaced instances with their task counts; Forget lets one go and keeps its history), pairing (one click through the local t3 CLI, or a code), T3 URL (env `T3_URL` wins), OpenCode connection (one-click Connect to the OpenCode service on this machine with its own password, a pairing link from `opencode pair` whose session renews automatically, or a plain address with a password when asked; Disconnect forgets it; tasks and history stay), sync now, global notification event filter (needs input / failed / ready / done / settled; unchecked events are dropped from inbox, push and desktop notifications), per-device notifications (Web Push in the browser, a native on/off on desktop, on by default).

## Brand Commitments
- Name: stage (lowercase).
- User-pinned visual direction: inspired by zed.dev and px0.ai; sharp borders, mildly colored borders; Ink accent (black on white / near-white on near-black); pure white light mode; color only on states; Geist UI text with JetBrains Mono for metadata.

## Evidence on Hand
Real data lives in the user's T3 instance; there are no testimonials, customers, or metrics to show and none should be invented.

## Product Principles
1. Needs-input is never missable: it leads every view it appears in.
2. Conversations and approvals stay in the selected harness. Stage retains accepted results and journals. Every task links straight to its thread.
3. Phone and laptop are both first-class; phone gets the glance and the quick action.
4. Dense and scannable over decorative; state carries the color.

Pre-existing external T3 sessions waiting for input appear as silent current-state rows in the inbox when they have no recorded notification group. They add no unread event, push, toast or synthetic history. A recorded group replaces the silent row.
