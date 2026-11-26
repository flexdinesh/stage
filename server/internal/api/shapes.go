package api

import (
	"context"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

type artifactLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type taskJSON struct {
	Links         execution.Links `json:"links"`
	ArtifactLinks []artifactLink  `json:"artifactLinks"`
	db.Task
}

type taskDetailJSON struct {
	FinalReply  *execution.Reply `json:"finalReply,omitempty"`
	Usage       *execution.Usage `json:"usage,omitempty"` // the current session's, when its harness reports one
	Acceptances []db.Acceptance  `json:"acceptances"`
	taskJSON
	Events []db.TaskEvent `json:"events"`
	// Tree: the task's tree (null outside one).
	Tree *treeDetailJSON `json:"tree"`
	// Batch: the tasks the task's origin session created (null without one).
	Batch *createdTasksJSON `json:"batch"`
	// CurrentHandoffs: what the current launch attempt appended, one per
	// need (empty when none).
	CurrentHandoffs []db.Handoff `json:"currentHandoffs"`
}

// treeDetailJSON is db.TreeDetail with API task shapes.
type treeDetailJSON struct {
	db.Tree
	db.TreeTimes
	Tasks  []taskJSON     `json:"tasks"`
	Edges  []db.TreeEdge  `json:"edges"`
	Levels [][]int64      `json:"levels"`
	Events []db.TaskEvent `json:"events"`
}

func (s *server) treeDetail(t db.TreeDetail) treeDetailJSON {
	return treeDetailJSON{Tree: t.Tree, TreeTimes: t.TreeTimes, Tasks: s.tasks(t.Tasks), Edges: t.Edges,
		Levels: t.Levels, Events: t.Events}
}

// createdTasksJSON is db.CreatedTasks with API task shapes.
type createdTasksJSON struct {
	Trees       []batchTreeJSON `json:"trees"`
	Independent []taskJSON      `json:"independent"`
}

type batchTreeJSON struct {
	Tree  db.TreeRef `json:"tree"`
	Tasks []taskJSON `json:"tasks"`
}

func (s *server) createdTasks(c db.CreatedTasks) createdTasksJSON {
	out := createdTasksJSON{Trees: make([]batchTreeJSON, len(c.Trees)), Independent: s.tasks(c.Independent)}
	for i, t := range c.Trees {
		out.Trees[i] = batchTreeJSON{Tree: t.Tree, Tasks: s.tasks(t.Tasks)}
	}
	return out
}

func (s *server) task(t db.Task) taskJSON {
	if a, e := s.Store.Attempt(context.Background(), t.ID, t.LaunchAttempt); e == nil {
		if a.Session != nil {
			links := s.sessionLinks(context.Background(), a.Session.ConnectionID, &a.Session.ExternalSessionID)
			// No destination and no stored observation: the resume command
			// the attempt kept.
			if links == (execution.Links{}) && a.Session.Links != nil {
				links = *a.Session.Links
			}
			a.Session.Links = &links
		}
		t.Launch = &a
	}
	out := taskJSON{ArtifactLinks: []artifactLink{}, Task: t}
	if t.Session != nil {
		out.Links = s.sessionLinks(context.Background(), t.ConnectionID, &t.Session.ExternalSessionID)
	}
	return out
}

// sessionLinks are the session's links: its adapter's destinations (an
// in-process adapter's resume command too), else its stored observation's
// (a remote machine's adapter resolves none without a call).
func (s *server) sessionLinks(ctx context.Context, id int64, external *string) execution.Links {
	if external == nil {
		return execution.Links{}
	}
	ref := db.SessionRef{ConnectionID: id, ExternalSessionID: *external}
	if a, err := s.Sync.Adapter(id); err == nil {
		links := a.Destinations(ctx, ref)
		if links.Web != "" || links.Native != "" || links.Command != "" {
			return links
		}
	}
	if o, err := s.Store.Observation(ctx, ref); err == nil {
		return o.Links
	}
	return execution.Links{}
}

func (s *server) tasks(ts []db.Task) []taskJSON {
	out := make([]taskJSON, len(ts))
	for i, t := range ts {
		out[i] = s.task(t)
	}
	return out
}
