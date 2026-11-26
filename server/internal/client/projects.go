package client

import (
	"context"
	"fmt"
	"net/http"

	"github.com/flexdinesh/stage/server/internal/db"
)

// ProjectList is the GET /api/projects response.
type ProjectList struct {
	Projects []db.Project       `json:"projects"`
	Imports  []db.ImportSummary `json:"imports"`
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

// BindProjectInput identifies a discovered workspace, including pathless rows.
type BindProjectInput struct {
	ConnectionID      int64  `json:"connectionId"`
	ExternalProjectID string `json:"externalProjectId"`
	Workspace         string `json:"workspace"`
	Primary           bool   `json:"primary,omitempty"`
	Replaces          *int64 `json:"replaces,omitempty"`
}

func (c *Client) BindProject(ctx context.Context, id int64, in BindProjectInput) (db.Project, error) {
	var out db.Project
	err := c.do(ctx, http.MethodPut, projectPath(id)+"/binding", in, &out)
	return out, err
}

func (c *Client) UnbindProject(ctx context.Context, id, bindingID int64) (db.Project, error) {
	var out db.Project
	err := c.do(ctx, http.MethodDelete, fmt.Sprintf("%s/bindings/%d", projectPath(id), bindingID), nil, &out)
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

// ResolveWorkspaces previews placement, or applies it with its preview token.
func (c *Client) ResolveWorkspaces(ctx context.Context, connectionID int64, apply bool, previewToken string) (db.ResolveResult, error) {
	in := struct {
		Apply        bool   `json:"apply"`
		PreviewToken string `json:"previewToken,omitempty"`
	}{Apply: apply, PreviewToken: previewToken}
	var out db.ResolveResult
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/connections/%d/resolve", connectionID), in, &out)
	return out, err
}

func (c *Client) WorkspaceReview(ctx context.Context) (db.WorkspaceReview, error) {
	var out db.WorkspaceReview
	err := c.do(ctx, http.MethodGet, "/api/workspaces/review", nil, &out)
	return out, err
}

// MergeProjects previews the move from one project into another, or confirms
// the move with the preview token returned by the server.
func (c *Client) MergeProjects(ctx context.Context, into, from int64, confirm bool, previewToken string) (db.MergeResult, error) {
	in := struct {
		From         int64  `json:"from"`
		Confirm      bool   `json:"confirm"`
		PreviewToken string `json:"previewToken,omitempty"`
	}{From: from, Confirm: confirm, PreviewToken: previewToken}
	var out db.MergeResult
	err := c.do(ctx, http.MethodPost, projectPath(into)+"/merge", in, &out)
	return out, err
}

func (c *Client) SplitProject(ctx context.Context, id, bindingID int64, title string) (db.SplitResult, error) {
	in := struct {
		BindingID int64  `json:"bindingId"`
		Title     string `json:"title,omitempty"`
	}{BindingID: bindingID, Title: title}
	var out db.SplitResult
	err := c.do(ctx, http.MethodPost, projectPath(id)+"/split", in, &out)
	return out, err
}
