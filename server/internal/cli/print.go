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
	"github.com/flexdinesh/stage/server/internal/execution"
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
	session := ""
	if c.Session != nil {
		session = fmt.Sprintf("%s (connection %d)", c.Session.ExternalSessionID, c.Session.ConnectionID)
		if c.SessionSource != "" {
			session += ", from " + c.SessionSource
		}
	} else if c.CallerWarning != "" {
		session = "- (" + c.CallerWarning + ")"
	}
	fmt.Fprintf(w, "session  %s\ncwd      %s\napi      %s\n", dash(session), dash(c.Cwd), c.APIURL)
	if c.Project != nil {
		fmt.Fprintf(w, "project  %d %s (%s, from %s)\n", c.Project.ID, c.Project.Title, projectWorkspace(*c.Project), c.ProjectSource)
	} else {
		fmt.Fprintf(w, "project  - (%s)\n", c.ProjectError)
	}
	if c.TreeID != nil {
		fmt.Fprintf(w, "tree     %d %s\n", *c.TreeID, orDash(c.TreeTitle))
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
			fmt.Fprintf(tw, "%d\t%s\t%s\n", p.ID, p.Title, projectWorkspace(p))
		}
	})
}

func printCapabilities(w io.Writer, c execution.Capabilities) {
	for _, m := range c.Models {
		fmt.Fprintf(w, "%s  %s  %s\n", m.ID, m.Label, m.Reason)
	}
	for _, v := range c.Workspaces {
		fmt.Fprintf(w, "workspace %s  %s\n", v.ID, v.Reason)
	}
	for _, v := range c.Modes {
		fmt.Fprintf(w, "mode %s  %s\n", v.ID, v.Reason)
	}
	for _, o := range c.Options {
		vals := make([]string, len(o.Values))
		for i, v := range o.Values {
			b, _ := v.MarshalJSON()
			vals[i] = strings.Trim(string(b), `"`)
		}
		fmt.Fprintf(w, "option %s=%s  (any model)\n", o.ID, strings.Join(vals, "|"))
	}
	fmt.Fprintln(w, "Use --json for model options, permissions and operation availability.")
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
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", t.ID, t.State, t.Project, t.Title, waitsOn(t.Needs, t.StartAt))
		}
	})
	if res.Total > len(res.Tasks) {
		fmt.Fprintf(w, "(%d of %d; --limit for more)\n", len(res.Tasks), res.Total)
	}
}

func waitsOn(needs []int64, start *time.Time) string {
	switch {
	case len(needs) > 0:
		return "needs " + needLabels(needs)
	case start != nil:
		return "start " + ts(start)
	}
	return "-"
}

