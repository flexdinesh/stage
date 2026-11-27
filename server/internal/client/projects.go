package client

import (
	"context"
	"fmt"
	"net/http"

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
	var out ProjectSummaries
	err := c.do(ctx, http.MethodGet, "/api/projects?view=destinations", nil, &out)
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

// Workspaces refreshes a connection's inventory and returns its available rows.
func (c *Client) Workspaces(ctx context.Context, connectionID int64) ([]db.Workspace, error) {
	var out struct {
		Workspaces []db.Workspace `json:"workspaces"`
	}
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/connections/%d/workspaces", connectionID), nil, &out)
	return out.Workspaces, err
}
