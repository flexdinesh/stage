package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/db"
)

// list is a repeatable string flag.
type list []string

func (l *list) String() string { return strings.Join(*l, ",") }
func (l *list) Set(v string) error {
	*l = append(*l, v)
	return nil
}

// csv splits comma-separated values.
func csv(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// configFlags are the task configuration flags (task add/edit, schedule
// add/edit).
type configFlags struct {
	connection                         int64
	workspace                          string
	executionConfig                    *agenttools.ConfigInput
	project, title, prompt, promptFile string
	images                             list
	clearConfig                        bool // --execution-config null (edit only)
}

// removedConfigFlags are the T3 model flags removed with API v8.
var removedConfigFlags = []string{"provider", "model", "effort", "option", "runtime", "mode"}

// removedWorkspaceFlags are the Stage worktree flags removed with API v8:
// a worktree, its base ref and branch are harness launch options.
var removedWorkspaceFlags = []string{"workspace", "base-ref", "branch", "base", "base-task"}

// removeWorkspaceFlags registers names as flags that fail naming the
// replacement.
func removeWorkspaceFlags(c *cmd, names ...string) {
	for _, name := range names {
		c.fs.Func(name, "removed", func(string) error {
			return fmt.Errorf("--%s was removed; set the harness's launch options (worktree, baseRef, branch where it declares them) in --execution-config; models ls --connection ID shows them", name)
		})
	}
}

func (f *configFlags) configInputFlag(c *cmd) {
	c.fs.Func("execution-config", "harness configuration as JSON (null clears it on edit)", func(raw string) error {
		if strings.TrimSpace(raw) == "null" {
			f.executionConfig, f.clearConfig = nil, true
			return nil
		}
		var cfg agenttools.ConfigInput
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&cfg); err != nil {
			return err
		}
		if !json.Valid([]byte(raw)) {
			return fmt.Errorf("expected one configuration object")
		}
		f.executionConfig, f.clearConfig = &cfg, false
		return nil
	})
}
func (f *configFlags) register(c *cmd) {
	f.configInputFlag(c)
	c.fs.Int64Var(&f.connection, "connection", 0, "destination connection ID")
	c.fs.StringVar(&f.workspace, "workspace-id", "", "exact workspace ID from projects ls (empty clears it on edit)")
	retireDestinationFlag(c, "binding")
	c.fs.StringVar(&f.project, "project", "", "project id or title")
	c.fs.StringVar(&f.title, "title", "", "title")
	c.fs.StringVar(&f.prompt, "prompt", "", "prompt text (- = stdin)")
	c.fs.StringVar(&f.promptFile, "prompt-file", "", "read the prompt from a file")
	c.fs.Var(&f.images, "image", "image file (repeatable; on edit, added to the current images)")
	removeWorkspaceFlags(c, removedWorkspaceFlags[:4]...)
	for _, name := range removedConfigFlags {
		c.fs.Func(name, "removed", func(string) error {
			return fmt.Errorf("--%s was removed; use --connection ID --execution-config JSON", name)
		})
	}
}

// readPrompt resolves --prompt (- = stdin) and --prompt-file.
func (f *configFlags) readPrompt(c *cmd) (string, bool, error) {
	switch {
	case c.set("prompt-file"):
		b, err := os.ReadFile(f.promptFile)
		return string(b), true, err
	case c.set("prompt") && f.prompt == "-":
		b, err := io.ReadAll(c.env.Stdin)
		return string(b), true, err
	case c.set("prompt"):
		return f.prompt, true, nil
	}
	return "", false, nil
}

