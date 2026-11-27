package cli

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
)

func runProjects(ctx context.Context, env Env, args []string) error {
	v, rest := verb(args, "ls")
	c := newCmd(env, "projects "+v)
	switch v {
	case "ls":
		if err := c.parse(rest); err != nil {
			return err
		}
		api := c.ops().Client
		projects, err := api.ListProjects(ctx)
		if err != nil {
			return err
		}
		connections, err := api.Connections(ctx)
		if err != nil {
			return err
		}
		res := struct {
			Projects     []db.Project           `json:"projects"`
			Connections  []db.HarnessConnection `json:"connections"`
			Destinations []db.Destination       `json:"destinations,omitempty"`
		}{projects.Projects, connections, projects.Destinations}
		return c.out(res, func(w io.Writer) {
			printProjects(w, res.Projects)
			for _, conn := range res.Connections {
				fmt.Fprintf(w, "connection %d  %s\n", conn.ID, conn.Harness)
			}
			if len(res.Destinations) != 0 {
				table(w, "CONNECTION\tWORKSPACE\tTITLE\tMACHINE", func(tw io.Writer) {
					for _, d := range res.Destinations {
						fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", d.ConnectionID, d.WorkspaceID, d.Title, d.MachineLabel)
					}
				})
			}
		})
	case "workspaces":
		return runProjectWorkspaces(ctx, c, rest)
	case "merge":
		return runProjectMerge(ctx, c, rest)
	case "split":
		return runProjectSplit(ctx, c, rest)
	default:
		return usagef("projects %s: unknown", v)
	}
}

func projectRefID(ctx context.Context, api *client.Client, ref string) (int64, error) {
	if id, err := strconv.ParseInt(strings.TrimSpace(ref), 10, 64); err == nil && id > 0 {
		return id, nil
	}
	p, _, err := agentctx.ResolveProject(ctx, api, agentctx.Caller{}, ref)
	return p.ID, err
}

func runProjectWorkspaces(ctx context.Context, c *cmd, args []string) error {
	var review bool
	c.fs.BoolVar(&review, "review", false, "show discovery review")
	if err := c.parse(args); err != nil {
		return err
	}
	if len(c.pos) > 1 {
		return usagef("projects workspaces: want at most one project")
	}
	api := c.ops().Client
	var project *db.Project
	if len(c.pos) == 1 {
		id, err := projectRefID(ctx, api, c.pos[0])
		if err != nil {
			return err
		}
		p, err := api.Project(ctx, id)
		if err != nil {
			return err
		}
		project = &p
	}

	if review {
		out, err := api.WorkspaceReview(ctx)
		if err != nil {
			return err
		}
		return c.out(out, func(w io.Writer) { printWorkspaceReview(w, out, project) })
	}
	if project != nil {
		return c.out(*project, func(w io.Writer) { printProjectWorkspaces(w, []db.Project{*project}) })
	}
	out, err := api.ListProjects(ctx)
	if err != nil {
		return err
	}
	return c.out(out, func(w io.Writer) { printProjectWorkspaces(w, out.Projects) })
}

func printProjectWorkspaces(w io.Writer, projects []db.Project) {
	table(w, "PROJECT\tBINDING\tMACHINE\tHARNESS\tPATH\tSTATE\tPRIMARY\tREMOTE", func(tw io.Writer) {
		for _, p := range projects {
			for _, b := range p.Bindings {
				fmt.Fprintf(tw, "%d\t%d\t%d\t%s\t%s\t%s\t%t\t%s\n",
					p.ID, b.ID, b.MachineID, b.Harness, dash(orFirst(b.Workspace, b.ExternalProjectID)), b.Status, b.Primary, dash(b.Remote))
			}
		}
	})
}

func printWorkspaceReview(w io.Writer, review db.WorkspaceReview, p *db.Project) {
	table(w, "REASON\tCONNECTION\tPATH\tREMOTE\tCANDIDATES", func(tw io.Writer) {
		for _, ws := range review.Workspaces {
			if p != nil && !slices.Contains(ws.Resolution.Candidates, p.ID) && (ws.ProjectID == nil || *ws.ProjectID != p.ID) {
				continue
			}
			fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n", ws.Reason, ws.ConnectionID,
				dash(orFirst(ws.Workspace.Workspace, ws.ExternalID)), dash(ws.Remote), dash(needLabels(ws.Resolution.Candidates)))
		}
	})
}

func runProjectMerge(ctx context.Context, c *cmd, args []string) error {
	var yes bool
	c.fs.BoolVar(&yes, "yes", false, "confirm the merge")
	if err := c.parse(args); err != nil {
		return err
	}
	if len(c.pos) != 2 {
		return usagef("projects merge: want FROM and INTO")
	}
	api := c.ops().Client
	from, err := projectRefID(ctx, api, c.pos[0])
	if err != nil {
		return err
	}
	into, err := projectRefID(ctx, api, c.pos[1])
	if err != nil {
		return err
	}
	preview, err := api.MergeProjects(ctx, into, from, false, "")
	if err != nil {
		return err
	}
	printPreview := func(w io.Writer) {
		fmt.Fprintf(w, "Merge #%d %s into #%d %s\n", preview.From.ID, preview.From.Title, preview.Project.ID, preview.Project.Title)
		m := preview.Moves
		fmt.Fprintf(w, "%d tasks, %d schedules, %d snippets, %d workspaces\n", m.Tasks, m.Schedules, m.Snippets, m.Workspaces)
		if preview.Blocked != nil {
			fmt.Fprintln(w, preview.Blocked.Reason)
		}
	}
	if !yes {
		return c.out(preview, func(w io.Writer) {
			printPreview(w)
			fmt.Fprintln(w, "Run again with --yes to merge.")
		})
	}
	if !c.json {
		printPreview(c.env.stdout())
	}
	out, err := api.MergeProjects(ctx, into, from, true, preview.PreviewToken)
	if err != nil {
		return err
	}
	return c.out(out, func(w io.Writer) {
		fmt.Fprintf(w, "Merged into #%d %s\n", out.Project.ID, out.Project.Title)
	})
}

func runProjectSplit(ctx context.Context, c *cmd, args []string) error {
	var binding int64
	var title string
	var yes bool
	c.fs.Int64Var(&binding, "binding", 0, "workspace binding to split")
	c.fs.StringVar(&title, "title", "", "new project title")
	c.fs.BoolVar(&yes, "yes", false, "confirm the split")
	if err := c.parse(args); err != nil {
		return err
	}
	if !yes {
		return usagef("split needs --yes")
	}
	if len(c.pos) != 1 || binding <= 0 {
		return usagef("projects split: want PROJECT and --binding ID")
	}
	api := c.ops().Client
	id, err := projectRefID(ctx, api, c.pos[0])
	if err != nil {
		return err
	}
	out, err := api.SplitProject(ctx, id, binding, title)
	if err != nil {
		return err
	}
	return c.out(out, func(w io.Writer) {
		fmt.Fprintf(w, "Moved binding #%d from #%d %s to #%d %s\n", binding, out.Source.ID, out.Source.Title, out.Project.ID, out.Project.Title)
	})
}
