package agenttools

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/flexdinesh/stage/server/internal/client"
	"github.com/flexdinesh/stage/server/internal/db"
	"github.com/flexdinesh/stage/server/internal/execution"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Accepting other work and driving other sessions. Stage refuses each
// action on the caller's own task or session (403 self_action_forbidden);
// the caller session goes with every write.

const selfRule = " Never on your own task or session: Stage refuses that."

// addWorkTools registers the acceptance and session interaction tools.
func addWorkTools(s *mcp.Server, o *Ops, ro *mcp.ToolAnnotations) {
	add(s, o, "get_completion_preview", "Preview completing a task: the evidence Stage would accept, blockedReason, the dependents it would release, and the revision and evidenceVersion complete_task requires.", ro, o.GetCompletionPreview)
	add(s, o, "complete_task", "Complete (accept) another task's result. Call get_completion_preview first and pass its revision and evidenceVersion; if either changed, re-read the preview. Completion releases the task's dependents in auto trees, which then launch. requestId is an idempotency key."+selfRule, nil, o.CompleteTask)
	add(s, o, "reopen_task", "Reopen a completed task at its current revision (from get_completion_preview); unclaimed dependents wait for acceptance again."+selfRule, nil, o.ReopenTask)
	add(s, o, "get_opening_prompt", "Read an adopted task's opening prompt (full=true: untruncated).", ro, o.GetOpeningPrompt)
	add(s, o, "list_requests", "List what a task's session waits on: approvals, questions and input requests, with their options.", ro, o.ListRequests)
	add(s, o, "respond_request", "Answer a pending request of a task's session with option, text or answers (by question id)."+selfRule, nil, o.RespondRequest)
	add(s, o, "send_message", "Send a task's running session a follow-up message."+selfRule, nil, o.SendMessage)
}

func (o *Ops) GetCompletionPreview(ctx context.Context, a TaskIDArgs) (db.CompletionPreview, error) {
	return o.Client.CompletionPreview(ctx, a.TaskID)
}

// CompleteTaskArgs is complete_task.
type CompleteTaskArgs struct {
	CallerArgs
	TaskID          int64  `json:"taskId" jsonschema:"task to complete (not your own)"`
	Revision        int64  `json:"revision" jsonschema:"revision from get_completion_preview"`
	EvidenceVersion string `json:"evidenceVersion" jsonschema:"evidenceVersion from get_completion_preview"`
	RequestID       string `json:"requestId" jsonschema:"required idempotency key; reuse only when retrying identical content"`
	Note            string `json:"note,omitempty" jsonschema:"optional outcome note (up to 16 KiB), saved to the task journal"`
}

func (o *Ops) CompleteTask(ctx context.Context, a CompleteTaskArgs) (db.AcceptanceResult, error) {
	if strings.TrimSpace(a.EvidenceVersion) == "" {
		return db.AcceptanceResult{}, fmt.Errorf("evidenceVersion is required: call get_completion_preview first")
	}
	c, err := o.caller(ctx, a.CallerArgs)
	if err != nil {
		return db.AcceptanceResult{}, err
	}
	out, err := o.Client.CompleteTask(ctx, a.TaskID, db.AcceptanceRequest{RequestID: a.RequestID, Revision: a.Revision, EvidenceVersion: a.EvidenceVersion, Note: a.Note}, c.Session)
	return out, stalePreview(err)
}

// ReopenTaskArgs is reopen_task.
type ReopenTaskArgs struct {
	CallerArgs
	TaskID    int64  `json:"taskId" jsonschema:"task to reopen (not your own)"`
	Revision  int64  `json:"revision" jsonschema:"revision from get_completion_preview"`
	RequestID string `json:"requestId" jsonschema:"required idempotency key; reuse only when retrying identical content"`
}

func (o *Ops) ReopenTask(ctx context.Context, a ReopenTaskArgs) (db.AcceptanceResult, error) {
	c, err := o.caller(ctx, a.CallerArgs)
	if err != nil {
		return db.AcceptanceResult{}, err
	}
	out, err := o.Client.ReopenTask(ctx, a.TaskID, db.AcceptanceRequest{RequestID: a.RequestID, Revision: a.Revision}, c.Session)
	return out, stalePreview(err)
}

// stalePreview adds the recovery to an acceptance conflict.
func stalePreview(err error) error {
	if client.IsStatus(err, http.StatusConflict) {
		return fmt.Errorf("%w; call get_completion_preview again and retry with its revision and evidenceVersion and a new requestId", err)
	}
	return err
}

// OpeningPromptArgs is get_opening_prompt.
type OpeningPromptArgs struct {
	TaskID int64 `json:"taskId" jsonschema:"adopted task id"`
	Full   bool  `json:"full,omitempty" jsonschema:"return the untruncated prompt"`
}

func (o *Ops) GetOpeningPrompt(ctx context.Context, a OpeningPromptArgs) (client.OpeningPrompt, error) {
	return o.Client.OpeningPrompt(ctx, a.TaskID, a.Full)
}

// RequestsOut is list_requests.
type RequestsOut struct {
	Requests []execution.Request `json:"requests"`
}

func (o *Ops) ListRequests(ctx context.Context, a TaskIDArgs) (RequestsOut, error) {
	rs, err := o.Client.Requests(ctx, a.TaskID)
	return RequestsOut{Requests: rs}, err
}

// RespondRequestArgs is respond_request (execution.Response).
type RespondRequestArgs struct {
	CallerArgs
	TaskID    int64               `json:"taskId" jsonschema:"task whose session asked (not your own)"`
	RequestID string              `json:"requestId" jsonschema:"request id from list_requests"`
	Option    string              `json:"option,omitempty" jsonschema:"chosen option id"`
	Text      string              `json:"text,omitempty" jsonschema:"text answer, or a reason with the request's textOption"`
	Answers   map[string][]string `json:"answers,omitempty" jsonschema:"question id to chosen option labels, for question requests"`
}

func (o *Ops) RespondRequest(ctx context.Context, a RespondRequestArgs) (client.Task, error) {
	c, err := o.caller(ctx, a.CallerArgs)
	if err != nil {
		return client.Task{}, err
	}
	return o.Client.Respond(ctx, a.TaskID, a.RequestID, execution.Response{Option: a.Option, Text: a.Text, Answers: a.Answers}, c.Session)
}

// SendMessageArgs is send_message (execution.Message).
type SendMessageArgs struct {
	CallerArgs
	TaskID int64  `json:"taskId" jsonschema:"task whose session receives it (not your own)"`
	Text   string `json:"text" jsonschema:"message text (up to 32 KiB)"`
}

func (o *Ops) SendMessage(ctx context.Context, a SendMessageArgs) (client.Task, error) {
	c, err := o.caller(ctx, a.CallerArgs)
	if err != nil {
		return client.Task{}, err
	}
	return o.Client.SendMessage(ctx, a.TaskID, execution.Message{Text: a.Text}, c.Session)
}