func (f *configFlags) create(c *cmd) (agenttools.ConfigArgs, error) {
	prompt, _, err := f.readPrompt(c)
	if err != nil {
		return agenttools.ConfigArgs{}, err
	}
	if strings.TrimSpace(f.title) == "" || strings.TrimSpace(prompt) == "" {
		return agenttools.ConfigArgs{}, usagef("--title and --prompt are required")
	}
	if f.clearConfig {
		return agenttools.ConfigArgs{}, usagef("--execution-config null clears a configuration on edit; omit it here for the defaults")
	}
	if c.set("workspace-id") && f.workspace == "" {
		return agenttools.ConfigArgs{}, usagef("--workspace-id requires a workspace ID from projects ls")
	}
	return agenttools.ConfigArgs{
		ConnectionID: f.connection, WorkspaceID: strFlag(c, "workspace-id", f.workspace), ExecutionConfig: f.executionConfig, Project: f.project, Title: f.title, Prompt: prompt,
		ImagePaths: f.images,
	}, nil
}

func strFlag(c *cmd, name, v string) *string {
	if !c.set(name) {
		return nil
	}
	return &v
}

// boolFlag is v when flag name was given, else nil.
func boolFlag(c *cmd, name string, v bool) *bool {
	if !c.set(name) {
		return nil
	}
	return &v
}

func (f *configFlags) update(c *cmd, id int64) (agenttools.UpdateArgs, error) {
	prompt, promptSet, err := f.readPrompt(c)
	if err != nil {
		return agenttools.UpdateArgs{}, err
	}
	a := agenttools.UpdateArgs{
		ConnectionID: f.connection, ID: id, Project: strFlag(c, "project", f.project), Title: strFlag(c, "title", f.title),
		ImagePaths: f.images,
	}
	if f.clearConfig {
		a.ExecutionConfig = db.Optional[agenttools.ConfigInput]{Set: true, Null: true}
	} else if f.executionConfig != nil {
		a.ExecutionConfig = db.Optional[agenttools.ConfigInput]{Set: true, V: *f.executionConfig}
	}
	if c.set("workspace-id") {
		a.WorkspaceID = db.Optional[string]{Set: true, Null: f.workspace == "", V: f.workspace}
	}
	if promptSet {
		a.Prompt = &prompt
	}
	return a, nil
}

func parseID(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, usagef("invalid id %q", s)
	}
	return id, nil
}

// oneID parses exactly one positional id.
func (c *cmd) oneID() (int64, error) {
	if len(c.pos) != 1 {
		return 0, usagef("%s: want one id", c.fs.Name())
	}
	return parseID(c.pos[0])
}

// waitFlags are the tree, needs and start flags of task add.
type waitFlags struct {
	needs            string
	tree             int64
	startMode        string
	afterMe          bool
	handoff          bool
	startAt, startTz string
}

func (w *waitFlags) register(c *cmd) {
	c.fs.StringVar(&w.needs, "needs", "", "task ids this task runs after (comma-separated; joins their tree): eligible once the user completes all needs in Stage")
	c.fs.Int64Var(&w.tree, "tree", 0, "join this tree (a parallel root unless --needs)")
	c.fs.StringVar(&w.startMode, "start-mode", "", "inherit (default)|auto|manual; set shared policy on the tree, override only for a task exception")
	removeWorkspaceFlags(c, "base-task")
	c.fs.BoolVar(&w.afterMe, "after-me", false, "also need your own session (adopted as a task)")
	c.fs.BoolVar(&w.handoff, "handoff", true, "append each need's final reply, branch and session link at launch (--handoff=false to skip)")
	c.fs.StringVar(&w.startAt, "start-at", "", "one-shot start YYYY-MM-DDTHH:mm")
	c.fs.StringVar(&w.startTz, "tz", "", "IANA zone for --start-at")
}

// needIDs parses --needs.
func (w *waitFlags) needIDs() ([]int64, error) { return parseIDs(csv(w.needs)) }

