package api

import (
	"context"
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
	T3WebURL *string `json:"t3WebUrl"`
	T3AppURL *string `json:"t3AppUrl"`
	// T3DeepLink opens the thread in the T3 desktop/mobile app.
	T3DeepLink *string `json:"t3DeepLink"`
}

type threadJSON struct {
	db.Thread
	T3WebURL *string `json:"t3WebUrl"`
	T3AppURL *string `json:"t3AppUrl"`
	// T3DeepLink opens the thread in the T3 desktop/mobile app.
	T3DeepLink *string `json:"t3DeepLink"`
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

// webURL = T3_PUBLIC_URL/<environmentId>/<threadId> (path-escaped: provider
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

// Preserve the legacy T3 response fields for older clients. The canonical
// selection retains the distinction between a harness default and an override.
func t3Defaults(config *execution.Config, provider, model *string, options []execution.ModelOption, permission, mode string) db.ModelDefaults {
	d := db.ModelDefaults{ExecutionConfig: config, ProviderInstanceID: provider, Model: model, ModelOptions: options, RuntimeMode: permission, InteractionMode: mode}
	if config == nil {
		return d
	}
	d.ProviderInstanceID, d.Model = nil, nil
	if p, m, err := t3.DecodeModelKey(config.Model); err == nil {
		d.ProviderInstanceID, d.Model = &p, &m
	}
	d.ModelOptions = append([]execution.ModelOption{}, config.Options...)
	if config.Permission != "" {
		d.RuntimeMode = config.Permission
	}
	// An empty mode is the harness default: launch sends no interaction mode,
	// so never echo a stale legacy column (e.g. "plan" from before the config).
	d.InteractionMode = string(t3.InteractionDefault)
	if config.Mode != "" {
		d.InteractionMode = config.Mode
	}
	return d
}

func (s *server) task(env string, t db.Task) taskJSON {
	if t.Harness == db.HarnessT3 && t.ExecutionConfig != nil {
		d := t3Defaults(t.ExecutionConfig, t.ProviderInstanceID, t.Model, t.ModelOptions, t.RuntimeMode, t.InteractionMode)
		t.ProviderInstanceID, t.Model, t.ModelOptions, t.RuntimeMode, t.InteractionMode = d.ProviderInstanceID, d.Model, d.ModelOptions, d.RuntimeMode, d.InteractionMode
	}
	if a, e := s.Store.Attempt(context.Background(), t.ID, t.LaunchAttempt); e == nil {
		t.Launch = &a
	}
	out := taskJSON{ArtifactLinks: []artifactLink{}, Task: t}
	// T3 links are built from the active environment: a task on a replaced
	// T3 connection gets none.
	if t.ConnectionID == s.Store.T3ID() && t.T3ThreadID != nil {
		out.T3WebURL = s.webURL(env, t.T3ThreadID)
		out.T3DeepLink = deepLink(env, t.T3ThreadID)
		out.T3AppURL = t.ThreadLink
	}
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
	id := t.T3ThreadID
	return threadJSON{Thread: t, T3WebURL: s.webURL(env, &id), T3AppURL: t.Link, T3DeepLink: deepLink(env, &id)}
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
