package agenttools

import (
	"context"
	"errors"

	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
)

// ---- project lifecycle and workspace group membership ----

// CreateProjectArgs is create_project.
type CreateProjectArgs struct {
	Title string `json:"title" jsonschema:"the new project group's title"`
}

func (o *Ops) CreateProject(ctx context.Context, a CreateProjectArgs) (db.ProjectSummary, error) {
	return o.Client.CreateProject(ctx, a.Title)
}

// UpdateProjectArgs is update_project (nil = unchanged).
type UpdateProjectArgs struct {
	ID       int64   `json:"id" jsonschema:"Stage project id (a merged id resolves)"`
	Title    *string `json:"title,omitempty" jsonschema:"rename; omitted keeps the title"`
	Archived *bool   `json:"archived,omitempty" jsonschema:"true archives, false unarchives; omitted keeps it"`
}

func (o *Ops) UpdateProject(ctx context.Context, a UpdateProjectArgs) (db.ProjectSummary, error) {
	p := client.Patch{}
	if a.Title != nil {
		p["title"] = *a.Title
	}
	if a.Archived != nil {
		p["archived"] = *a.Archived
	}
	if len(p) == 0 {
		return db.ProjectSummary{}, errors.New("nothing to change: pass title or archived")
	}
	return o.Client.UpdateProject(ctx, a.ID, p)
}

// ProjectEventsOut is get_project_events.
type ProjectEventsOut struct {
	Events []db.ProjectEvent `json:"events"`
}

func (o *Ops) GetProjectEvents(ctx context.Context, a ProjectIDArgs) (ProjectEventsOut, error) {
	es, err := o.Client.ProjectEvents(ctx, a.ID)
	if es == nil {
		es = []db.ProjectEvent{}
	}
	return ProjectEventsOut{Events: es}, err
}

// MembershipArgs names one exact workspace and where it goes.
type MembershipArgs struct {
	ConnectionID    int64  `json:"connectionId" jsonschema:"the workspace's connection from list_projects destinations"`
	WorkspaceID     string `json:"workspaceId" jsonschema:"the exact workspace from list_projects destinations"`
	TargetProjectID *int64 `json:"targetProjectId,omitempty" jsonschema:"the project group to move it into; omitted separates it into a new group titled after the workspace"`
}

func (a MembershipArgs) input(token string) db.GroupEditInput {
	return db.GroupEditInput{ConnectionID: a.ConnectionID, WorkspaceID: a.WorkspaceID, TargetProjectID: a.TargetProjectID, PreviewToken: token}
}

func (o *Ops) PreviewMembership(ctx context.Context, a MembershipArgs) (db.GroupEdit, error) {
	return o.Client.EditProjectGroup(ctx, a.input(""))
}

// SetMembershipArgs is set_membership.
type SetMembershipArgs struct {
	MembershipArgs
	PreviewToken string `json:"previewToken" jsonschema:"previewToken from preview_membership with the same inputs; a changed preview fails, so preview again"`
}

func (o *Ops) SetMembership(ctx context.Context, a SetMembershipArgs) (db.GroupEdit, error) {
	if a.PreviewToken == "" {
		return db.GroupEdit{}, errors.New("previewToken is required: call preview_membership first")
	}
	return o.Client.EditProjectGroup(ctx, a.input(a.PreviewToken))
}
