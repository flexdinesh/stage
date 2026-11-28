package t3

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/flexdinesh/stage/server/internal/execution"
)

// Project actions: T3's own project management, relayed through
// t3_project_update and t3_project_delete on the T3 project behind a
// workspace row. Each is user-triggered, never automatic. Stage group
// titles are untouched: a rename changes T3's title only.
const (
	ProjectRename  = "rename"
	ProjectArchive = "archive"
	ProjectRestore = "restore"
	ProjectRemove  = "remove"
)

const (
	alreadyArchived = "Already archived in T3."
	notArchived     = "Not archived in T3."
	// notEmptyReason replaces T3's refusal, which names a force flag
	// Stage never sends.
	notEmptyReason = "T3 can't remove a project with threads. Archive it instead."
)

type projectInput struct {
	Title string `json:"title"`
}

func projectActions() []execution.Action {
	both := []execution.Caller{execution.CallerUser, execution.CallerAgent}
	yes := execution.Availability{Available: true}
	return []execution.Action{
		{
			ID: ProjectRename, Label: "Rename in T3", Target: execution.TargetProject, Callers: both, Availability: yes,
			Input: []execution.Field{{ID: "title", Label: "Title", Kind: execution.FieldText, Required: true,
				Help: "The project's title in T3. The Stage project title doesn't change."}},
		},
		{
			ID: ProjectArchive, Label: "Archive in T3", Target: execution.TargetProject, Callers: both, Availability: yes,
			Confirm: "Archive this project in T3? T3 keeps its threads and files. Restore it here or in T3.",
		},
		{ID: ProjectRestore, Label: "Restore in T3", Target: execution.TargetProject, Callers: both, Availability: yes},
		{
			ID: ProjectRemove, Label: "Remove from T3", Target: execution.TargetProject, Destructive: true,
			Callers: []execution.Caller{execution.CallerUser}, Availability: yes,
			Confirm: "Remove this project from T3? T3 keeps the files on disk. A project with threads can't be removed; archive it instead.",
		},
	}
}

// FeaturesForWorkspace narrows the project actions to the T3 project's
// archive state. It never fails on the project listing: launches use these
// capabilities too, and only the actions depend on it.
func (a *Adapter) FeaturesForWorkspace(ctx context.Context, projectID, _ string) (execution.Capabilities, error) {
	c, err := a.Features(ctx)
	if err != nil {
		return c, err
	}
	reasons := map[string]string{}
	p, err := a.project(ctx, projectID)
	switch {
	case err != nil:
		reason := "Could not read T3's projects. Try again."
		if errors.Is(err, ErrNotFound) {
			reason = "T3 no longer lists this project. Refresh workspaces."
		}
		for _, id := range []string{ProjectRename, ProjectArchive, ProjectRestore, ProjectRemove} {
			reasons[id] = reason
		}
	case p.ArchivedAt != nil:
		reasons[ProjectArchive] = alreadyArchived
	default:
		reasons[ProjectRestore] = notArchived
	}
	c.Actions = slices.Clone(c.Actions)
	for i, action := range c.Actions {
		if reason := reasons[action.ID]; action.Target == execution.TargetProject && reason != "" {
			c.Actions[i].Availability = execution.Availability{Reason: reason}
		}
	}
	return c, nil
}

// project finds a live T3 project by ID. ErrNotFound when T3 does not
// list it.
func (a *Adapter) project(ctx context.Context, id string) (Project, error) {
	projects, err := a.Client.ListProjects(ctx)
	if err != nil {
		return Project{}, err
	}
	i := slices.IndexFunc(projects, func(p Project) bool { return p.ID == id && p.DeletedAt == nil })
	if i < 0 || id == "" {
		return Project{}, ErrNotFound
	}
	return projects[i], nil
}

func (a *Adapter) actOnProject(ctx context.Context, r execution.ActionRequest) (execution.ActionResult, error) {
	if r.Project == nil || r.Project.ExternalProjectID == "" {
		return execution.ActionResult{}, &execution.Invalid{Reason: "A T3 project action needs a T3 project."}
	}
	id := r.Project.ExternalProjectID
	var (
		update ProjectUpdate
		done   string
	)
	switch r.ID {
	case ProjectRename:
		var in projectInput
		if err := json.Unmarshal(r.Input, &in); err != nil || strings.TrimSpace(in.Title) == "" {
			return execution.ActionResult{}, &execution.Invalid{Reason: "Enter the project's new title."}
		}
		title := strings.TrimSpace(in.Title)
		update.Title, done = &title, "Renamed in T3 to "+title+"."
	case ProjectArchive, ProjectRestore:
		archived := r.ID == ProjectArchive
		update.Archived, done = &archived, "Restored in T3."
		if archived {
			done = "Archived in T3."
		}
	case ProjectRemove:
		return a.removeProject(ctx, id)
	default:
		return execution.ActionResult{}, &execution.Invalid{Reason: "Unknown T3 action."}
	}
	err := a.Client.UpdateProject(ctx, id, update)
	if IsTransportError(err) {
		// The reply was lost: T3 may have applied it. Its listing decides.
		if p, lerr := a.project(ctx, id); lerr == nil && updated(p, update) {
			err = nil
		} else {
			return execution.ActionResult{}, &execution.ActionUncertain{Cause: err}
		}
	}
	if err != nil {
		return execution.ActionResult{}, projectError(err)
	}
	return execution.ActionResult{Message: done}, nil
}

func (a *Adapter) removeProject(ctx context.Context, id string) (execution.ActionResult, error) {
	err := a.Client.DeleteProject(ctx, id)
	if IsTransportError(err) {
		// Gone from T3's listing: the lost call removed it.
		if _, lerr := a.project(ctx, id); errors.Is(lerr, ErrNotFound) {
			err = nil
		} else {
			return execution.ActionResult{}, &execution.ActionUncertain{Cause: err}
		}
	}
	if err != nil {
		return execution.ActionResult{}, projectError(err)
	}
	return execution.ActionResult{Message: "Removed from T3. The files stay on disk."}, nil
}

// updated reports whether p already carries the update.
func updated(p Project, u ProjectUpdate) bool {
	if u.Title != nil && p.Title != *u.Title {
		return false
	}
	return u.Archived == nil || *u.Archived == (p.ArchivedAt != nil)
}

// projectError relays T3's refusal: nothing changed in T3.
func projectError(err error) error {
	var te *ToolError
	switch {
	case errors.Is(err, ErrNotFound):
		return execution.ErrNotFound
	case !errors.As(err, &te) || strings.TrimSpace(te.Message) == "":
		return err
	case te.Message == msgProjectNotFound:
		return execution.ErrNotFound
	case te.Message == msgProjectNotEmpty:
		return &execution.Invalid{Reason: notEmptyReason}
	}
	return &execution.Invalid{Reason: te.Message}
}
