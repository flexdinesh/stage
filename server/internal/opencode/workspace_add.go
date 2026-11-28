package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"

	"github.com/flexdinesh/stage/server/internal/execution"
)

var _ execution.WorkspaceAdder = (*Adapter)(nil)

const (
	// sourceFolder adds an existing folder on the OpenCode host.
	sourceFolder            = "folder"
	workspaceAddUnavailable = "This OpenCode version cannot add folders (location.get is missing)."
	folderNotFound          = "Folder not found on the OpenCode host."
	folderInGlobal          = "OpenCode files this folder under its global project. Give the repository a commit or a remote, then add it again."
)

// errLocationNotFound: GET /api/location answered 404 LocationNotFoundError.
var errLocationNotFound = errors.New(folderNotFound)

// folderSource declares the existing-folder source; ok reports location.get.
func folderSource(ok bool) execution.WorkspaceSource {
	s := execution.WorkspaceSource{
		ID: sourceFolder, Label: "Existing folder",
		Help: "OpenCode keeps the folder as a project. Git is optional; a folder inside a repository joins that repository's project.",
		Input: []execution.Field{{
			ID: "path", Label: "Path", Kind: execution.FieldPath, Required: true,
			Help: "Absolute path on the OpenCode host",
		}},
		Availability: available,
	}
	if !ok {
		s.Availability = execution.Availability{Reason: workspaceAddUnavailable}
	}
	return s
}

type wireLocation struct {
	Directory string
	Project   struct{ ID, Directory, Canonical string }
}

// AddWorkspace adds an existing folder. OpenCode v2 has no create-project
// endpoint: a location-scoped request for an existing directory resolves
// and persists its project, clearing an archived time (OpenCode 2.3.2,
// @opencode/Location with Project.resolve persist; undocumented, pinned by
// TestAddWorkspaceFolder). A folder inside a checkout or a linked worktree
// resolves to the repository's project, which Stage lists at its canonical
// checkout. Repeating the request changes nothing.
func (a *Adapter) AddWorkspace(ctx context.Context, r execution.WorkspaceAddRequest) (execution.WorkspaceAdded, error) {
	var out execution.WorkspaceAdded
	if r.Source != sourceFolder {
		return out, &execution.Invalid{Reason: fmt.Sprintf("OpenCode has no workspace source %q.", r.Source)}
	}
	var in struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(r.Input, &in); err != nil {
		return out, &execution.Invalid{Reason: "Enter the folder's absolute path on the OpenCode host."}
	}
	if err := invisible("Path", in.Path); err != nil {
		return out, &execution.Invalid{Reason: err.Error()}
	}
	if !path.IsAbs(in.Path) {
		return out, &execution.Invalid{Reason: "Enter an absolute path on the OpenCode host, like /home/me/project."}
	}
	dir := path.Clean(in.Path)
	contract, err := a.checkContract(ctx)
	if err != nil {
		return out, err
	}
	if !contract.location {
		return out, fmt.Errorf("%w: %s", execution.ErrUnavailable, workspaceAddUnavailable)
	}
	before, err := a.projects(ctx)
	if err != nil {
		return out, err
	}
	var loc wireLocation
	if err := a.get(ctx, "/api/location"+queryDirectory(dir), &loc); err != nil {
		var status responseError
		switch {
		case errors.Is(err, errLocationNotFound):
			return out, &execution.Invalid{Reason: folderNotFound}
		case errors.As(err, &status) && status.code == 400:
			return out, &execution.Invalid{Reason: "OpenCode refused this folder path."}
		case errors.As(err, &status):
			return out, fmt.Errorf("OpenCode could not open this folder: %w", err)
		default:
			// No answer: OpenCode may have saved the project.
			return out, &execution.OutcomeUncertain{Cause: err}
		}
	}
	p := loc.Project
	if p.ID == "" || p.Directory == "" || p.Canonical == "" {
		return out, &execution.OutcomeUncertain{Cause: errors.New("OpenCode returned an incomplete location")}
	}
	if p.ID == globalProject {
		return out, &execution.Invalid{Reason: folderInGlobal}
	}
	after, err := a.projects(ctx)
	if err != nil {
		return out, &execution.OutcomeUncertain{Cause: err}
	}
	i := slices.IndexFunc(after, func(w wireProject) bool { return w.ID == p.ID })
	if i < 0 || after[i].Time.Archived > 0 {
		return out, errors.New("OpenCode opened the folder but does not list its project. Check it in OpenCode.")
	}
	project := after[i]
	// The resolved checkout must be the project's canonical directory or a
	// saved worktree (another clone of the same repository, a linked one).
	if p.Directory != project.Canonical {
		trees, err := a.worktrees(ctx, p.ID)
		if err != nil {
			return out, &execution.OutcomeUncertain{Cause: err}
		}
		if !listed(trees, p.Directory) {
			return out, errors.New("OpenCode opened the folder but its project does not list the checkout. Check it in OpenCode.")
		}
	}
	prior := slices.IndexFunc(before, func(w wireProject) bool { return w.ID == p.ID })
	out.Workspace = a.workspace(project)
	name := out.Workspace.Title
	switch {
	case prior >= 0 && before[prior].Time.Archived <= 0:
		out.Existing = true
		out.Message = fmt.Sprintf("OpenCode already lists %s at %s.", name, project.Canonical)
	case prior >= 0:
		out.Message = fmt.Sprintf("OpenCode restored the archived project %s at %s.", name, project.Canonical)
	default:
		out.Message = fmt.Sprintf("OpenCode added %s at %s.", name, project.Canonical)
	}
	if dir != project.Canonical {
		out.Message += fmt.Sprintf(" OpenCode keeps %s under that project.", dir)
	}
	return out, nil
}
