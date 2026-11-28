package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
)

// Accepting and driving another task's session (docs/api.md "Acceptance"
// and "Session interaction"). caller names the calling agent's session
// (the Stage-Caller-Session header): the server refuses an action on the
// caller's own task or session.

// CompletionPreview reads what completing a task would accept: its revision
// and evidenceVersion confirm CompleteTask.
func (c *Client) CompletionPreview(ctx context.Context, id int64) (db.CompletionPreview, error) {
	var out db.CompletionPreview
	err := c.do(ctx, http.MethodGet, taskPath(id, "completion-preview"), nil, &out)
	return out, err
}

// CompleteTask accepts a task's evidence at the previewed revision and
// evidenceVersion (409 when either changed).
func (c *Client) CompleteTask(ctx context.Context, id int64, in db.AcceptanceRequest, caller *db.SessionRef) (db.AcceptanceResult, error) {
	var out db.AcceptanceResult
	err := c.doCaller(ctx, http.MethodPost, taskPath(id, "complete"), in, caller, &out)
	return out, err
}

// ReopenTask clears a task's acceptance at revision.
func (c *Client) ReopenTask(ctx context.Context, id int64, in db.AcceptanceRequest, caller *db.SessionRef) (db.AcceptanceResult, error) {
	var out db.AcceptanceResult
	err := c.doCaller(ctx, http.MethodPost, taskPath(id, "reopen"), in, caller, &out)
	return out, err
}

// OpeningItem is an adopted task's opening prompt.
type OpeningItem struct {
	execution.Reply
	Kind      string `json:"kind"`
	Inherited bool   `json:"inherited"`
}

// OpeningPrompt is GET /api/tasks/:id/opening-prompt (Item nil: none).
type OpeningPrompt struct {
	Item *OpeningItem `json:"item"`
}

// OpeningPrompt reads an adopted task's opening prompt (full: untruncated).
func (c *Client) OpeningPrompt(ctx context.Context, id int64, full bool) (OpeningPrompt, error) {
	q := url.Values{}
	if full {
		q.Set("full", "true")
	}
	var out OpeningPrompt
	err := c.do(ctx, http.MethodGet, withQuery(taskPath(id, "opening-prompt"), q), nil, &out)
	return out, err
}

// Requests lists what a task's session waits on.
func (c *Client) Requests(ctx context.Context, id int64) ([]execution.Request, error) {
	var out struct {
		Requests []execution.Request `json:"requests"`
	}
	err := c.do(ctx, http.MethodGet, taskPath(id, "requests"), nil, &out)
	return out.Requests, err
}

// SessionActivity is the recent activity in a task's session.
type SessionActivity struct {
	Activity execution.Activity `json:"activity"`
}

func (c *Client) Activity(ctx context.Context, id int64, attempt *int, q execution.ActivityQuery) (SessionActivity, error) {
	params := url.Values{}
	if attempt != nil {
		params.Set("attempt", strconv.Itoa(*attempt))
	}
	if q.Limit != 0 {
		params.Set("limit", strconv.Itoa(q.Limit))
	}
	var out SessionActivity
	err := c.do(ctx, http.MethodGet, withQuery(taskPath(id, "activity"), params), nil, &out)
	return out, err
}

// Respond answers a pending request of a task's session. expected
// (expectedStateRevision, nil: unguarded) makes it a guarded answer: 409
// already_answered when the task moved on or was answered first.
func (c *Client) Respond(ctx context.Context, id int64, requestID string, in execution.Response, expected *int64, caller *db.SessionRef) (Task, error) {
	var out Task
	body := struct {
		execution.Response
		ExpectedStateRevision *int64 `json:"expectedStateRevision,omitempty"`
	}{in, expected}
	err := c.doCaller(ctx, http.MethodPost, taskPath(id, "requests/"+url.PathEscape(requestID)), body, caller, &out)
	return out, err
}

// SendMessage sends a task's session a message; expected as in Respond.
func (c *Client) SendMessage(ctx context.Context, id int64, in execution.Message, expected *int64, caller *db.SessionRef) (Task, error) {
	var out Task
	body := struct {
		Text                  string             `json:"text"`
		Delivery              execution.Delivery `json:"delivery,omitempty"`
		ExpectedStateRevision *int64             `json:"expectedStateRevision,omitempty"`
	}{in.Text, in.Delivery, expected}
	err := c.doCaller(ctx, http.MethodPost, taskPath(id, "messages"), body, caller, &out)
	return out, err
}

// doCaller is do naming the caller session (nil: none).
func (c *Client) doCaller(ctx context.Context, method, path string, body any, caller *db.SessionRef, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if caller != nil {
		req.Header.Set("Stage-Caller-Session", fmt.Sprintf("%d:%s", caller.ConnectionID, caller.ExternalSessionID))
	}
	_, err = c.send(req, out)
	return err
}
