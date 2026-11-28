package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// ProjectRename renames the OpenCode project behind a workspace row
// (project.update with a name). User-triggered, never automatic; the
// Stage project title is untouched.
const ProjectRename = "rename"

const (
	projectRenameUnsupported = "This OpenCode version cannot rename projects (project.update with a name)."
	globalRenameReason       = "OpenCode's global project can't be renamed."
)

func projectActions(contract serverContract) []execution.Action {
	availability := execution.Availability{Reason: projectRenameUnsupported}
	if contract.projectRename {
		availability = available
	}
	return []execution.Action{{
		ID: ProjectRename, Label: "Rename in OpenCode", Target: execution.TargetProject,
		Input: []execution.Field{{ID: "name", Label: "Name", Kind: execution.FieldText, Required: true,
			Help: "The project's name in OpenCode. The Stage project title doesn't change."}},
		Callers:      []execution.Caller{execution.CallerUser, execution.CallerAgent},
		Availability: availability,
	}}
}

// projectActionStates narrows the project actions to the workspace's
// OpenCode project.
func projectActionStates(actions []execution.Action, project string) []execution.Action {
	if project != globalProject {
		return actions
	}
	out := slices.Clone(actions)
	for i, a := range out {
		if a.Target == execution.TargetProject && a.ID == ProjectRename && a.Availability.Available {
			out[i].Availability = execution.Availability{Reason: globalRenameReason}
		}
	}
	return out
}

func (a *Adapter) actOnProject(ctx context.Context, r execution.ActionRequest) (execution.ActionResult, error) {
	if r.Project == nil || r.Project.ExternalProjectID == "" {
		return execution.ActionResult{}, &execution.Invalid{Reason: "An OpenCode project action needs an OpenCode project."}
	}
	if r.ID != ProjectRename {
		return execution.ActionResult{}, &execution.Invalid{Reason: "Unknown OpenCode action."}
	}
	project := r.Project.ExternalProjectID
	if project == globalProject {
		return execution.ActionResult{}, &execution.Invalid{Reason: globalRenameReason}
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(r.Input, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		return execution.ActionResult{}, &execution.Invalid{Reason: "Enter the project's new name."}
	}
	name := strings.TrimSpace(in.Name)
	contract, err := a.checkContract(ctx)
	if err != nil {
		return execution.ActionResult{}, err
	}
	if !contract.projectRename {
		return execution.ActionResult{}, fmt.Errorf("%w: %s", execution.ErrUnavailable, projectRenameUnsupported)
	}
	err = a.request(ctx, http.MethodPatch, "/api/project/"+url.PathEscape(project), map[string]string{"name": name}, nil)
	var status responseError
	switch {
	case err == nil:
	case missing(err):
		return execution.ActionResult{}, execution.ErrNotFound
	case errors.As(err, &status) && status.code == http.StatusBadRequest:
		reason := "OpenCode refused the new name."
		if m := strings.TrimSpace(status.message); m != "" {
			reason = "OpenCode refused the new name: " + m
		}
		return execution.ActionResult{}, &execution.Invalid{Reason: reason}
	case errors.As(err, &status):
		return execution.ActionResult{}, err
	default:
		// No answer: OpenCode may have renamed it. Its listing decides.
		if !a.projectNamed(ctx, project, name) {
			return execution.ActionResult{}, &execution.ActionUncertain{Cause: err}
		}
	}
	return execution.ActionResult{Message: "Renamed in OpenCode to " + name + "."}, nil
}

// projectNamed reports whether OpenCode lists project with name.
func (a *Adapter) projectNamed(ctx context.Context, project, name string) bool {
	projects, err := a.projects(ctx)
	return err == nil && slices.ContainsFunc(projects, func(p wireProject) bool { return p.ID == project && p.Name == name })
}
