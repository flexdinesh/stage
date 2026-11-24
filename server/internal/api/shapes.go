package api

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
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

type threadJSON struct {
	db.Thread
}

type taskDetailJSON struct {
	FinalReply  *execution.Reply `json:"finalReply,omitempty"`
	Acceptances []db.Acceptance  `json:"acceptances"`
	taskJSON
	Events []db.TaskEvent `json:"events"`
	Thread *threadJSON    `json:"thread"`
	// Tree: the task's tree (null outside one).
	Tree *treeDetailJSON `json:"tree"`
	// Batch: the tasks the task's origin session created (null without one).
	Batch *createdTasksJSON `json:"batch"`
	// CurrentHandoffs: what the current launch attempt appended, one per
	// need (empty when none).
	CurrentHandoffs []db.Handoff `json:"currentHandoffs"`
	// CurrentLaunchBase: the worktree base the current launch attempt used
	// (workspace.base predecessor; null when none).
	CurrentLaunchBase *db.LaunchBase `json:"currentLaunchBase"`
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

func (s *server) treeDetail(env string, t db.TreeDetail) treeDetailJSON {
	return treeDetailJSON{Tree: t.Tree, TreeTimes: t.TreeTimes, Tasks: s.tasks(env, t.Tasks), Edges: t.Edges,
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

func (s *server) createdTasks(env string, c db.CreatedTasks) createdTasksJSON {
	out := createdTasksJSON{Trees: make([]batchTreeJSON, len(c.Trees)), Independent: s.tasks(env, c.Independent)}
	for i, t := range c.Trees {
		out.Trees[i] = batchTreeJSON{Tree: t.Tree, Tasks: s.tasks(env, t.Tasks)}
	}
	return out
}

// webURL = <T3 browser URL>/<environmentId>/<threadId> (path-escaped: provider
// thread ids contain ':' and '%').
func (s *server) webURL(envID string, threadID *string) *string {
	if threadID == nil {
		return nil
	}
	u := t3.WebThreadURL(s.Setup.Endpoint().PublicURL, envID, *threadID)
	if u == "" {
		return nil
	}
	return &u
}

// deepLink = t3code://threads/<environmentId>/<threadId>.
func deepLink(envID string, threadID *string) *string {
	if threadID == nil {
		return nil
	}
	u := t3.AppThreadURL(envID, *threadID)
	if u == "" {
		return nil
	}
	return &u
}

// executionConfig is a row's canonical config: its own, else the one its
// harness decodes from the legacy model columns (nil when neither).
func (s *server) executionConfig(kind string, config *execution.Config, legacy func() json.RawMessage) *execution.Config {
	if config != nil {
		return config
	}
	d, ok := s.registry().Lookup(kind)
	if !ok || d.LegacyConfig == nil {
		return nil
	}
	c, err := d.LegacyConfig(legacy())
	if err != nil {
		return nil
	}
	return &c
}

func (s *server) task(env string, t db.Task) taskJSON {
	t.ExecutionConfig = s.executionConfig(t.Harness, t.ExecutionConfig, t.LegacyModel)
	if a, e := s.Store.Attempt(context.Background(), t.ID, t.LaunchAttempt); e == nil {
		if a.Session != nil {
			links := s.sessionLinks(context.Background(), a.Session.ConnectionID, &a.Session.ExternalSessionID)
			a.Session.Links = &links
		}
		t.Launch = &a
	}
	out := taskJSON{ArtifactLinks: []artifactLink{}, Task: t}
	if t.Session != nil {
		out.Links = s.sessionLinks(context.Background(), t.ConnectionID, &t.Session.ExternalSessionID)
	}
	for _, pr := range t.PullRequests {
		if pr.URL != "" {
			out.ArtifactLinks = append(out.ArtifactLinks, artifactLink{Label: pr.Repository + "#" + strconv.Itoa(pr.Number), URL: pr.URL})
		}
	}
	return out
}

func (s *server) sessionLinks(ctx context.Context, id int64, external *string) execution.Links {
	if external == nil {
		return execution.Links{}
	}
	ref := db.SessionRef{ConnectionID: id, ExternalSessionID: *external}
	if a, err := s.Sync.Adapter(id); err == nil {
		links := a.Destinations(ctx, ref)
		if links.Web != "" || links.Native != "" {
			return links
		}
	}
	if o, err := s.Store.Observation(ctx, ref); err == nil {
		return o.Links
	}
	return execution.Links{}
}
func (s *server) tasks(env string, ts []db.Task) []taskJSON {
	out := make([]taskJSON, len(ts))
	for i, t := range ts {
		out[i] = s.task(env, t)
	}
	return out
}

func (s *server) thread(env string, t db.Thread) threadJSON {
	// Preserve the retained T3 response vocabulary at this compatibility edge.
	switch t.Status {
	case "done":
		t.Status = "completed"
	case "needs_input":
		t.Status = "waiting"
		t.Settled = false
	case "running":
		t.Settled = false
	}
	return threadJSON{Thread: t}
}

type catalogOptionJSON struct {
	ID      string                  `json:"id"`
	Values  []execution.OptionValue `json:"values"`
	Default *execution.OptionValue  `json:"default"`
}

type catalogModelJSON struct {
	ID      string              `json:"id"`
	Label   string              `json:"label"`
	Options []catalogOptionJSON `json:"options"`
}

type catalogProviderJSON struct {
	ProviderInstanceID string             `json:"providerInstanceId"`
	DriverKind         string             `json:"driverKind"`
	DisplayName        string             `json:"displayName"`
	Models             []catalogModelJSON `json:"models"`
	RuntimeModes       []string           `json:"runtimeModes"`
}

type catalogJSON struct {
	Providers []catalogProviderJSON `json:"providers"`
	FetchedAt *time.Time            `json:"fetchedAt"`
}

// catalogShape drops providers with constraints and applies fallbacks.
func catalogShape(snap *t3sync.CatalogSnapshot) catalogJSON {
	out := catalogJSON{Providers: []catalogProviderJSON{}, FetchedAt: &snap.FetchedAt}
	for _, m := range snap.Catalog.Models {
		if !m.Available {
			continue
		}
		provider, name, err := t3.DecodeModelKey(m.ID)
		if err != nil {
			continue
		}
		index := -1
		for i, p := range out.Providers {
			if p.ProviderInstanceID == provider {
				index = i
				break
			}
		}
		if index < 0 {
			out.Providers = append(out.Providers, catalogProviderJSON{ProviderInstanceID: provider, DriverKind: provider, DisplayName: m.Group, Models: []catalogModelJSON{}, RuntimeModes: nil})
			index = len(out.Providers) - 1
		}
		model := catalogModelJSON{ID: name, Label: m.Label, Options: []catalogOptionJSON{}}
		for _, o := range m.Options {
			values := o.Values
			if values == nil {
				values = []execution.OptionValue{}
			}
			model.Options = append(model.Options, catalogOptionJSON{ID: o.ID, Values: values, Default: o.Default})
		}
		out.Providers[index].Models = append(out.Providers[index].Models, model)
		for _, mode := range m.Permissions {
			found := false
			for _, v := range out.Providers[index].RuntimeModes {
				if v == mode.ID {
					found = true
				}
			}
			if !found {
				out.Providers[index].RuntimeModes = append(out.Providers[index].RuntimeModes, mode.ID)
			}
		}
	}
	return out
}
