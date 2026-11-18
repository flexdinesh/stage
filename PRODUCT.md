# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users
Developers running many T3 Code agent threads across several projects. They use stage mostly from a laptop and a phone on their private network, rarely a desktop: queueing work, checking what needs input, and glancing at what is running or done.

## Product Purpose
A self-hosted stage manager for T3 Code. It queues tasks (prompt + provider/model/effort + runtime/workspace), launches them as T3 threads, tracks thread status, runs task trees (a task starts when every task it needs is settled in T3), and gives a cross-project history of every T3 thread. Success: never miss a thread that needs input, keep a queue moving without babysitting, and see recent work at a glance.

## Positioning
Sits beside T3 rather than replacing it: T3 remains where conversations happen ("Open in T3"); stage owns the queue, trees, and the cross-project view T3 does not provide. The one exception is a session's pending question: stage shows it and lets the user answer it in place, so a needs-input thread can be unblocked from a phone in one step.

## Operating Context
- Self-hosted on the T3 host via docker compose, listening on 127.0.0.1:7380 by default (compose publishes it on host loopback; expose it only on a trusted private network or behind a proxy), no auth.
- Or as a per-user OS service with the Linux/macOS desktop app: the same UI in a native window over the local Unix socket; the app starts the service, stays resident when its window closes and shows native notifications and a needs-input badge while resident; Quit stops them and leaves the server running. Phones and other machines keep using the browser.
- The desktop app can also switch to saved remote Stage servers on a trusted network (no auth, plain TCP to that server's listener; a hostname needs that server's `STAGE_ALLOWED_ORIGINS`). One server at a time in the window (Settings Servers: add with Test connection, edit, remove, switch; a top-bar server menu once more than one is saved); notifications and the badge cover every saved server. Open in T3 uses native links locally and T3 web links for remote servers. Each Stage server is bound to its own T3 backend; the desktop never starts or configures a remote.
- Talks to T3 only through T3's MCP endpoint; polls active tasks every 5s, project and tracked-task refresh every 5 min, and active external discovery for notifications.
- Settling a thread happens in T3, not here; dependent tasks wait for it.

