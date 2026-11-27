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
			// Only a service on Stage's machine shares its filesystem.
			Local: a.local,
		}
		out = append(out, w)
	}
	return out, nil
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
	return execution.Capabilities{Operations: operations(), ModeLabel: "Agent", Options: launchOptions()}, nil
}

// optionWorktree is the worktree launch option: OpenCode creates the
// worktree (POST /api/worktree) with its selected strategy and default
// base, and the session runs there.
const optionWorktree = "worktree"

func launchOptions() []execution.Option {
	off := execution.BoolValue(false)
	return []execution.Option{{ID: optionWorktree, Kind: execution.OptionBoolean, Label: "OpenCode worktree", Values: []execution.OptionValue{execution.BoolValue(true), off}, Default: &off}}
}

// operations are OpenCode's, whatever the server.
func operations() map[execution.Op]execution.Availability {
	answer := execution.Availability{Reason: "Answer in OpenCode: open the session there."}
	return map[execution.Op]execution.Availability{
		execution.OpLaunch: available, execution.OpStop: available, execution.OpAdopt: available, execution.OpReply: available, execution.OpOpening: available, execution.OpAttention: available,
		execution.OpSessionSearch: {Reason: "Search is unavailable on OpenCode. Enter a session link or ID."},
		execution.OpImages:        {Reason: "Image attachment delivery has not been verified for this adapter."},
		execution.OpRespond:       answer,
		execution.OpSend:          answer,
	}
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
			opt := execution.Option{ID: "variant", Kind: execution.OptionChoice, Label: "Variant"}
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
	var launch, modelOpts []execution.ModelOption
	for _, o := range in.Config.Options {
		if o.ID == optionWorktree {
			launch = append(launch, o)
		} else {
			modelOpts = append(modelOpts, o)
		}
	}
	if err := (execution.Capabilities{Options: c.Options}).ValidateConfig(execution.Config{Options: launch}); err != nil {
		return nil, reject(err.Error())
	}
	if len(in.Images) > 0 {
		return nil, reject(c.Operations[execution.OpImages].Reason)
	}
	if in.Config.Permission != "" {
		return nil, reject("Use the selected OpenCode agent's permission policy.")
	}
	if ag := in.Config.Mode; ag != "" && !slices.ContainsFunc(c.Modes, func(v execution.Choice) bool { return v.ID == ag }) {
		return nil, reject("Choose an available primary OpenCode agent.")
	}
	if in.Config.Model == "" {
		if len(modelOpts) > 0 {
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
	for _, opt := range modelOpts {
		if ref.Variant != "" || opt.ID != "variant" || opt.Value.IsBool || opt.Value.Str == "" || !slices.ContainsFunc(c.Models[i].Options, func(v execution.Option) bool { return v.ID == opt.ID && slices.Contains(v.Values, opt.Value) }) {
			return nil, reject(fmt.Sprintf("OpenCode model option %q is unavailable in this workspace.", opt.ID))
		}
		ref.Variant = opt.Value.Str
	}
	return ref, nil
}
