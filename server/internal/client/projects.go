package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/flexdinesh/stage/server/internal/db"
)

// ProjectList is the GET /api/projects response.
type ProjectList struct {
	Projects     []db.Project       `json:"projects"`
	Imports      []db.ImportSummary `json:"imports"`
	Destinations []db.Destination   `json:"destinations,omitempty"`
}

type ProjectSummaries struct {
	Projects     []db.ProjectSummary      `json:"projects"`
	Groups       []db.ProjectGroupSummary `json:"groups"`
	Destinations []db.Destination         `json:"destinations"`
}

// ProjectSummaries reads the selection projection without refreshing a harness.
func (c *Client) ProjectSummaries(ctx context.Context) (ProjectSummaries, error) {
	return c.ProjectSummariesArchived(ctx, "")
}

// ProjectSummariesArchived is ProjectSummaries filtered by archived
// (false|true|all; "" = server default, active only).
func (c *Client) ProjectSummariesArchived(ctx context.Context, archived string) (ProjectSummaries, error) {
	q := url.Values{"view": {"destinations"}}
	if archived != "" {
		q.Set("archived", archived)
	}
	var out ProjectSummaries
	err := c.do(ctx, http.MethodGet, withQuery("/api/projects", q), nil, &out)
	return out, err
}

func projectPath(id int64) string { return fmt.Sprintf("/api/projects/%d", id) }

// ListProjects reads projects and their recent discovery import summaries.
func (c *Client) ListProjects(ctx context.Context) (ProjectList, error) {
	var out ProjectList
	err := c.do(ctx, http.MethodGet, "/api/projects", nil, &out)
	return out, err
}

// Project looks up a project by ID or merged ID.
func (c *Client) Project(ctx context.Context, id int64) (db.Project, error) {
	var out db.Project
	err := c.do(ctx, http.MethodGet, projectPath(id), nil, &out)
	return out, err
}

// ProjectSummary reads a project's selection projection (the shape agents
// get, whatever the principal) by ID or merged ID.
func (c *Client) ProjectSummary(ctx context.Context, id int64) (db.ProjectSummary, error) {
	var out db.ProjectSummary
	err := c.do(ctx, http.MethodGet, withQuery(projectPath(id), url.Values{"view": {"destinations"}}), nil, &out)
	return out, err
}

// ProjectGroups lists logical project groups' summaries (archived:
// false|true|all; "" = server default).
func (c *Client) ProjectGroups(ctx context.Context, archived string) ([]db.ProjectGroupSummary, error) {
	q := url.Values{"view": {"destinations"}}
	if archived != "" {
		q.Set("archived", archived)
	}
	var out struct {
		Groups []db.ProjectGroupSummary `json:"groups"`
	}
	err := c.do(ctx, http.MethodGet, withQuery("/api/project-groups", q), nil, &out)
	return out.Groups, err
}

// ProjectGroup reads a project group's summary by project ID or legacy alias.
func (c *Client) ProjectGroup(ctx context.Context, id int64) (db.ProjectGroupSummary, error) {
	var out db.ProjectGroupSummary
	err := c.do(ctx, http.MethodGet, withQuery(fmt.Sprintf("/api/project-groups/%d", id), url.Values{"view": {"destinations"}}), nil, &out)
	return out, err
}

// Workspaces refreshes a connection's inventory and returns its available rows.
func (c *Client) Workspaces(ctx context.Context, connectionID int64) ([]db.Workspace, error) {
	var out struct {
		Workspaces []db.Workspace `json:"workspaces"`
	}
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/connections/%d/workspaces", connectionID), nil, &out)
	return out.Workspaces, err
}

// summaryView asks for the agent projection whatever the principal.
var summaryView = url.Values{"view": {"destinations"}}

// CreateProject creates an empty logical project group.
func (c *Client) CreateProject(ctx context.Context, title string) (db.ProjectSummary, error) {
	var out db.ProjectSummary
	err := c.do(ctx, http.MethodPost, withQuery("/api/projects", summaryView), map[string]string{"title": title}, &out)
	return out, err
}

// UpdateProject renames or archives a project (title, archived; an omitted
// field keeps its value).
func (c *Client) UpdateProject(ctx context.Context, id int64, p Patch) (db.ProjectSummary, error) {
	var out db.ProjectSummary
	err := c.do(ctx, http.MethodPatch, withQuery(projectPath(id), summaryView), p, &out)
	return out, err
}

// ProjectEvents reads a project's lifecycle events (merges, splits).
func (c *Client) ProjectEvents(ctx context.Context, id int64) ([]db.ProjectEvent, error) {
	var out struct {
		Events []db.ProjectEvent `json:"events"`
	}
	err := c.do(ctx, http.MethodGet, projectPath(id)+"/events", nil, &out)
	return out.Events, err
}

// EditProjectGroup previews (no previewToken) or applies (the preview's
// token) moving one exact workspace into a group, or into a new group when
// TargetProjectID is nil. Raw paths are omitted.
func (c *Client) EditProjectGroup(ctx context.Context, in db.GroupEditInput) (db.GroupEdit, error) {
	var out db.GroupEdit
	err := c.do(ctx, http.MethodPost, withQuery("/api/project-groups/membership", summaryView), in, &out)
	return out, err
}
