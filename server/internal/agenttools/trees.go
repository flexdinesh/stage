package agenttools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/agentctx"
	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
)

// ---- trees ----

// TreeRow is a compact tree for lists.
type TreeRow struct {
	ID        int64        `json:"id"`
	Title     string       `json:"title"`
	StartMode db.StartMode `json:"startMode"`
	Tasks     int          `json:"tasks"`
	Levels    int          `json:"levels"`
	Projects  []string     `json:"projects"`
	// States counts members by state (blocked and ready split backlog);
	// zero counts are left out.
	States    map[string]int `json:"states"`
	SettledAt *time.Time     `json:"settledAt,omitempty"`
	Status    db.TreeStatus  `json:"status"` // open | done | archived
}

func treeRow(t db.TreeSummary) TreeRow {
	c := t.Counts
	states := map[string]int{}
	for k, v := range map[string]int{
		"blocked": c.Blocked, "ready": c.Ready, "launching": c.Launching, "running": c.Running,
		"needs_input": c.NeedsInput, "done": c.Done, "settled": c.Settled, "failed": c.Failed,
		"interrupted": c.Interrupted, "cancelled": c.Cancelled, "backlog": c.Backlog - c.Blocked - c.Ready,
	} {
		if v > 0 {
			states[k] = v
		}
	}
	return TreeRow{
		ID: t.ID, Title: t.Title, StartMode: t.StartMode, Tasks: c.Total, Levels: t.Levels,
		Projects: t.Projects, States: states, SettledAt: t.SettledAt, Status: t.Status,
	}
}

// ListTreesArgs is list_trees.
type ListTreesArgs struct {
	All     bool   `json:"all,omitempty" jsonschema:"every tree (status all)"`
	Status  string `json:"status,omitempty" jsonschema:"active (default: open with a member not accepted) | done (marked done, or every member accepted) | archived | all"`
	Project string `json:"project,omitempty" jsonschema:"project id or title: trees with a member there; omitted = all projects"`
}

// TreesOut is list_trees.
type TreesOut struct {
	Trees []TreeRow `json:"trees"`
}

func (o *Ops) ListTrees(ctx context.Context, a ListTreesArgs) (TreesOut, error) {
	q := client.TreeQuery{All: a.All, Status: a.Status}
	if a.Project != "" {
		p, _, err := agentctx.ResolveProject(ctx, o.Client, agentctx.Caller{}, a.Project)
		if err != nil {
			return TreesOut{}, err
		}
		q.ProjectID = &p.ID
	}
	ts, err := o.Client.Trees(ctx, q)
	if err != nil {
		return TreesOut{}, err
	}
	out := TreesOut{Trees: []TreeRow{}}
	for _, t := range ts {
		out.Trees = append(out.Trees, treeRow(t))
	}
	return out, nil
}

// TreeIDArgs names one tree.
type TreeIDArgs struct {
	ID int64 `json:"id" jsonschema:"tree id"`
}

func (o *Ops) GetTree(ctx context.Context, a TreeIDArgs) (client.TreeDetail, error) {
	return o.Client.Tree(ctx, a.ID)
}

// TreeTaskArgs is a create_tree member.
type TreeTaskArgs struct {
	ID    int64   `json:"id" jsonschema:"task id"`
	Needs []int64 `json:"needs,omitempty" jsonschema:"task ids (members of this tree) it runs after; omitted = keep its needs (none: a parallel root)"`
}

// CreateTreeArgs is create_tree.
type CreateTreeArgs struct {
	Title     string         `json:"title" jsonschema:"tree title"`
	StartMode string         `json:"startMode,omitempty" jsonschema:"auto (default; omit unless asked: members launch when prerequisites are completed) | manual (explicit: members read ready; the user starts them)"`
	Tasks     []TreeTaskArgs `json:"tasks" jsonschema:"existing tasks to put in the tree, with the needs between them"`
}

func (o *Ops) CreateTree(ctx context.Context, a CreateTreeArgs) (client.TreeDetail, error) {
	mode, err := treeStartMode(a.StartMode)
	if err != nil {
		return client.TreeDetail{}, err
	}
	in := client.NewTree{Title: a.Title, StartMode: mode, Tasks: make([]db.TreeMember, len(a.Tasks))}
	for i, t := range a.Tasks {
		in.Tasks[i] = db.TreeMember{TaskID: t.ID, Needs: t.Needs}
	}
	return o.Client.CreateTree(ctx, in)
}

// treeStartMode parses a tree start mode (auto | manual; "" = omitted).
func treeStartMode(s string) (*db.StartMode, error) {
	m, err := startMode(s)
	if err != nil || m.inherit {
		return nil, fmt.Errorf("invalid startMode %q (want auto|manual)", s)
	}
	return m.mode, nil
}

// UpdateTreeArgs is update_tree.
type UpdateTreeArgs struct {
	ID        int64   `json:"id" jsonschema:"tree id"`
	Title     *string `json:"title,omitempty"`
	StartMode *string `json:"startMode,omitempty" jsonschema:"auto | manual"`
	Status    *string `json:"status,omitempty" jsonschema:"done (mark done: remaining tasks never start) | archived | open (reopen or unarchive); not while a member is launching, running or needs input"`
}