// parseIDs parses task ids.
func parseIDs(ss []string) ([]int64, error) {
	var ids []int64
	for _, p := range ss {
		id, err := parseID(strings.TrimPrefix(p, "#"))
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// int64Flag is v when flag name was given, else nil.
func int64Flag(c *cmd, name string, v int64) *int64 {
	if !c.set(name) {
		return nil
	}
	return &v
}

func runTask(ctx context.Context, env Env, args []string) error {
	v, rest := verb(args, "ls")
	c := newCmd(env, "task "+v)
	o := func() *agenttools.Ops { return c.ops() }
	printTask := func(t any) error { return c.out(t, func(w io.Writer) { printAnyTask(w, t) }) }
	switch v {
	case "notes":
		return runNotes(ctx, env, "task", rest)
	case "ls", "list":
		var a agenttools.ListTasksArgs
		origin := c.sessionFlags("origin")
		var states string
		var schedule int64
		c.fs.StringVar(&a.Project, "project", "", "project")
		c.fs.Int64Var(&schedule, "schedule", 0, "only tasks this schedule spawned")
		c.fs.StringVar(&states, "state", "", "states")
		c.fs.StringVar(&a.Query, "q", "", "search")
		c.fs.StringVar(&a.From, "from", "", "created at or after (ISO date or datetime, UTC)")
		c.fs.StringVar(&a.To, "to", "", "created before (ISO date or datetime, UTC)")
		c.fs.StringVar(&a.CompletedFrom, "completed-from", "", "completed (accepted) at or after (ISO date or datetime, UTC)")
		c.fs.StringVar(&a.Closed, "closed", "", "false|true|all (default false: open tasks only)")
		c.fs.IntVar(&a.Limit, "limit", 50, "max rows")
		if err := c.parse(rest); err != nil {
			return err
		}
		var err error
		a.OriginSession, err = origin()
		if err != nil {
			return err
		}
		a.States = csv(states)
		a.ScheduleID = int64Flag(c, "schedule", schedule)
		res, err := o().ListTasks(ctx, a)
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) { printTaskRows(w, res) })
	case "show", "get":
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		res, err := o().GetTask(ctx, agenttools.IDArgs{ID: id})
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) { printTaskDetail(w, res) })
	case "add", "create", "new":
		caller := c.sessionFlags("caller")
		var f configFlags
		var wf waitFlags
		var initialNote string
		var start bool
		f.register(c)
		wf.register(c)
		c.fs.StringVar(&initialNote, "initial-note", "", "initial journal note")
		c.fs.BoolVar(&start, "start", false, "start now")
		if err := c.parse(rest); err != nil {
			return err
		}
		cfg, err := f.create(c)
		if err != nil {
			return err
		}
		needs, err := wf.needIDs()
		if err != nil {
			return err
		}
		ref, err := caller()
		if err != nil {
			return err
		}
		res, err := o().CreateTask(ctx, agenttools.CreateTaskArgs{
			CallerArgs: agenttools.CallerArgs{CallerSession: ref},
			ConfigArgs: cfg, Start: start, InitialNote: initialNote,
			WaitArgs: agenttools.WaitArgs{
				TreeID: int64Flag(c, "tree", wf.tree), Needs: needs, AfterMe: wf.afterMe,
				StartMode:  wf.startMode,
				StartLocal: wf.startAt, StartTz: wf.startTz, Handoff: boolFlag(c, "handoff", wf.handoff),
			},
		})
		if err != nil {
			return err
		}
		return printTask(res)
	case "edit", "update":
		var f configFlags
		var startAt, tz, startMode string
		var handoff bool
		f.register(c)
		c.fs.StringVar(&startAt, "start-at", "", "one-shot start (empty clears)")
		c.fs.StringVar(&tz, "tz", "", "zone for --start-at")
		c.fs.BoolVar(&handoff, "handoff", true, "append each need's final reply, branch and session link at launch (true|false)")
		c.fs.StringVar(&startMode, "start-mode", "", "auto|manual|inherit")
		removeWorkspaceFlags(c, "base-task")
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		u, err := f.update(c, id)
		if err != nil {
			return err
		}
		a := agenttools.UpdateTaskArgs{
			UpdateArgs: u, StartLocal: strFlag(c, "start-at", startAt), StartTz: strFlag(c, "tz", tz), Handoff: boolFlag(c, "handoff", handoff),
			StartMode: strFlag(c, "start-mode", startMode),
		}
		res, err := o().UpdateTask(ctx, a)
		if err != nil {
			return err
		}
		return printTask(res)
	case "needs":
		if err := c.parse(rest); err != nil {
			return err
		}
		if len(c.pos) == 0 {
			return usagef("task needs: want a task id, then the ids it needs (none clears them)")
		}
		id, err := parseID(c.pos[0])
		if err != nil {
			return err
		}
		var parts []string
		for _, p := range c.pos[1:] {
			parts = append(parts, csv(p)...)
		}
		needs, err := parseIDs(parts)
		if err != nil {
			return err
		}
		res, err := o().SetNeeds(ctx, agenttools.SetNeedsArgs{TaskID: id, Needs: needs})
		if err != nil {
			return err
		}
		return printTask(res)
	case "rm", "delete":
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		res, err := o().DeleteTask(ctx, agenttools.IDArgs{ID: id})
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) { success(w, "Deleted task %d", id) })
	case "archive":
		var undo bool
		c.fs.BoolVar(&undo, "undo", false, "unarchive")
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		archived := !undo
		res, err := o().ArchiveTask(ctx, agenttools.ArchiveTaskArgs{ID: id, Archived: &archived})
		if err != nil {
			return err
		}
		return printTask(res)
	case "start", "stop", "dup", "duplicate":
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		ops := o()
		fn := map[string]func(context.Context, agenttools.IDArgs) (any, error){
			"start":     wrap(ops.StartTask),
			"stop":      wrap(ops.StopTask),
			"dup":       wrap(ops.DuplicateTask),
			"duplicate": wrap(ops.DuplicateTask),
		}[v]
		res, err := fn(ctx, agenttools.IDArgs{ID: id})
		if err != nil {
			return err
		}
		return printTask(res)
	case "wait":
		var until string
		var timeout time.Duration
		c.fs.StringVar(&until, "until", "", "comma-separated states (default: needs_input,done,failed,interrupted,cancelled)")
		c.fs.DurationVar(&timeout, "timeout", 10*time.Minute, "timeout")
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		res, err := o().WaitTask(ctx, agenttools.WaitTaskArgs{ID: id, Until: csv(until), TimeoutSeconds: int(timeout.Seconds())})
		if err != nil {
			return err
		}
		if err := c.out(res, func(w io.Writer) {
			printAnyTask(w, res.Task)
			if res.TimedOut {
				warn(w, "Timed out")
			}
		}); err != nil {
			return err
		}
		if res.TimedOut {
			return errTimeout
		}
		return nil
	}
	return usagef("task %s: unknown", v)
}

