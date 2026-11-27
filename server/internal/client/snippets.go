package client

import (
	"context"
	"fmt"
	"net/http"

	"github.com/flexdinesh/stage/server/internal/db"
)

func snippetPath(id int64) string { return fmt.Sprintf("/api/snippets/%d", id) }

// Snippets lists saved prompt snippets (global first).
func (c *Client) Snippets(ctx context.Context) ([]db.Snippet, error) {
	var out struct {
		Snippets []db.Snippet `json:"snippets"`
	}
	err := c.do(ctx, http.MethodGet, "/api/snippets", nil, &out)
	return out.Snippets, err
}

// Snippet reads a snippet by id.
func (c *Client) Snippet(ctx context.Context, id int64) (db.Snippet, error) {
	var out db.Snippet
	err := c.do(ctx, http.MethodGet, snippetPath(id), nil, &out)
	return out, err
}

// CreateSnippet creates a snippet (name, body, projectId).
func (c *Client) CreateSnippet(ctx context.Context, p Patch) (db.Snippet, error) {
	var out db.Snippet
	err := c.do(ctx, http.MethodPost, "/api/snippets", p, &out)
	return out, err
}

// UpdateSnippet applies the set fields (projectId nil: global).
func (c *Client) UpdateSnippet(ctx context.Context, id int64, p Patch) (db.Snippet, error) {
	var out db.Snippet
	err := c.do(ctx, http.MethodPatch, snippetPath(id), p, &out)
	return out, err
}

// DeleteSnippet deletes a snippet.
func (c *Client) DeleteSnippet(ctx context.Context, id int64) error {
	return c.do(ctx, http.MethodDelete, snippetPath(id), nil, nil)
}

// DeleteAttachment deletes an image: staged, or on a backlog task.
func (c *Client) DeleteAttachment(ctx context.Context, id int64) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/api/attachments/%d", id), nil, nil)
}
