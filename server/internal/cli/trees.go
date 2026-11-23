package cli

import (
	"context"
	"io"
	"strings"

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
		var title, mode string
		c.fs.StringVar(&title, "title", "", "tree title")
		c.fs.StringVar(&mode, "start-mode", "", "auto|manual")
		if err := c.parse(rest); err != nil {
			return err
		}
		id, err := c.oneID()
		if err != nil {
			return err
		}
		res, err := c.ops().UpdateTree(ctx, agenttools.UpdateTreeArgs{ID: id, Title: strFlag(c, "title", title), StartMode: strFlag(c, "start-mode", mode)})
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
	}
	return usagef("tree %s: unknown", v)
}
