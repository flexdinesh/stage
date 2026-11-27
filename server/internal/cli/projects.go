package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/db"
)

func runProjects(ctx context.Context, env Env, args []string) error {
	v, rest := verb(args, "ls")
	c := newCmd(env, "projects "+v)
	switch v {
	case "ls":
		var a agenttools.ListProjectsArgs
		c.fs.StringVar(&a.Archived, "archived", "", "false|true|all (default false: active projects)")
		if err := c.parse(rest); err != nil {
			return err
		}
		res, err := c.ops().ListProjects(ctx, a)
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) {
			for _, group := range res.Groups {
				fmt.Fprintf(w, "#%d  %s\n", group.ID, group.Title)
			}
			for _, conn := range res.Connections {
				fmt.Fprintf(w, "connection %d  %s\n", conn.ID, conn.Label)
			}
			if len(res.Destinations) != 0 {
				table(w, "CONNECTION\tWORKSPACE\tTITLE\tMACHINE\tPROJECT\tSTATE", func(tw io.Writer) {
					for _, d := range res.Destinations {
						project, state, title := "—", "unavailable", d.Title
						if d.GroupProjectID != nil {
							project = strconv.FormatInt(*d.GroupProjectID, 10)
						}
						if d.Available {
							state = "available"
						}
						if d.Discriminator != "" {
							title += " (" + d.Discriminator + ")"
						}
						fmt.Fprintf(tw, "%d (%s)\t%s\t%s\t%s\t%s\t%s\n", d.ConnectionID, d.ConnectionLabel, d.WorkspaceID, title, d.MachineLabel, project, state)
					}
				})
			}
		})
	case "add", "edit":
		var title, archived string
		c.fs.StringVar(&title, "title", "", "project title")
		if v == "edit" {
			c.fs.StringVar(&archived, "archived", "", "true archives, false unarchives (omitted keeps it)")
		}
		if err := c.parse(rest); err != nil {
			return err
		}
		var res db.ProjectSummary
		var err error
		if v == "add" {
			if len(c.pos) != 0 {
				return usagef("projects add: takes --title, not an id")
			}
			res, err = c.ops().CreateProject(ctx, agenttools.CreateProjectArgs{Title: title})
		} else {
			id, idErr := c.oneID()
			if idErr != nil {
				return idErr
			}
			a := agenttools.UpdateProjectArgs{ID: id}
			if c.set("title") {
				a.Title = &title
			}
			if c.set("archived") {
				on, parseErr := strconv.ParseBool(archived)
				if parseErr != nil {
					return usagef("--archived: want true or false")
				}
				a.Archived = &on
			}
			res, err = c.ops().UpdateProject(ctx, a)
		}
		if err != nil {
			return err
		}
		return c.out(res, func(w io.Writer) {
			state := ""
			if res.ArchivedAt != nil {
				state = "  (archived)"
			}
			fmt.Fprintf(w, "#%d  %s%s\n", res.ID, res.Title, state)
		})
	case "workspaces":
		return usagef("projects workspaces was retired. Use projects ls for exact destinations or open Projects for workspace history.")
	case "merge", "split":
		return usagef("projects %s was retired. Use Projects to move or separate an exact workspace.", v)
	default:
		return usagef("projects %s: unknown", v)
	}
}

func retireDestinationFlag(c *cmd, name string) {
	c.fs.Func(name, "retired", func(string) error {
		return fmt.Errorf("--%s was retired. Choose the connection and --workspace-id from projects ls", name)
	})
}
