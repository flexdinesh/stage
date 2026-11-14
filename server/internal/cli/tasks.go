package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/agenttools"
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
	project, title, prompt, promptFile, provider, model, effort string
	runtime, mode, baseRef, branch, base, notes                 string
	options, tags, images                                       list
}

func (f *configFlags) register(c *cmd) {
	c.fs.StringVar(&f.project, "project", "", "project id or title")
	c.fs.StringVar(&f.title, "title", "", "title")
	c.fs.StringVar(&f.prompt, "prompt", "", "prompt text (- = stdin)")
	c.fs.StringVar(&f.promptFile, "prompt-file", "", "read the prompt from a file")
	c.fs.StringVar(&f.provider, "provider", "", "providerInstanceId")
	c.fs.StringVar(&f.model, "model", "", "model id")
	c.fs.StringVar(&f.effort, "effort", "", "effort option")
	c.fs.StringVar(&f.runtime, "runtime", "", "runtime mode")
	c.fs.StringVar(&f.mode, "mode", "", "interaction mode: default|plan")
	c.fs.StringVar(&f.baseRef, "base-ref", "", "worktree base ref")
	c.fs.StringVar(&f.branch, "branch", "", "worktree branch")
	c.fs.StringVar(&f.base, "base", "", "worktree base: ref (from --base-ref) | predecessor (from the linked predecessor's branch; --base-ref is the fallback)")
	c.fs.StringVar(&f.notes, "notes", "", "notes")
	c.fs.Var(&f.options, "option", "model option K=V (repeatable)")
	c.fs.Var(&f.tags, "tag", "tag (repeatable)")
	c.fs.Var(&f.images, "image", "image file (repeatable)")
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

func (f *configFlags) optionMap() (map[string]string, error) {
	if len(f.options) == 0 {
		return nil, nil
	}
	m := map[string]string{}
	for _, o := range f.options {
		k, v, ok := strings.Cut(o, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, usagef("--option %q: want KEY=VALUE", o)
		}
		m[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return m, nil
}

func (f *configFlags) create(c *cmd) (agenttools.ConfigArgs, error) {
	prompt, _, err := f.readPrompt(c)
	if err != nil {
		return agenttools.ConfigArgs{}, err
	}
	if strings.TrimSpace(f.title) == "" || strings.TrimSpace(prompt) == "" {
		return agenttools.ConfigArgs{}, usagef("--title and --prompt are required")
	}
	opts, err := f.optionMap()
	if err != nil {
		return agenttools.ConfigArgs{}, err
	}
	return agenttools.ConfigArgs{
		Project: f.project, Title: f.title, Prompt: prompt, Provider: f.provider, Model: f.model,
		Effort: f.effort, Options: opts, RuntimeMode: f.runtime, InteractionMode: f.mode,
		BaseRef: f.baseRef, Branch: f.branch, Base: f.base, Tags: f.tags, Notes: f.notes, ImagePaths: f.images,
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
	if len(f.images) > 0 {
		return agenttools.UpdateArgs{}, usagef("--image is not supported on edit")
	}
	prompt, promptSet, err := f.readPrompt(c)
	if err != nil {
		return agenttools.UpdateArgs{}, err
	}
	opts, err := f.optionMap()
	if err != nil {
		return agenttools.UpdateArgs{}, err
	}
	a := agenttools.UpdateArgs{
		ID: id, Project: strFlag(c, "project", f.project), Title: strFlag(c, "title", f.title),
		Provider: strFlag(c, "provider", f.provider), Model: strFlag(c, "model", f.model),
		Effort: strFlag(c, "effort", f.effort), Options: opts, RuntimeMode: strFlag(c, "runtime", f.runtime),
		InteractionMode: strFlag(c, "mode", f.mode), BaseRef: strFlag(c, "base-ref", f.baseRef),
		Branch: strFlag(c, "branch", f.branch), Base: strFlag(c, "base", f.base), Notes: strFlag(c, "notes", f.notes),
	}
	if promptSet {
		a.Prompt = &prompt
	}
	if c.set("tag") {
		a.Tags = f.tags
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

// waitFlags are the run-order flags of task add / link.
type waitFlags struct {
	afterTask        int64
	afterThread      string
	afterMe          bool
	handoff          bool
	startAt, startTz string
}

func (w *waitFlags) register(c *cmd, start bool) {
	c.fs.Int64Var(&w.afterTask, "after-task", 0, "run after this task")
	c.fs.StringVar(&w.afterThread, "after-thread", "", "run after this T3 session")
	c.fs.BoolVar(&w.afterMe, "after-me", false, "run after your own session")
	c.fs.BoolVar(&w.handoff, "handoff", true, "append the predecessor's final reply, PRs, branch and thread link at launch (--handoff=false to skip)")
	if start {
		c.fs.StringVar(&w.startAt, "start-at", "", "one-shot start YYYY-MM-DDTHH:mm")
		c.fs.StringVar(&w.startTz, "tz", "", "IANA zone for --start-at")
	}
}

func runTask(ctx context.Context, env Env, args []string) error {
	v, rest := verb(args, "ls")
	c := newCmd(env, "task "+v)
	o := func() *agenttools.Ops { return c.ops() }
	printTask := func(t any) error { return c.out(t, func(w io.Writer) { printAnyTask(w, t) }) }
	switch v {
	case "ls", "list":
		var a agenttools.ListTasksArgs
		var states string
		c.fs.StringVar(&a.Project, "project", "", "project")
		c.fs.StringVar(&states, "state", "", "states")
		c.fs.StringVar(&a.Tag, "tag", "", "tag")
		c.fs.StringVar(&a.Query, "q", "", "search")
		c.fs.IntVar(&a.Limit, "limit", 50, "max rows")
		if err := c.parse(rest); err != nil {
			return err
		}
		a.States = csv(states)
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
		var f configFlags
		var wf waitFlags
		var start bool
		f.register(c)
		wf.register(c, true)
		c.fs.BoolVar(&start, "start", false, "start now")
		if err := c.parse(rest); err != nil {
			return err
		}
		cfg, err := f.create(c)
		if err != nil {
			return err
		}
		res, err := o().CreateTask(ctx, agenttools.CreateTaskArgs{
			ConfigArgs: cfg, Start: start,
			WaitArgs: agenttools.WaitArgs{
				AfterTaskID: wf.afterTask, AfterThreadID: wf.afterThread, AfterMe: wf.afterMe,
				StartLocal: wf.startAt, StartTz: wf.startTz, Handoff: boolFlag(c, "handoff", wf.handoff),
			},
		})
		if err != nil {
			return err
		}
		return printTask(res)
	case "edit", "update":
		var f configFlags
		var startAt, tz string
		var handoff bool
		f.register(c)
		c.fs.StringVar(&startAt, "start-at", "", "one-shot start (empty clears)")
		c.fs.StringVar(&tz, "tz", "", "zone for --start-at")
		c.fs.BoolVar(&handoff, "handoff", true, "append the predecessor handoff at launch (true|false)")
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
		a := agenttools.UpdateTaskArgs{UpdateArgs: u, StartLocal: strFlag(c, "start-at", startAt), StartTz: strFlag(c, "tz", tz), Handoff: boolFlag(c, "handoff", handoff)}
		res, err := o().UpdateTask(ctx, a)
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
		return c.out(res, func(w io.Writer) { fmt.Fprintf(w, "deleted task %d\n", id) })
	case "start", "stop", "dup", "duplicate", "unlink":
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
			"unlink":    wrap(ops.UnlinkTask),
		}[v]
		res, err := fn(ctx, agenttools.IDArgs{ID: id})
		if err != nil {
			return err
		}
		return printTask(res)
	case "followup":
		var a agenttools.FollowupArgs
		var images list
		c.fs.StringVar(&a.Message, "m", "", "message (- = stdin)")
		c.fs.StringVar(&a.Message, "message", "", "message (- = stdin)")
		c.fs.StringVar(&a.Mode, "mode", "", "send mode")
		c.fs.Var(&images, "image", "image file (repeatable)")
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		if a.Message == "-" {
			b, err := io.ReadAll(env.Stdin)
			if err != nil {
				return err
			}
			a.Message = string(b)
		}
		a.ID, a.ImagePaths = id, images
		res, err := o().FollowupTask(ctx, a)
		if err != nil {
			return err
		}
		return printTask(res)
	case "link":
		var wf waitFlags
		wf.register(c, false)
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		res, err := o().LinkTask(ctx, agenttools.LinkTaskArgs{ID: id, AfterTaskID: wf.afterTask, AfterThreadID: wf.afterThread, AfterMe: wf.afterMe, Handoff: boolFlag(c, "handoff", wf.handoff)})
		if err != nil {
			return err
		}
		return printTask(res)
	case "order":
		if err := c.parse(rest); err != nil {
			return err
		}
		ids := make([]int64, len(c.pos))
		for i, p := range c.pos {
			id, err := parseID(p)
			if err != nil {
				return err
			}
			ids[i] = id
		}
		if len(ids) < 2 {
			return usagef("task order: want at least two ids, in run order")
		}
		res, err := o().OrderTasks(ctx, agenttools.OrderTasksArgs{TaskIDs: ids})
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) { printOrder(w, res.Tasks) })
	case "wait":
		var until string
		var timeout time.Duration
		c.fs.StringVar(&until, "until", "", "states")
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
				fmt.Fprintln(w, "timed out")
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
		var a agenttools.ProjectArgs
		c.fs.StringVar(&a.Project, "project", "", "project")
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
		var f configFlags
		var a agenttools.CreateScheduleArgs
		f.register(c)
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
		a.ConfigArgs = cfg
		res, err := c.ops().CreateSchedule(ctx, a)
		if err != nil {
			return err
		}
		return printSchedule(res)
	case "edit", "update":
		var f configFlags
		var cron, tz string
		f.register(c)
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
		res, err := c.ops().UpdateSchedule(ctx, agenttools.UpdateScheduleArgs{UpdateArgs: u, Cron: strFlag(c, "cron", cron), Tz: strFlag(c, "tz", tz)})
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
		return c.out(res, func(w io.Writer) { fmt.Fprintf(w, "deleted schedule %d\n", id) })
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
	case "ls", "list":
		var a agenttools.ListSessionsArgs
		c.fs.StringVar(&a.Project, "project", "", "project")
		c.fs.BoolVar(&a.Active, "active", false, "only active")
		c.fs.StringVar(&a.Query, "q", "", "search")
		c.fs.IntVar(&a.Limit, "limit", 20, "max rows")
		if err := c.parse(rest); err != nil {
			return err
		}
		res, err := c.ops().ListSessions(ctx, a)
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) { printSessions(w, res) })
	case "search", "find":
		var a agenttools.SearchSessionsArgs
		c.fs.StringVar(&a.Project, "project", "", "project")
		c.fs.IntVar(&a.Limit, "limit", 20, "max matches per source")
		if err := c.parse(rest); err != nil {
			return err
		}
		a.Query = strings.Join(c.pos, " ")
		if strings.TrimSpace(a.Query) == "" {
			return usagef("sessions search: want a query")
		}
		res, err := c.ops().SearchSessions(ctx, a)
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) { printSearch(w, res) })
	case "show", "get":
		if err := c.parse(rest); err != nil {
			return err
		}
		if len(c.pos) != 1 {
			return usagef("sessions show: want one thread id")
		}
		res, err := c.ops().GetSession(ctx, agenttools.SessionArgs{ID: c.pos[0]})
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) { printSessionDetail(w, res) })
	case "questions":
		if err := c.parse(rest); err != nil {
			return err
		}
		if len(c.pos) != 1 {
			return usagef("sessions questions: want one thread id")
		}
		res, err := c.ops().GetSessionQuestions(ctx, agenttools.SessionArgs{ID: c.pos[0]})
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) { printQuestions(w, c.pos[0], res) })
	}
	return usagef("sessions %s: unknown", v)
}
