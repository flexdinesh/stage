package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/flexdinesh/stage/server/internal/agenttools"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
)

func table(w io.Writer, header string, rows func(tw io.Writer)) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, header)
	rows(tw)
	_ = tw.Flush()
}

func orDash(s *string) string {
	if s == nil || *s == "" {
		return "-"
	}
	return *s
}

func ts(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04")
}

func printContext(w io.Writer, c agenttools.ContextOut) {
	fmt.Fprintf(w, "session  %s\ncwd      %s\napi      %s\n", dash(c.ThreadID), dash(c.Cwd), c.APIURL)
	if c.Project != nil {
		fmt.Fprintf(w, "project  %d %s (%s, from %s)\n", c.Project.ID, c.Project.Title, c.Project.WorkspaceRoot, c.ProjectSource)
	} else {
		fmt.Fprintf(w, "project  - (%s)\n", c.ProjectError)
	}
	if c.CallerTaskID != nil {
		fmt.Fprintf(w, "task     %d\n", *c.CallerTaskID)
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func printProjects(w io.Writer, ps []db.Project) {
	table(w, "ID\tTITLE\tROOT", func(tw io.Writer) {
		for _, p := range ps {
			fmt.Fprintf(tw, "%d\t%s\t%s\n", p.ID, p.Title, p.WorkspaceRoot)
		}
	})
}

func printCatalog(w io.Writer, c client.Catalog) {
	for _, p := range c.Providers {
		fmt.Fprintf(w, "%s (%s)\n", p.ProviderInstanceID, p.DisplayName)
		for _, m := range p.Models {
			var opts []string
			for _, o := range m.Options {
				vals := make([]string, len(o.Values))
				for i, v := range o.Values {
					b, _ := v.MarshalJSON()
					vals[i] = strings.Trim(string(b), `"`)
				}
				opts = append(opts, o.ID+"="+strings.Join(vals, "|"))
			}
			fmt.Fprintf(w, "  %s  %s  %s\n", m.ID, m.Label, strings.Join(opts, " "))
		}
	}
}

func printTaskRows(w io.Writer, res agenttools.TasksOut) {
	table(w, "ID\tSTATE\tPROJECT\tTITLE\tWAITS ON", func(tw io.Writer) {
		for _, t := range res.Tasks {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", t.ID, t.State, t.Project, t.Title, waitsOn(t.AfterTaskID, t.AfterThreadID, t.StartAt))
		}
	})
	if res.Total > len(res.Tasks) {
		fmt.Fprintf(w, "(%d of %d; --limit for more)\n", len(res.Tasks), res.Total)
	}
}

func waitsOn(task *int64, thread *string, start *time.Time) string {
	switch {
	case task != nil:
		return fmt.Sprintf("task %d", *task)
	case thread != nil:
		return "session " + *thread
	case start != nil:
		return "start " + ts(start)
	}
	return "-"
}

func printTask(w io.Writer, t client.Task) {
	fmt.Fprintf(w, "task %d  %s  [%s]  %s\n", t.ID, t.Title, t.State, t.ProjectTitle)
	model := "T3 default"
	if t.Model != nil {
		model = orDash(t.ProviderInstanceID) + "/" + *t.Model
	}
	fmt.Fprintf(w, "  model    %s  %s  %s\n", model, t.RuntimeMode, t.InteractionMode)
	if t.Workspace.Type == "worktree" {
		fmt.Fprintf(w, "  worktree %s (branch %s)\n", t.Workspace.BaseRef, orDash(t.Workspace.Branch))
	}
	if s := waitsOn(t.AfterTaskID, t.AfterThreadID, t.StartAt); s != "-" {
		fmt.Fprintf(w, "  waits on %s\n", s)
	}
	if t.OriginThreadID != nil {
		fmt.Fprintf(w, "  created by session %s %s\n", *t.OriginThreadID, orDash(t.OriginThreadTitle))
	}
	if t.T3ThreadID != nil {
		fmt.Fprintf(w, "  thread   %s\n", *t.T3ThreadID)
	}
	if t.T3WebURL != nil {
		fmt.Fprintf(w, "  open     %s\n", *t.T3WebURL)
	}
	if t.LastError != nil {
		fmt.Fprintf(w, "  error    %s\n", *t.LastError)
	}
}

func printAnyTask(w io.Writer, v any) {
	switch t := v.(type) {
	case client.Task:
		printTask(w, t)
	case client.TaskDetail:
		printTaskDetail(w, t)
	default:
		fmt.Fprintf(w, "%v\n", v)
	}
}

func printTaskDetail(w io.Writer, d client.TaskDetail) {
	printTask(w, d.Task)
	if len(d.Tags) > 0 {
		fmt.Fprintf(w, "  tags     %s\n", strings.Join(d.Tags, ", "))
	}
	fmt.Fprintf(w, "\n%s\n", strings.TrimRight(d.Prompt, "\n"))
	if len(d.Events) > 0 {
		fmt.Fprintln(w)
		for _, e := range d.Events {
			fmt.Fprintf(w, "  %s  %s  %s\n", e.At.Local().Format("2006-01-02 15:04:05"), e.ToState, e.Detail)
		}
	}
}

func printSchedules(w io.Writer, ss []db.Schedule) {
	table(w, "ID\tNEXT\tCRON\tTZ\tPAUSED\tPROJECT\tTITLE", func(tw io.Writer) {
		for _, s := range ss {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%v\t%s\t%s\n", s.ID, ts(s.NextRunAt), s.Cron, s.Tz, s.Paused, s.ProjectTitle, s.Title)
		}
	})
}

func printSchedule(w io.Writer, s db.Schedule) {
	fmt.Fprintf(w, "schedule %d  %s  %s  (%s %s)  next %s\n", s.ID, s.Title, s.ProjectTitle, s.Cron, s.Tz, ts(s.NextRunAt))
	if s.Paused {
		fmt.Fprintln(w, "  paused")
	}
}

func printAnySchedule(w io.Writer, v any) {
	if s, ok := v.(db.Schedule); ok {
		printSchedule(w, s)
		return
	}
	fmt.Fprintf(w, "%v\n", v)
}

func printScheduleDetail(w io.Writer, d client.ScheduleDetail) {
	printSchedule(w, d.Schedule)
	fmt.Fprintf(w, "\n%s\n", strings.TrimRight(d.Prompt, "\n"))
	for _, r := range d.Runs {
		task := "-"
		if r.TaskID != nil {
			task = fmt.Sprintf("task %d", *r.TaskID)
		}
		fmt.Fprintf(w, "  %s  %s  %s  %s\n", r.FireAt.Local().Format("2006-01-02 15:04"), r.Outcome, task, r.Detail)
	}
}

func printSessions(w io.Writer, res agenttools.SessionsOut) {
	table(w, "STATE\tPROJECT\tTITLE\tTHREAD\tTASK", func(tw io.Writer) {
		for _, s := range res.Sessions {
			task := "-"
			if s.TaskID != nil {
				task = fmt.Sprint(*s.TaskID)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", s.State, orDash(s.Project), s.Title, s.T3ThreadID, task)
		}
	})
	if res.Total > len(res.Sessions) {
		fmt.Fprintf(w, "(%d of %d; --limit for more)\n", len(res.Sessions), res.Total)
	}
}

func printSessionDetail(w io.Writer, d client.ThreadDetail) {
	t := d.Thread
	fmt.Fprintf(w, "session %s  %s  [%s]  %s\n", t.T3ThreadID, t.Title, t.State, orDash(t.ProjectTitle))
	if t.T3WebURL != nil {
		fmt.Fprintf(w, "  open  %s\n", *t.T3WebURL)
	}
	if d.Task != nil {
		fmt.Fprintf(w, "  task  %d %s\n", d.Task.ID, d.Task.Title)
	}
	for _, c := range d.Children {
		fmt.Fprintf(w, "  child %s  %s  [%s]\n", c.T3ThreadID, c.Title, c.State)
	}
}