func (o *Ops) UpdateTree(ctx context.Context, a UpdateTreeArgs) (client.TreeDetail, error) {
	p := client.Patch{}
	if a.Title != nil {
		p["title"] = *a.Title
	}
	if a.StartMode != nil {
		mode, err := treeStartMode(*a.StartMode)
		if err != nil {
			return client.TreeDetail{}, err
		}
		if mode == nil {
			return client.TreeDetail{}, errors.New("startMode must be auto or manual")
		}
		p["startMode"] = *mode
	}
	if a.Status != nil {
		st := db.TreeStatus(*a.Status)
		if !st.Valid() {
			return client.TreeDetail{}, fmt.Errorf("invalid status %q (want open|done|archived)", *a.Status)
		}
		p["status"] = st
	}
	return o.Client.UpdateTree(ctx, a.ID, p)
}

// DeleteTree deletes an unstarted tree and every member task.
func (o *Ops) DeleteTree(ctx context.Context, a TreeIDArgs) (DeletedOut, error) {
	err := o.Client.DeleteTree(ctx, a.ID)
	return DeletedOut{Deleted: err == nil, ID: a.ID}, err
}

// AddToTreeArgs is add_to_tree.
type AddToTreeArgs struct {
	TreeID int64   `json:"treeId" jsonschema:"tree id"`
	TaskID int64   `json:"taskId" jsonschema:"task id (a task in another tree moves; its old edges go)"`
	Needs  []int64 `json:"needs,omitempty" jsonschema:"task ids (members of the tree) it runs after; omitted = a parallel root (a member keeps its needs; a task moving from another tree drops them)"`
}

func (o *Ops) AddToTree(ctx context.Context, a AddToTreeArgs) (client.TreeDetail, error) {
	var needs *[]int64
	if a.Needs != nil {
		needs = &a.Needs
	}
	return o.Client.AddToTree(ctx, a.TreeID, a.TaskID, needs)
}

// TaskIDArgs names one task.
type TaskIDArgs struct {
	TaskID int64 `json:"taskId" jsonschema:"task id"`
}

// RemovedOut confirms a removal from a tree.
type RemovedOut struct {
	Removed bool  `json:"removed"`
	TaskID  int64 `json:"taskId"`
	TreeID  int64 `json:"treeId"`
}

// RemoveFromTree splices the task out of its tree: its dependents take its
// needs. The task itself stays.
func (o *Ops) RemoveFromTree(ctx context.Context, a TaskIDArgs) (RemovedOut, error) {
	t, err := o.Client.Task(ctx, a.TaskID)
	if err != nil {
		return RemovedOut{}, err
	}
	if t.TreeID == nil {
		return RemovedOut{}, fmt.Errorf("task %d is not in a tree", a.TaskID)
	}
	tree := *t.TreeID
	err = o.Client.RemoveFromTree(ctx, tree, a.TaskID)
	return RemovedOut{Removed: err == nil, TaskID: a.TaskID, TreeID: tree}, err
}

// SetNeedsArgs is set_needs.
type SetNeedsArgs struct {
	TaskID int64   `json:"taskId" jsonschema:"task id (backlog)"`
	Needs  []int64 `json:"needs" jsonschema:"task ids it needs (replaces its needs; [] clears them); a task outside a tree joins the needs' tree"`
}

func (o *Ops) SetNeeds(ctx context.Context, a SetNeedsArgs) (client.Task, error) {
	return o.Client.SetNeeds(ctx, a.TaskID, a.Needs)
}

// AdoptSessionArgs is adopt_session.
type AdoptSessionArgs struct {
	Session         *db.SessionRef `json:"session,omitempty" jsonschema:"target session on its connection; omitted uses caller"`
	ProjectID       *int64         `json:"projectId,omitempty"`
	NewProjectTitle string         `json:"newProjectTitle,omitempty"`
	Title           string         `json:"title,omitempty" jsonschema:"task title; default: the session title"`
	InitialNote     string         `json:"initialNote,omitempty"`
	TreeID          *int64         `json:"treeId,omitempty" jsonschema:"also put the task in this tree (no needs)"`
	CallerArgs
}

// AdoptOut is adopt_session: the task and whether this call created it
// (false: a task already owned the session).
type AdoptOut struct {
	Task    client.Task `json:"task"`
	Created bool        `json:"created"`
}

// AdoptSession tracks a session as a task ("Track as task") so it can be a
// tree node. stage never starts it; follow-ups and stop work.
func (o *Ops) AdoptSession(ctx context.Context, a AdoptSessionArgs) (AdoptOut, error) {
	c, err := o.caller(ctx, a.CallerArgs)
	if err != nil {
		return AdoptOut{}, err
	}
	ref := a.Session
	if ref == nil {
		ref = c.Session
	}
	if ref != nil && (ref.ConnectionID <= 0 || strings.TrimSpace(ref.ExternalSessionID) == "") {
		return AdoptOut{}, errors.New("target session requires connectionId and externalSessionId")
	}
	if ref == nil {
		return AdoptOut{}, errors.New("session is required outside a harness session; pass the target session {connectionId, externalSessionId}")
	}
	t, created, err := o.Client.AdoptScoped(ctx, *ref, client.AdoptSession{
		CallerSession: c.Session, ProjectID: a.ProjectID, NewProjectTitle: a.NewProjectTitle, Title: strPtr(a.Title), InitialNote: strPtr(a.InitialNote), TreeID: a.TreeID,
	})
	return AdoptOut{Task: t, Created: created}, err
}

func (o *Ops) StartTree(ctx context.Context, a TreeIDArgs) (client.TreeStart, error) {
	return o.Client.StartTree(ctx, a.ID)
}
