package agenttools

import (
	"context"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is the MCP server implementation version.
const Version = "1.0.0"

// Guide is the agent workflow guidance: MCP server instructions, `stage
// guide`, and the block `stage setup instructions` writes.
const Guide = `stage is the task queue for T3 Code: a backlog of agent tasks, run order, schedules, and a cross-project view of T3 sessions. T3 stays where conversations happen; stage queues and starts them.

Use it to:
- Save follow-up work you notice but should not do now: create_task (lands in backlog; does not start).
- Kick off work: create_task with start=true, or start_task on a backlog task.
- Chain work: afterMe=true starts the task when your own session is settled in T3; afterTaskId / afterThreadId wait on another task or session; order_tasks links several backlog tasks in order at once. A failed predecessor keeps it waiting.
- Handoff: when a linked task starts, stage appends its predecessor's final reply (up to 4000 chars), PR links, branch/worktree and T3 thread id to the prompt, so sequential tasks carry context forward (handoff=false turns it off). Read more of the predecessor with t3_thread_read.
- Stacked work: base=predecessor (with baseRef as the fallback) starts a linked task's worktree from its predecessor's branch, so it builds on unmerged changes; it falls back to baseRef when that branch is unusable (root, other project, merged, deleted). get_task shows currentLaunchBase.
- Verify a plan you queued: get_session on your session id returns createdTasks (chains and independent tasks).
- Recurring work: create_schedule (5-field cron in an IANA zone).
- Check progress: list_tasks, get_task, wait_task, list_sessions.
- Find a past session by what was said in it: search_sessions.
- See what a session is asking its owner: get_session_questions (read-only; the owner answers in stage or T3).

Rules:
- Prompts must be self-contained: the new thread does not see your conversation (a linked task also gets the handoff).
- Project defaults to your session's project, else the project containing your cwd. Pass project (id or title) to target another one (list_projects).
- Model defaults to the user's pinned or last-used model; list_models shows choices.
- Over HTTP MCP, pass callerThreadId ($T3CODE_THREAD_ID) and cwd so afterMe, provenance and project defaults work; stdio and CLI read them from the environment.
- Settling happens in T3, not here.

CLI equivalent: ` + "`stage task add|ls|show|start|stop|followup|link|order|wait ... --json`" + ` (stage help).`

// NewServer returns an MCP server exposing every operation as a tool.
func NewServer(o *Ops) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "stage", Title: "stage", Version: Version},
		&mcp.ServerOptions{Instructions: Guide})
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true}
	destructive := true
	del := &mcp.ToolAnnotations{DestructiveHint: &destructive}

	add(s, "get_context", "Your session id, cwd, resolved stage project and owning task.", ro, o.Context)
	add(s, "list_projects", "List stage projects (mirrors T3 projects).", ro, o.ListProjects)
	add(s, "list_models", "List providers, models and model options (effort, ...) available for tasks.", ro, o.ListModels)

	add(s, "list_tasks", "List tasks (compact rows, newest activity first).", ro, o.ListTasks)
	add(s, "get_task", "Read a task with its events, thread (recent runs: ordinal, status, model, times), pull requests, run order, currentHandoff (what its launch appended from its predecessor) and currentLaunchBase (the worktree base its launch used with base=predecessor).", ro, o.GetTask)
	add(s, "create_task", "Create a task: saved to backlog by default; start=true launches it now; afterMe/afterTaskId/afterThreadId queue it to run after a session or task settles (handoff, default true, appends that predecessor's final reply, PRs, branch and thread id at launch; base=predecessor starts its worktree from that predecessor's branch, baseRef the fallback); startLocal+startTz schedule a one-shot start.", nil, o.CreateTask)
	add(s, "update_task", "Edit a task (incl. handoff on/off, base ref|predecessor). Outside backlog only title, tags and notes change.", nil, o.UpdateTask)
	add(s, "delete_task", "Delete a task (not while launching, running or needing input).", del, o.DeleteTask)
	add(s, "start_task", "Launch a task in T3 now (backlog, failed, interrupted or cancelled). Unlinks it from an unfinished predecessor.", nil, o.StartTask)
	add(s, "stop_task", "Interrupt a task's running thread.", nil, o.StopTask)
	add(s, "followup_task", "Send a message (and optionally images) to a task's thread.", nil, o.FollowupTask)
	add(s, "duplicate_task", "Copy a task into a new backlog task.", nil, o.DuplicateTask)
	add(s, "link_task", "Make a backlog task run after a task, a session, or your own session (afterMe). handoff (default true) appends the predecessor's final reply, PRs, branch and thread id at launch; base=predecessor starts its worktree from the predecessor's branch.", nil, o.LinkTask)
	add(s, "order_tasks", "Link backlog tasks to run in the given order in one step (each after the previous one settles). The first keeps its own wait; the others' start times and session waits are replaced.", nil, o.OrderTasks)
	add(s, "unlink_task", "Remove a task's run-after link.", nil, o.UnlinkTask)
	add(s, "wait_task", "Wait until a task reaches a state (default: needs input or finished) or a timeout.", ro, o.WaitTask)

	add(s, "list_schedules", "List recurring schedules (next fire first).", ro, o.ListSchedules)
	add(s, "get_schedule", "Read a schedule with its recent runs.", ro, o.GetSchedule)
	add(s, "create_schedule", "Create a recurring schedule: each cron fire spawns and starts a task with this config.", nil, o.CreateSchedule)
	add(s, "update_schedule", "Edit a schedule, or pause/resume it (paused).", nil, o.UpdateSchedule)
	add(s, "delete_schedule", "Delete a schedule (spawned tasks stay).", del, o.DeleteSchedule)
	add(s, "run_schedule", "Spawn and start a schedule's task now.", nil, o.RunSchedule)
	add(s, "preview_schedule", "Next three fire times of a cron in a zone.", ro, o.PreviewSchedule)

	add(s, "list_sessions", "List T3 sessions (root threads) across projects, newest first.", ro, o.ListSessions)
	add(s, "search_sessions", "Find T3 sessions by title, branch, PR or project, then by message text (via T3; falls back to stage titles when T3 is offline, see t3).", ro, o.SearchSessions)
	add(s, "get_session", "Read a T3 session with its pull requests, recent runs (ordinal, status, model, times), children, continuations, task, and the tasks it created (createdTasks: run-order chains head first, then independent tasks).", ro, o.GetSession)
	add(s, "get_session_questions", "Read the questions a T3 session is waiting on its owner to answer (read-only). Approvals are only counted; they are handled in T3.", ro, o.GetSessionQuestions)
	return s
}

// add registers op as a tool with an inferred input schema. Output has no
// schema (Out = any): results are the REST shapes as JSON.
func add[In, Out any](s *mcp.Server, name, desc string, ann *mcp.ToolAnnotations, op func(context.Context, In) (Out, error)) {
	mcp.AddTool(s, &mcp.Tool{Name: name, Description: desc, Annotations: ann},
		func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
			out, err := op(ctx, in)
			if err != nil {
				return nil, nil, err
			}
			return nil, out, nil
		})
}

// HTTPHandler serves streamable HTTP MCP over ops (no local file access).
func HTTPHandler(o *Ops) http.Handler {
	s := NewServer(o)
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true})
}