// wrap adapts a typed op for dispatch tables.
func wrap[In, Out any](op func(context.Context, In) (Out, error)) func(context.Context, In) (any, error) {
	return func(ctx context.Context, in In) (any, error) { return op(ctx, in) }
}

func runSchedule(ctx context.Context, env Env, args []string) error {
	v, rest := verb(args, "ls")
	c := newCmd(env, "schedule "+v)
	printSchedule := func(s any) error { return c.out(s, func(w io.Writer) { printAnySchedule(w, s) }) }
	switch v {
	case "ls", "list":
		var a agenttools.ListSchedulesArgs
		c.fs.StringVar(&a.Project, "project", "", "project")
		c.fs.StringVar(&a.DueWithin, "due-within", "", "only unpaused schedules firing within this Go duration (e.g. 24h)")
		if err := c.parse(rest); err != nil {
			return err
		}
		res, err := c.ops().ListSchedules(ctx, a)
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) { printSchedules(w, res.Schedules) })
	case "show", "get":
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		res, err := c.ops().GetSchedule(ctx, agenttools.IDArgs{ID: id})
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) { printScheduleDetail(w, res) })
	case "add", "create", "new":
		caller := c.sessionFlags("caller")
		var f configFlags
		var a agenttools.CreateScheduleArgs
		f.register(c)
		c.fs.StringVar(&a.Notes, "notes", "", "note template for spawned tasks")
		c.fs.StringVar(&a.Cron, "cron", "", "cron")
		c.fs.StringVar(&a.Tz, "tz", "", "zone")
		c.fs.BoolVar(&a.Paused, "paused", false, "create paused")
		if err := c.parse(rest); err != nil {
			return err
		}
		cfg, err := f.create(c)
		if err != nil {
			return err
		}
		a.CallerSession, err = caller()
		if err != nil {
			return err
		}
		a.ConfigArgs = cfg
		res, err := c.ops().CreateSchedule(ctx, a)
		if err != nil {
			return err
		}
		return printSchedule(res)
	case "edit", "update":
		var f configFlags
		var cron, tz, notes string
		f.register(c)
		c.fs.StringVar(&notes, "notes", "", "note template for spawned tasks")
		c.fs.StringVar(&cron, "cron", "", "cron")
		c.fs.StringVar(&tz, "tz", "", "zone")
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		u, err := f.update(c, id)
		if err != nil {
			return err
		}
		res, err := c.ops().UpdateSchedule(ctx, agenttools.UpdateScheduleArgs{UpdateArgs: u, Notes: strFlag(c, "notes", notes), Cron: strFlag(c, "cron", cron), Tz: strFlag(c, "tz", tz)})
		if err != nil {
			return err
		}
		return printSchedule(res)
	case "pause", "resume":
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		paused := v == "pause"
		res, err := c.ops().UpdateSchedule(ctx, agenttools.UpdateScheduleArgs{UpdateArgs: agenttools.UpdateArgs{ID: id}, Paused: &paused})
		if err != nil {
			return err
		}
		return printSchedule(res)
	case "rm", "delete":
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		res, err := c.ops().DeleteSchedule(ctx, agenttools.IDArgs{ID: id})
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) { success(w, "Deleted schedule %d", id) })
	case "run":
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		res, err := c.ops().RunSchedule(ctx, agenttools.IDArgs{ID: id})
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) { printAnyTask(w, res) })
	case "preview":
		var a agenttools.PreviewArgs
		c.fs.StringVar(&a.Cron, "cron", "", "cron")
		c.fs.StringVar(&a.Tz, "tz", "", "zone")
		if err := c.parse(rest); err != nil {
			return err
		}
		res, err := c.ops().PreviewSchedule(ctx, a)
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) {
			for _, t := range res.Next {
				fmt.Fprintln(w, t.Format(time.RFC3339))
			}
		})
	}
	return usagef("schedule %s: unknown", v)
}