## Capabilities and Constraints
- Projects have stable Stage IDs and Stage-owned titles/archive. Create and plan offline. Explicit workspace bindings gate launch; T3 discovery never imports automatically. Bind existing workspaces or explicitly register existing directories. Changed paths require confirmation, and external removal never erases Stage history.
- Onboarding: until the first full sync the Dashboard is T3 setup (T3 reachable, pairing, first sync with live counts), one action per state; pairing kicks a full sync. After that, T3 trouble is a Dashboard Alert with the same fix, never a takeover. Each stage server is bound to one T3 backend.
- Task states: backlog, launching, running, needs input, done, settled, failed, interrupted, cancelled.
- Task actions: start, stop, duplicate, archive/unarchive, delete unstarted work without recorded history, and Open in T3. Adopted sessions are stopped or opened in T3, never started or duplicated by Stage.
- Pending questions: Needs-input tasks and notifications offer Open in T3. Questions, permission approvals and follow-ups are handled in T3.
- Screenshots: images dropped on (or picked for) the prompt are sent with it on every launch; read-only once the task leaves backlog.
- Trees: a tree is a DAG of tasks (may span projects); a task's needs are tree members that must settle first. Tree start mode auto launches a task once its needs settle; manual marks it ready (Ready group and stat cell, a `ready` notification with Start). A task may override the mode. Roots never auto-launch and read ready. Close work: Mark done (the rest will not be worked on; members keep their states, nothing more starts) or Archive a tree, Reopen/Unarchive to undo; not while a member is launching, running or needs input. Activity on a member (T3 restart, Start) reopens the tree; an archived task unarchives the same way. Done and archived trees, their tasks and archived tasks leave the Dashboard and default lists. Delete a tree (with its tasks) only when no member started and no recorded work exists. Protection survives tasks leaving. Dissolve detaches tasks and archives the tree; emptying also archives it, preserving identity and journal. Views: Trees list (Active | Done | Archived | All; last activity and created date per tree), tree view (graph list in level order with time in state per task, or a List | Graph toggle to a top-down layered graph (step bands, solid/dashed need arrows), side panel: tree Activity feed or the selected task), a Tree panel on task detail (mini levels, needs, dependents, start mode, base, handoff; Add to tree when outside one), New tree from a session batch selection (Parallel or Chain). A failed need keeps dependents blocked; Start early runs a blocked task anyway. Removing a task splices it out (its dependents take its needs). An existing T3 session can be tracked as a task (adopted: Track session on Stage or a project, or Add session in a tree) so it can be a node; stage never starts, restarts or duplicates it, and its prompt is read from T3.
- Handoff: on by default per task (form toggle; REST, CLI and MCP flag). When a task with needs launches, stage reads each finished need live from T3 and appends one block per need to the prompt: final reply (4000 chars, less when there are several needs; truncation marked), artifact links, branch/worktree, thread id and link. Composed at launch; each attempt's block is stored and shown in the task's Prompt panel (backlog shows what will be appended). A failed read never blocks the launch: it launches bare and records why.
- Predecessor base: opt-in per task (Workspace "#12's branch" cell, shown in the form for a task with needs; REST, CLI and MCP `base: predecessor`, `baseTaskId` picks the need). The task's T3 worktree starts from a need's branch (local ref, unpushed commits included), so stacked work builds on unmerged changes. Falls back to the task's own base ref, with a task event and reason, when there is no single need to pick, or the need ran at root, in another project, has no branch, has only merged PRs, or T3 rejects the branch; the launch is never blocked. Task detail shows the planned base, then the base the launch used.
- Scheduling: a backlog task can start at a time (one-shot, IANA zone, starts late after downtime; never together with needs). A schedule is a recurring template (presets or 5-field cron, own zone) that spawns and starts a new task per fire; skipped while the previous run is pending/running/needs input; fires missed while down are skipped. No new notifications.
- Sessions: Track session on Stage or a project, or Add session in a tree, accepts a pasted T3 link or thread ID. Preview title, project and state; confirm with an optional task title and initial journal note. Existing owners are reused. No global session browser.
- Journals: append-only task/tree notes and events with immutable IDs and provenance. Optional categories (finding, decision, blocker, outcome), text/task filters, cursor pagination, 600-rune previews and full-entry reads. Task notes follow current membership in tree aggregates; tree notes/events remain in their original tree. Moves log both IDs in both trees. Closed work accepts notes without reopening or scheduling. Initial notes on creation/adoption; schedules retain note templates; duplicates copy no notes. API v2 discards old task notes, preserves task event history, and starts accurate tree history at rollout. Agents use read_journal, get_journal_entry, append_note (CLI task notes / tree notes). Every launch receives persisted, retry-stable instructions to refresh membership, read context, checkpoint risky work, and record outcomes; notes are not instructions/proof, failed writes must be reported, and handoff is independent. No retroactive messages to existing/adopted sessions.
- Transcript: Conversations, detailed runs and session history stay in T3. Stage retains the adopted task opening prompt and its bounded final reply.
- Last word: Task detail retains the latest assistant reply as sanitized markdown (4000 characters, longer replies link to T3). Task rows show a one-line preview. Cached replies remain available when T3 is offline. Reply polling only runs for task-owned sessions at rest when their updated time changed.
- Pull requests and runs: Historical artifact URLs remain available as plain task links. Stage no longer tracks PR status or stores new detailed run history. Existing data is preserved. The predecessor-base launch check still consults merged PR state for its existing fallback behavior.
- Dashboard: a Trees group of tree rows (segmented state bar, next step, Start ready) rolls up tree members out of the state groups; members that need input or are ready also keep their own row with a tree chip. Settled (last 7 days): tasks and completed trees in one list, newest settle first, each with its settle time and elapsed run time.
- History: Global session/history browsing is retired. Task and tree journals retain their recorded history.
- Agent interface: CLI and MCP manage tasks, trees, journals and schedules. Explicit session adoption and read_ref remain. list_tasks with originThreadId (CLI task ls --origin-thread ID) lists an agent-created batch; get_task returns its grouped batch. Conversation actions belong in T3.
- References: Task headers copy markdown references or bare URLs. Task rows and the palette copy task links. read_ref / stage show resolve task references and explicitly named sessions. Historical session references resolve to their owning task or native T3 destination. References remain context, never dependencies.
- Snippets: reusable prompt text, global or scoped to one project, inserted by typing `/` in a prompt or notes. Settings: connection (state, T3 identity), pairing (one click through the local t3 CLI, or a code), T3 URL (env `T3_URL` wins), sync now, global notification event filter (needs input / failed / ready / done / settled; unchecked events are dropped from inbox, push and desktop notifications), per-device notifications (Web Push in the browser, a native on/off on desktop, on by default).

## Brand Commitments
- Name: stage (lowercase).
- User-pinned visual direction: inspired by zed.dev and px0.ai; sharp borders, mildly colored borders; Ink accent (black on white / near-white on near-black); pure white light mode; color only on states; Geist UI text with JetBrains Mono for metadata.

## Evidence on Hand
Real data lives in the user's T3 instance; there are no testimonials, customers, or metrics to show and none should be invented.

## Product Principles
1. Needs-input is never missable: it leads every view it appears in.
2. T3 is the place to talk; stage is the place to queue tasks, track work and retain results. Every task links straight to its thread.
3. Phone and laptop are both first-class; phone gets the glance and the quick action.
4. Dense and scannable over decorative; state carries the color.

Pre-existing external T3 sessions waiting for input appear as silent current-state rows in the inbox when they have no recorded notification group. They add no unread event, push, toast or synthetic history. A recorded group replaces the silent row.