func printTask(w io.Writer, t client.Task) {
	fmt.Fprintf(w, "task %d  %s  [%s]  %s\n", t.ID, t.Title, t.State, t.ProjectTitle)
	model, permission, mode := "harness default", "", ""
	if !t.ConnectionActive && t.ConnectionID > 0 {
		fmt.Fprintf(w, "  replaced connection %d (%s): history only\n", t.ConnectionID, t.ConnectionLabel)
	}
	if t.ConnectionID > 0 {
		fmt.Fprintf(w, "  connection %d\n", t.ConnectionID)
	}
	if cfg := t.ExecutionConfig; cfg != nil {
		if cfg.Model != "" {
			model = cfg.Model
		}
		permission, mode = cfg.Permission, cfg.Mode
	}
	fmt.Fprintf(w, "  model    %s  %s  %s\n", model, permission, mode)
	switch {
	case t.Workspace.FromPredecessor():
		fmt.Fprintf(w, "  worktree from %s's branch (configured ref %s, branch %s)\n", predecessorLabel(t), t.Workspace.BaseRef, orDash(t.Workspace.Branch))
	case t.Workspace.Type == "worktree":
		fmt.Fprintf(w, "  worktree %s (branch %s)\n", t.Workspace.BaseRef, orDash(t.Workspace.Branch))
	}
	if t.TreeID != nil {
		mode := "-"
		if t.EffectiveStartMode != nil {
			mode = string(*t.EffectiveStartMode)
		}
		fmt.Fprintf(w, "  tree     %d %s (start %s)\n", *t.TreeID, orDash(t.TreeTitle), mode)
	}
	if s := waitsOn(t.Needs, t.StartAt); s != "-" {
		fmt.Fprintf(w, "  waits on %s\n", s)
	}
	if t.Readiness != nil {
		fmt.Fprintf(w, "  readiness %s\n", *t.Readiness)
	}
	if t.Adopted {
		fmt.Fprintln(w, "  adopted  tracks an existing session (stage never starts it)")
	}
	if t.OriginSession != nil {
		fmt.Fprintf(w, "  created by connection %d session %s %s\n", t.OriginSession.ConnectionID, t.OriginSession.ExternalSessionID, orDash(t.OriginThreadTitle))
	}
	if t.Session != nil {
		fmt.Fprintf(w, "  session  %s\n", t.Session.ExternalSessionID)
	}
	if open := orFirst(t.Links.Web, t.Links.Native); open != "" {
		fmt.Fprintf(w, "  open     %s\n", open)
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
	for _, link := range d.ArtifactLinks {
		fmt.Fprintf(w, "artifact  %s %s\n", link.Label, link.URL)
	}
	printTask(w, d.Task)
	printHandoff(w, d)
	printLaunchBase(w, d.CurrentLaunchBase)
	fmt.Fprintf(w, "\n%s\n", strings.TrimRight(d.Prompt, "\n"))
	if d.Thread != nil {
		printLastWord(w, d.Thread.LastWord)
	} else if d.FinalReply != nil {
		fmt.Fprintf(w, "\nFinal reply\n%s\n", d.FinalReply.Text)
	}
	if len(d.Events) > 0 {
		fmt.Fprintln(w)
		for _, e := range d.Events {
			fmt.Fprintf(w, "  %s  %s  %s\n", e.At.Local().Format("2006-01-02 15:04:05"), e.ToState, e.Detail)
		}
	}
}

// printHandoff: the toggle for a task with needs, then the current
// attempt's record per need.
func printHandoff(w io.Writer, d client.TaskDetail) {
	if len(d.Needs) > 0 {
		state := "on"
		if !d.Handoff {
			state = "off"
		}
		fmt.Fprintf(w, "  handoff  %s\n", state)
	}
	for _, h := range d.CurrentHandoffs {
		from := h.SourceTitle
		if h.SourceTaskID != nil {
			from = fmt.Sprintf("#%d", *h.SourceTaskID)
		}
		switch h.Status {
		case db.HandoffSent:
			fmt.Fprintf(w, "  handoff  sent from %s (attempt %d, %d chars)\n", from, h.Attempt, len([]rune(h.Text)))
		default:
			reason := ""
			if h.Reason != nil {
				reason = *h.Reason
			}
			fmt.Fprintf(w, "  handoff  skipped from %s (attempt %d): %s\n", from, h.Attempt, reason)
		}
	}
}

// needLabels is "#3 #5".
func needLabels(ids []int64) string {
	labels := make([]string, len(ids))
	for i, id := range ids {
		labels[i] = fmt.Sprintf("#%d", id)
	}
	return strings.Join(labels, " ")
}

// predecessorLabel names the need a worktree starts from: workspace
// baseTaskId, else the sole need, else `a need`.
func predecessorLabel(t client.Task) string {
	switch {
	case t.Workspace.BaseTaskID != nil:
		return fmt.Sprintf("#%d", *t.Workspace.BaseTaskID)
	case len(t.Needs) == 1:
		return fmt.Sprintf("#%d", t.Needs[0])
	}
	return "a need"
}

// printLaunchBase prints the base the current launch attempt used:
// `base     feat-x (from #12)` or `base     origin/main (fallback: <reason>)`.
func printLaunchBase(w io.Writer, b *db.LaunchBase) {
	if b == nil {
		return
	}
	if b.Source == db.LaunchBasePredecessor {
		from := db.HandoffSource{TaskID: b.SourceTaskID, Title: b.SourceTitle}.Label()
		fmt.Fprintf(w, "  base     %s (from %s)\n", b.BaseRef, from)
		return
	}
	reason := ""
	if b.Reason != nil {
		reason = *b.Reason
	}
	fmt.Fprintf(w, "  base     %s (fallback: %s)\n", b.BaseRef, reason)
}

func printTreeRows(w io.Writer, res agenttools.TreesOut) {
	if len(res.Trees) == 0 {
		fmt.Fprintln(w, "no trees")
		return
	}
	table(w, "ID\tMODE\tTASKS\tLEVELS\tSTATES\tPROJECTS\tTITLE", func(tw io.Writer) {
		for _, t := range res.Trees {
			fmt.Fprintf(tw, "%d\t%s\t%d\t%d\t%s\t%s\t%s\n", t.ID, t.StartMode, t.Tasks, t.Levels, stateCounts(t.States),
				strings.Join(t.Projects, ","), t.Title)
		}
	})
}

// stateCounts is "running 1, ready 2" in lifecycle order.
func stateCounts(m map[string]int) string {
	var parts []string
	for _, k := range []string{"needs_input", "ready", "running", "launching", "blocked", "backlog", "done", "failed", "interrupted", "cancelled", "settled"} {
		if n := m[k]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", k, n))
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

func printAnyTree(w io.Writer, v any) {
	if t, ok := v.(client.TreeDetail); ok {
		printTree(w, t)
		return
	}
	fmt.Fprintf(w, "%v\n", v)
}

// printTree prints a tree's levels (roots first): each member's state, id,
// title, needs and readiness.
func printTree(w io.Writer, t client.TreeDetail) {
	fmt.Fprintf(w, "tree %d  %s  (%s)  %d tasks, %d levels\n", t.ID, t.Title, t.StartMode, len(t.Tasks), len(t.Levels))
	byID := make(map[int64]client.Task, len(t.Tasks))
	for _, m := range t.Tasks {
		byID[m.ID] = m
	}
	table(w, "  LEVEL\tID\tSTATE\tTITLE\tNEEDS\tREADINESS", func(tw io.Writer) {
		for lvl, ids := range t.Levels {
			for _, id := range ids {
				m, ok := byID[id]
				if !ok {
					continue
				}
				needs, ready := "-", "-"
				if len(m.Needs) > 0 {
					needs = needLabels(m.Needs)
				}
				if m.Readiness != nil {
					ready = string(*m.Readiness)
				}
				fmt.Fprintf(tw, "  %d\t#%d\t%s\t%s\t%s\t%s\n", lvl, m.ID, m.State, m.Title, needs, ready)
			}
		}
	})
}

func printTreeStart(w io.Writer, res client.TreeStart) {
	if len(res.Started) == 0 && len(res.Errors) == 0 {
		fmt.Fprintln(w, "no ready tasks")
	}
	for _, t := range res.Started {
		fmt.Fprintf(w, "started #%d [%s] %s\n", t.ID, t.State, t.Title)
	}
	for _, e := range res.Errors {
		fmt.Fprintf(w, "failed  #%d: %s\n", e.TaskID, e.Error)
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

func printSessionDetail(w io.Writer, d client.ThreadDetail) {
	t := d.Thread
	fmt.Fprintf(w, "session %s  %s  [%s]  %s\n", t.T3ThreadID, t.Title, t.State, orDash(t.ProjectTitle))
	if d.Task != nil {
		if open := orFirst(d.Task.Links.Web, d.Task.Links.Native); open != "" {
			fmt.Fprintf(w, "  open  %s\n", open)
		}
		fmt.Fprintf(w, "  task  %d %s\n", d.Task.ID, d.Task.Title)
	}

}

// printRef prints what a reference names: a task (with its session id), a
// tree, or a session (with its task), then how to read the full conversation.
func printRef(w io.Writer, r agenttools.RefOut) {
	switch {
	case r.Tree != nil:
		printTree(w, *r.Tree)
	case r.Kind == "session" && r.Session != nil:
		printSessionDetail(w, *r.Session)
	case r.Task != nil:
		printTaskDetail(w, *r.Task)
		if r.Session != nil {
			fmt.Fprintf(w, "\nsession %s\n", r.Session.Thread.T3ThreadID)
		}
	}
	if r.SessionRef != nil && r.Session == nil {
		fmt.Fprintf(w, "session %s (connection %d)  %s\n", r.SessionRef.ExternalSessionID, r.SessionRef.ConnectionID, r.SessionTitle)
	}
	for _, link := range []string{r.Links.Web, r.Links.App, r.Links.Native} {
		if link != "" {
			fmt.Fprintln(w, link)
		}
	}
	if r.Next != "" {
		fmt.Fprintf(w, "\n%s\n", r.Next)
	}
}

// cliRunLimit caps the runs block; --json returns every stored run.
const cliRunLimit = 10

// runModel is the executed model, "requested -> executed" when they differ.
func runModel(r db.ThreadRun) string {
	switch {
	case r.ExecutedModel == nil || *r.ExecutedModel == "":
		return orDash(r.Model)
	case r.Model != nil && *r.Model != "" && *r.Model != *r.ExecutedModel:
		return *r.Model + " -> " + *r.ExecutedModel
	}
	return *r.ExecutedModel
}

// span is to - from rounded to seconds; "-" when either end is unknown.
func span(from, to *time.Time) string {
	if from == nil || to == nil || from.IsZero() || to.Before(*from) {
		return "-"
	}
	return to.Sub(*from).Round(time.Second).String()
}

// printLastWord prints the thread's latest assistant reply excerpt.
func printLastWord(w io.Writer, lw *db.LastWord) {
	if lw == nil {
		return
	}
	fmt.Fprintf(w, "\nlast word (%s)\n%s\n", ts(lw.At), strings.TrimRight(lw.Text, "\n"))
	if lw.Truncated {
		fmt.Fprintln(w, "[excerpt; full reply in T3]")
	}
}

// printCreated prints a session batch: each tree's tasks (by id), then
// independent tasks.
func printCreated(w io.Writer, c client.CreatedTasks) {
	n := len(c.Independent)
	for _, tr := range c.Trees {
		n += len(tr.Tasks)
	}
	if n == 0 {
		return
	}
	fmt.Fprintf(w, "\ncreated tasks (%d)\n", n)
	for _, tr := range c.Trees {
		fmt.Fprintf(w, "  tree %d %s (%s)\n", tr.Tree.ID, tr.Tree.Title, tr.Tree.StartMode)
		for _, t := range tr.Tasks {
			fmt.Fprintf(w, "    #%d [%s] %s%s\n", t.ID, t.State, t.Title, createdWait(t))
		}
	}
	for _, t := range c.Independent {
		fmt.Fprintf(w, "  task #%d [%s] %s%s\n", t.ID, t.State, t.Title, createdWait(t))
	}
}

// createdWait is a batch row's wait ("  (waits on needs #3 #5)").
func createdWait(t client.Task) string {
	if s := waitsOn(t.Needs, t.StartAt); s != "-" {
		return "  (waits on " + s + ")"
	}
	return ""
}

func projectWorkspace(p db.Project) string {
	if len(p.Bindings) == 0 {
		return "workspace not bound"
	}
	var bindings []string
	for _, b := range p.Bindings {
		bindings = append(bindings, fmt.Sprintf("%s %d: %s", b.Harness, b.ConnectionID, b.Workspace))
	}
	return strings.Join(bindings, ", ")
}

// orFirst is the first nonempty value.
func orFirst(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
