package opencode

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/flexdinesh/stage/server/internal/gitmeta"
)

func queryDirectory(dir string) string {
	return "?" + url.Values{"location[directory]": {dir}}.Encode()
}
func millis(i int64) *time.Time {
	if i <= 0 {
		return nil
	}
	t := time.UnixMilli(i).UTC()
	return &t
}
// globalProject is OpenCode's catch-all project for directories outside any
// repository it can identify.
const globalProject = "global"

// title names an unnamed project after its checkout: the last path segment,
// or parent/segment for a worktree-style layout (octo/main).
func title(name, canonical string) string {
	if name != "" {
		return name
	}
	clean := path.Clean(canonical)
	base := path.Base(clean)
	if base == "/" || base == "." {
		return canonical
	}
	switch base {
	case "main", "master", "trunk":
		if parent := path.Base(path.Dir(clean)); parent != "/" && parent != "." {
			return parent + "/" + base
		}
	}
	return base
}

// Workspaces lists OpenCode's project inventory. A project is importable
// when it is a version-controlled checkout OpenCode has not archived;
// plain directories (home, config, scratch) stay manual-bind only.
func (a *Adapter) Workspaces(ctx context.Context) ([]execution.WorkspaceInfo, error) {
	var projects []struct {
		ID, Name, Canonical, Vcs string
		Time                     struct{ Created, Updated, Active, Archived int64 }
	}
	if e := a.get(ctx, "/api/project?archived=true", &projects); e != nil {
		return nil, e
	}
	if projects == nil {
		return nil, errors.New("invalid OpenCode project inventory")
	}
	out := make([]execution.WorkspaceInfo, 0, len(projects))
	for _, p := range projects {
		if p.ID == "" || p.Canonical == "" || p.Time.Created <= 0 || p.Time.Updated <= 0 {
			return nil, errors.New("incomplete OpenCode project inventory")
		}
		// A project binding pins the canonical checkout. Created worktrees
		// belong to its separate per-project inventory.
		w := execution.WorkspaceInfo{
			ID: p.ID, Title: title(p.Name, p.Canonical), WorkspaceRoot: p.Canonical,
			ArchivedAt: millis(p.Time.Archived), CreatedAt: *millis(p.Time.Created), UpdatedAt: *millis(p.Time.Updated),
			ActiveAt:   millis(p.Time.Active),
			Importable: p.Vcs != "" && p.Time.Archived <= 0 && p.ID != globalProject,
		}
		// Only a service on Stage's machine shares its filesystem.
		if a.local && p.Vcs == "git" {
			w.Repo = gitmeta.OriginRepo(p.Canonical)
		}
		out = append(out, w)
	}
	return out, nil
}
func (a *Adapter) RegisterWorkspace(context.Context, string, string) (execution.WorkspaceInfo, error) {
	return execution.WorkspaceInfo{}, errors.New("open the directory in OpenCode, then refresh Stage workspace discovery")
}

func (a *Adapter) verifyLocation(ctx context.Context, project, dir string) error {
	var loc struct {
		Directory string
		Project   struct{ ID, Directory, Canonical string }
	}
	if dir == "" || project == "" {
		return errors.New("choose a bound OpenCode project and directory")
	}
	if e := a.get(ctx, "/api/location"+queryDirectory(dir), &loc); e != nil {
		return e
	}
	if loc.Directory != dir || loc.Project.ID != project || loc.Project.Directory != dir {
		return errors.New("OpenCode resolved a different checkout or project. Verify its binding.")
	}
	return nil
}
func (a *Adapter) worktrees(ctx context.Context, project string) ([]location, error) {
	var trees []location
	if e := a.get(ctx, "/api/worktree?projectID="+url.QueryEscape(project), &trees); e != nil {
		return nil, e
	}
	if trees == nil || slices.ContainsFunc(trees, func(w location) bool { return w.Directory == "" }) {
		return nil, errors.New("invalid OpenCode worktree inventory")
	}
	return trees, nil
}
func (a *Adapter) verifyWorktree(ctx context.Context, project, dir string) error {
	trees, err := a.worktrees(ctx, project)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(trees, func(w location) bool { return w.Directory == dir }) {
		return errors.New("created workspace is missing from the OpenCode project inventory")
	}
	return a.verifyLocation(ctx, project, dir)
}

var available = execution.Availability{Available: true}

