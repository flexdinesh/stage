package api

import (
	"strconv"
	"time"

	"github.com/flexdinesh/stage/server/internal/db"
	t3sync "github.com/flexdinesh/stage/server/internal/sync"
	"github.com/flexdinesh/stage/server/internal/t3"
)

type artifactLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type taskJSON struct {
	ArtifactLinks []artifactLink `json:"artifactLinks"`
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
	Acceptances []db.Acceptance `json:"acceptances"`
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

func (s *server) task(env string, t db.Task) taskJSON {
	out := taskJSON{ArtifactLinks: []artifactLink{}, Task: t, T3WebURL: s.webURL(env, t.T3ThreadID), T3DeepLink: deepLink(env, t.T3ThreadID)}
	if t.ConnectionID != db.T3ConnectionID {
		out.T3WebURL = nil
		out.T3DeepLink = nil
	}
	for _, pr := range t.PullRequests {
		if pr.URL != "" {
			out.ArtifactLinks = append(out.ArtifactLinks, artifactLink{Label: pr.Repository + "#" + strconv.Itoa(pr.Number), URL: pr.URL})
		}
	}
	if t.ConnectionID == db.T3ConnectionID && t.T3ThreadID != nil {
		out.T3AppURL = t.ThreadLink
	}
	return out
}

func (s *server) tasks(env string, ts []db.Task) []taskJSON {
	out := make([]taskJSON, len(ts))
	for i, t := range ts {
		out[i] = s.task(env, t)
	}
	return out
}

func (s *server) thread(env string, t db.Thread) threadJSON {
	id := t.T3ThreadID
	return threadJSON{Thread: t, T3WebURL: s.webURL(env, &id), T3AppURL: t.Link, T3DeepLink: deepLink(env, &id)}
}

type catalogOptionJSON struct {
	ID      string           `json:"id"`
	Values  []t3.OptionValue `json:"values"`
	Default *t3.OptionValue  `json:"default"`
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
	RuntimeModes       []t3.RuntimeMode   `json:"runtimeModes"`
}

type catalogJSON struct {
	Providers []catalogProviderJSON `json:"providers"`
	FetchedAt *time.Time            `json:"fetchedAt"`
}

// catalogShape drops providers with constraints and applies fallbacks.
func catalogShape(snap *t3sync.CatalogSnapshot) catalogJSON {
	fetched := snap.FetchedAt
	out := catalogJSON{Providers: []catalogProviderJSON{}, FetchedAt: &fetched}
	for _, p := range snap.Catalog.Providers {
		if len(p.Constraints) > 0 {
			continue
		}
		cp := catalogProviderJSON{
			ProviderInstanceID: p.ProviderInstanceID, DriverKind: p.DriverKind, DisplayName: p.DisplayName,
			Models: []catalogModelJSON{}, RuntimeModes: p.RuntimeModes,
		}
		if cp.DisplayName == "" {
			cp.DisplayName = p.ProviderInstanceID
		}
		for _, m := range p.Models {
			cm := catalogModelJSON{ID: m.ID, Label: m.Label, Options: []catalogOptionJSON{}}
			if cm.Label == "" {
				cm.Label = m.ID
			}
			for _, o := range m.Options {
				vals := o.Values
				if vals == nil {
					vals = []t3.OptionValue{}
				}
				cm.Options = append(cm.Options, catalogOptionJSON{ID: o.ID, Values: vals, Default: o.Default})
			}
			cp.Models = append(cp.Models, cm)
		}
		out.Providers = append(out.Providers, cp)
	}
	return out
}
