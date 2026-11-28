package cli

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/agenttools"
)

// treeMember parses a `tree new` member: ID or ID:NEED,NEED.
func treeMember(s string) (agenttools.TreeTaskArgs, error) {
	idPart, needPart, hasNeeds := strings.Cut(s, ":")
	id, err := parseID(strings.TrimPrefix(idPart, "#"))
	if err != nil {
		return agenttools.TreeTaskArgs{}, err
	}
	m := agenttools.TreeTaskArgs{ID: id}
	if hasNeeds {
		if m.Needs, err = parseIDs(csv(needPart)); err != nil {
			return agenttools.TreeTaskArgs{}, err
		}
	}
	return m, nil
}

func runTree(ctx context.Context, env Env, args []string) error {
	v, rest := verb(args, "ls")
	c := newCmd(env, "tree "+v)
	printTreeOut := func(res any) error { return c.out(res, func(w io.Writer) { printAnyTree(w, res) }) }
	switch v {
	case "notes":
		return runNotes(ctx, env, "tree", rest)
	case "ls", "list":
		var a agenttools.ListTreesArgs
		c.fs.BoolVar(&a.All, "all", false, "every tree (default: active)")
		c.fs.StringVar(&a.Status, "status", "", "active|done|archived|all (default active)")
		c.fs.StringVar(&a.Project, "project", "", "project")
		c.fs.StringVar(&a.CompletedFrom, "completed-from", "", "with active: also trees completed since (ISO date or datetime, UTC)")
		if err := c.parse(rest); err != nil {
			return err
		}
		res, err := c.ops().ListTrees(ctx, a)
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) { printTreeRows(w, res) })
	case "show", "get":
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		res, err := c.ops().GetTree(ctx, agenttools.TreeIDArgs{ID: id})
		if err != nil {
			return err
		}
		return printTreeOut(res)
	case "new", "create":
		var a agenttools.CreateTreeArgs
		c.fs.StringVar(&a.Title, "title", "", "tree title")
		c.fs.StringVar(&a.StartMode, "start-mode", "", "auto|manual (default auto)")
		if err := c.parse(rest); err != nil {
			return err
		}
		if strings.TrimSpace(a.Title) == "" || len(c.pos) == 0 {
			return usagef("tree new: want --title and task ids (ID or ID:NEED,NEED)")
		}
		for _, p := range c.pos {
			m, err := treeMember(p)
			if err != nil {
				return err
			}
			a.Tasks = append(a.Tasks, m)
		}
		res, err := c.ops().CreateTree(ctx, a)
		if err != nil {
			return err
		}
		return printTreeOut(res)
	case "add":
		var needs string
		c.fs.StringVar(&needs, "needs", "", "task ids (tree members) it runs after, comma-separated; omitted: a parallel root (a member keeps its needs)")
		if err := c.parse(rest); err != nil {
			return err
		}
		if len(c.pos) != 2 {
			return usagef("tree add: want TREE_ID TASK_ID")
		}
		tree, err := parseID(c.pos[0])
		if err != nil {
			return err
		}
		task, err := parseID(c.pos[1])
		if err != nil {
			return err
		}
		a := agenttools.AddToTreeArgs{TreeID: tree, TaskID: task}
		if c.set("needs") {
			ids, err := parseIDs(csv(needs))
			if err != nil {
				return err
			}
			a.Needs = append([]int64{}, ids...)
		}
		res, err := c.ops().AddToTree(ctx, a)
		if err != nil {
			return err
		}
		return printTreeOut(res)
	case "rm", "remove":
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		res, err := c.ops().RemoveFromTree(ctx, agenttools.TaskIDArgs{TaskID: id})
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) { success(w, "Removed task %d from tree %d", res.TaskID, res.TreeID) })
	case "edit", "update":
		var title, mode, hold string
		c.fs.StringVar(&title, "title", "", "tree title")
		c.fs.StringVar(&mode, "start-mode", "", "auto|manual")
		c.fs.StringVar(&hold, "hold", "", "steering hold: a duration like 5m, at most 1h (0 or off: no hold)")
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		a := agenttools.UpdateTreeArgs{ID: id, Title: strFlag(c, "title", title), StartMode: strFlag(c, "start-mode", mode)}
		if c.set("hold") {
			seconds, err := holdSeconds(hold)
			if err != nil {
				return err
			}
			a.SteeringHoldSeconds = &seconds
		}
		res, err := c.ops().UpdateTree(ctx, a)
		if err != nil {
			return err
		}
		return printTreeOut(res)
	case "start":
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		res, err := c.ops().StartTree(ctx, agenttools.TreeIDArgs{ID: id})
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) { printTreeStart(w, res) })
	case "done", "archive":
		var undo bool
		c.fs.BoolVar(&undo, "undo", false, "reopen (done) or unarchive (archive)")
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		status := "done"
		if v == "archive" {
			status = "archived"
		}
		if undo {
			status = "open"
		}
		res, err := c.ops().UpdateTree(ctx, agenttools.UpdateTreeArgs{ID: id, Status: &status})
		if err != nil {
			return err
		}
		return printTreeOut(res)
	case "rm-tree":
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		res, err := c.ops().DeleteTree(ctx, agenttools.TreeIDArgs{ID: id})
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) { success(w, "Deleted tree %d and its tasks", id) })
	case "steer":
		var a agenttools.SetTreeSteererArgs
		var f configFlags
		c.fs.StringVar(&f.prompt, "prompt", "", "the steerer's prompt (- = stdin)")
		c.fs.StringVar(&f.promptFile, "prompt-file", "", "read the prompt from a file")
		c.fs.StringVar(&a.Title, "title", "", "steerer task title (default Steer: <tree title>)")
		c.fs.Int64Var(&a.ConnectionID, "connection", 0, "destination connection ID (its harness must offer send)")
		c.fs.StringVar(&a.WorkspaceID, "workspace-id", "", "exact workspace ID from projects ls")
		c.fs.StringVar(&a.WorkspaceID, "workspace", "", "same as --workspace-id")
		c.fs.BoolVar(&a.Replace, "replace", false, "replace the current steerer (ends it and stops its session)")
		c.fs.StringVar(&a.RequestID, "request-id", "", "idempotency key (default: a new one)")
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		if a.Prompt, _, err = f.readPrompt(c); err != nil {
			return err
		}
		if strings.TrimSpace(a.Prompt) == "" {
			return usagef("--prompt is required")
		}
		a.TreeID = id
		res, err := c.ops().SetTreeSteerer(ctx, a)
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) { printSteerer(w, res) })
	case "unsteer":
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		res, err := c.ops().EndTreeSteering(ctx, agenttools.TreeSteeringArgs{TreeID: id})
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) { printSteerer(w, res) })
	case "steering-events":
		var a agenttools.SteeringEventsArgs
		c.fs.Int64Var(&a.After, "after", 0, "events after this event id")
		c.fs.IntVar(&a.Limit, "limit", 0, "at most this many (1-200, default 50)")
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		a.TreeID = id
		res, err := c.ops().GetSteeringEvents(ctx, a)
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) { printSteeringEvents(w, res) })
	case "dissolve":
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		res, err := c.ops().DissolveTree(ctx, agenttools.TreeIDArgs{ID: id})
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) { success(w, "Dissolved tree %d: its tasks are standalone, the tree is archived", id) })
	}
	return usagef("tree %s: unknown", v)
}

// holdSeconds parses tree edit --hold: a whole-second Go duration, or 0/off.
func holdSeconds(v string) (int64, error) {
	v = strings.TrimSpace(v)
	if v == "0" || v == "off" {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 || d%time.Second != 0 {
		return 0, usagef("tree edit: --hold wants a whole-second duration like 5m, or 0/off; got %q", v)
	}
	return int64(d / time.Second), nil
}