func runSessions(ctx context.Context, env Env, args []string) error {
	v, rest := verb(args, "ls")
	c := newCmd(env, "sessions "+v)
	switch v {
	case "adopt", "track":
		var a agenttools.AdoptSessionArgs
		caller := c.sessionFlags("caller")
		target := c.sessionFlags("target")
		var projectID int64
		c.fs.Int64Var(&projectID, "project", 0, "bind to this Stage project ID")
		c.fs.StringVar(&a.NewProjectTitle, "new-project", "", "bind to a new project")
		var tree int64
		c.fs.StringVar(&a.Title, "title", "", "task title (default: the session title)")
		c.fs.StringVar(&a.InitialNote, "initial-note", "", "initial journal note")
		c.fs.Int64Var(&tree, "tree", 0, "also put the task in this tree")
		if err := c.parse(rest); err != nil {
			return err
		}
		if len(c.pos) > 0 {
			return usagef("sessions adopt: unexpected argument %q (name the session with --target-connection ID --target-session ID; default: your session)", c.pos[0])
		}
		var err error
		a.CallerSession, err = caller()
		if err != nil {
			return err
		}
		a.Session, err = target()
		if err != nil {
			return err
		}
		a.ProjectID = int64Flag(c, "project", projectID)
		a.TreeID = int64Flag(c, "tree", tree)
		res, err := c.ops().AdoptSession(ctx, a)
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) {
			if res.Created {
				success(w, "Adopted session as a task")
			} else {
				success(w, "Session already tracked")
			}
			printTask(w, res.Task)
		})

	}
	return usagef("sessions %s: unknown", v)
}