func choice(id, label string) execution.Choice {
	return execution.Choice{ID: id, Label: label, Availability: available}
}
func (a *Adapter) Features(ctx context.Context) (execution.Capabilities, error) {
	if _, err := a.check(ctx); err != nil {
		return execution.Capabilities{}, err
	}
	return execution.Capabilities{SessionEnvironment: "OPENCODE_SESSION_ID", Operations: map[string]execution.Availability{
		"launch": available, "stop": available, "adopt": available, "reply": available, "opening": available, "attention": available,
		"predecessorBase":   {Reason: "OpenCode selects the worktree strategy and base. Choose its default base."},
		"images":            {Reason: "Image attachment delivery has not been verified for this adapter."},
		"registerWorkspace": {Reason: "Open the workspace in OpenCode and refresh discovery."},
	}, Workspaces: []execution.Choice{choice("root", "Bound checkout"), choice("worktree", "OpenCode-managed worktree")}}, nil
}

type wireModel struct {
	ID         string `json:"id"`
	ProviderID string `json:"providerID"`
	Variant    string `json:"variant,omitempty"`
}

func (a *Adapter) FeaturesForWorkspace(ctx context.Context, project, dir string) (execution.Capabilities, error) {
	c, err := a.Features(ctx)
	if err != nil {
		return c, err
	}
	if e := a.verifyLocation(ctx, project, dir); e != nil {
		return c, e
	}
	var models envelope[[]struct {
		ID, ProviderID, Name string
		Enabled              bool
		Variants             []struct{ ID string }
	}]
	var agents envelope[[]struct {
		ID, Name, Mode string
		Hidden         bool
	}]
	if e := a.get(ctx, "/api/model"+queryDirectory(dir), &models); e != nil {
		return c, e
	}
	if e := a.get(ctx, "/api/agent"+queryDirectory(dir), &agents); e != nil {
		return c, e
	}
	if models.Location.Directory != dir || agents.Location.Directory != dir || models.Data == nil || agents.Data == nil {
		return c, errors.New("OpenCode returned an incomplete catalog or different location")
	}
	for _, m := range models.Data {
		if !m.Enabled {
			continue
		}
		if m.ID == "" || m.ProviderID == "" {
			return c, errors.New("invalid OpenCode model identity")
		}
		model := execution.Model{Choice: choice(m.ProviderID+"/"+m.ID, m.Name), Group: m.ProviderID}
		if len(m.Variants) > 0 {
			opt := execution.Option{ID: "variant"}
			for _, v := range m.Variants {
				opt.Values = append(opt.Values, execution.StringValue(v.ID))
			}
			model.Options = []execution.Option{opt}
		}
		c.Models = append(c.Models, model)
	}
	for _, ag := range agents.Data {
		if !ag.Hidden && (ag.Mode == "primary" || ag.Mode == "all") {
			c.Modes = append(c.Modes, choice(ag.ID, ag.Name))
		}
	}
	return c, nil
}
func selection(c execution.Capabilities, in execution.Intent) (*wireModel, error) {
	reject := func(s string) error { return &execution.Rejected{Reason: s} }
	if !slices.Contains([]string{"root", "worktree"}, in.Workspace.Kind) || in.Workspace.Base != "" || in.Workspace.Branch != "" || in.Workspace.FromRemote {
		return nil, reject("Choose the bound checkout or an OpenCode worktree with its default base and branch.")
	}
	if len(in.Images) > 0 {
		return nil, reject(c.Operations["images"].Reason)
	}
	if in.Config.Permission != "" {
		return nil, reject("Use the selected OpenCode agent's permission policy.")
	}
	if ag := in.Config.Mode; ag != "" && !slices.ContainsFunc(c.Modes, func(v execution.Choice) bool { return v.ID == ag }) {
		return nil, reject("Choose an available primary OpenCode agent.")
	}
	if in.Config.Model == "" {
		if len(in.Config.Options) > 0 {
			return nil, reject("Select a model before choosing its variant.")
		}
		return nil, nil
	}
	i := slices.IndexFunc(c.Models, func(m execution.Model) bool { return m.ID == in.Config.Model })
	if i < 0 {
		return nil, reject("Choose a model available in this OpenCode workspace.")
	}
	provider, id, _ := strings.Cut(c.Models[i].ID, "/")
	ref := &wireModel{ID: id, ProviderID: provider}
	for _, opt := range in.Config.Options {
		if ref.Variant != "" || opt.ID != "variant" || opt.Value.IsBool || opt.Value.Str == "" || !slices.ContainsFunc(c.Models[i].Options, func(v execution.Option) bool { return v.ID == opt.ID && slices.Contains(v.Values, opt.Value) }) {
			return nil, reject(fmt.Sprintf("OpenCode model option %q is unavailable in this workspace.", opt.ID))
		}
		ref.Variant = opt.Value.Str
	}
	return ref, nil
}
